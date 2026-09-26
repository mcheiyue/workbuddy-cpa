package wbexecutor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Execute 非流式执行：发请求到上游，流式强制 stream=true，
// 本地聚合后返回合法 chat.completion JSON。
func Execute(ctx context.Context, cfg Config, req ExecuteRequest) ([]byte, *ExecError) {
	resolver := cfg.resolver()
	internalModel, err := resolver.ResolveModel(req.AuthID, req.PublicModelID)
	if err != nil {
		return nil, &ExecError{Kind: ErrClient, Status: http.StatusBadRequest,
			Msg: fmt.Sprintf("model resolve failed: %s", sanitizeMsg(err.Error()))}
	}
	body := PreparePayload(req.Payload)
	// 替换 model 为内部 key。
	if err := setModel(body, internalModel); err != nil {
		return nil, &ExecError{Kind: ErrClient, Status: http.StatusBadRequest, Msg: "invalid payload"}
	}
	resp, execErr := doUpstream(ctx, cfg, req, body)
	if execErr != nil {
		return nil, execErr
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "read body failed"}
	}
	if resp.StatusCode >= 400 {
		kind := Classify(resp.StatusCode, string(raw))
		return nil, &ExecError{Kind: kind, Status: resp.StatusCode, Msg: sanitizeMsg(truncateBody(raw))}
	}
	// 上游可能在 HTTP 200 内嵌业务错误。
	if bizErr := checkBusinessError(raw); bizErr != nil {
		return nil, bizErr
	}
	// 替换响应中的 model 为公开 ID（上游返回内部 key，消费方要公开 ID）。
	result := injectModel(raw, req.PublicModelID)
	return result, nil
}

// ExecuteStream 流式执行：经宿主 do_stream 桥直连上游 SSE，逐 chunk 转发裸
// JSON（无 "data: " 前缀）。宿主负责包装 SSE（CPA 统一加 "data: " 前缀）。
// 与旧全缓冲路径的区别：不等上游连接终结（h2 流不 END_STREAM 时旧路径死锁），
// 数据到齐 [DONE] 即收尾。
func ExecuteStream(ctx context.Context, cfg Config, req ExecuteRequest) *ExecError {
	if cfg.StreamEmit == nil || cfg.StreamClose == nil {
		return &ExecError{Kind: ErrClient, Status: http.StatusBadRequest, Msg: "stream seams not configured"}
	}
	if strings.TrimSpace(req.StreamID) == "" {
		return &ExecError{Kind: ErrClient, Status: http.StatusBadRequest, Msg: "stream_id is required"}
	}
	if cfg.StreamDoer == nil {
		return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "stream doer not configured"}
	}
	resolver := cfg.resolver()
	internalModel, err := resolver.ResolveModel(req.AuthID, req.PublicModelID)
	if err != nil {
		return &ExecError{Kind: ErrClient, Status: http.StatusBadRequest,
			Msg: fmt.Sprintf("model resolve failed: %s", sanitizeMsg(err.Error()))}
	}
	body := PreparePayload(req.Payload)
	if err := setModel(body, internalModel); err != nil {
		return &ExecError{Kind: ErrClient, Status: http.StatusBadRequest, Msg: "invalid payload"}
	}
	httpReq, reqErr := buildUpstreamRequest(ctx, req, body)
	if reqErr != nil {
		return reqErr
	}
	handle, doErr := cfg.StreamDoer(ctx, httpReq.Method, httpReq.URL.String(), httpReq.Header.Clone(), body)
	if doErr != nil {
		if errors.Is(doErr, context.Canceled) {
			return &ExecError{Kind: ErrClient, Status: 499, Msg: "request canceled"}
		}
		return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: sanitizeMsg(doErr.Error())}
	}
	defer handle.Close()
	if handle.StatusCode >= 400 {
		raw := drainStream(handle)
		kind := Classify(handle.StatusCode, string(raw))
		return &ExecError{Kind: kind, Status: handle.StatusCode, Msg: sanitizeMsg(truncateBody(raw))}
	}
	// 首字节判定：'{' = HTTP 200 内嵌 JSON（业务错误体），否则按 SSE 转发。
	br := bufio.NewReaderSize(&streamReader{handle: handle}, 64*1024)
	first, perr := br.ReadByte()
	if perr != nil {
		if errors.Is(perr, io.EOF) {
			cfg.StreamClose(req.StreamID, "empty upstream stream")
			return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "upstream stream contained no valid data events"}
		}
		return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "stream read error"}
	}
	if first == '{' {
		rest, _ := io.ReadAll(io.LimitReader(br, 1<<20))
		raw := append([]byte{first}, rest...)
		if bizErr := checkBusinessError(raw); bizErr != nil {
			return bizErr
		}
		// JSON 但非业务错误：br 已耗尽，pumpStream 走 empty-stream 收尾（对齐旧路径）。
		return pumpStream(cfg, req.StreamID, br, req.PublicModelID)
	}
	if err := br.UnreadByte(); err != nil {
		return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "stream read error"}
	}
	return pumpStream(cfg, req.StreamID, br, req.PublicModelID)
}

// pumpStream 从上游 SSE 流逐帧读取，提取裸 JSON chunk，发给宿主。
// 禁止预包 "data: "（CPA 宿主统一加 SSE 前缀）。
func pumpStream(cfg Config, streamID string, r io.Reader, publicModel string) *ExecError {
	br := bufio.NewReaderSize(r, 64*1024)
	sawDone := false
	sawData := false
	for {
		line, err := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed != "" {
			payload, done, ok := ParseSSELine(trimmed)
			if done {
				sawDone = true
				break
			}
			if ok {
				sawData = true
				// 规范化上游全字段平铺格式 + 注入公开 model ID。
				chunk := NormalizeChunk([]byte(payload), publicModel)
				if emitErr := cfg.StreamEmit(streamID, chunk); emitErr != nil {
					return &ExecError{Kind: ErrClient, Status: 0, Msg: "stream emit failed"}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "stream read error"}
		}
	}
	if !sawData && !sawDone {
		cfg.StreamClose(streamID, "empty upstream stream")
		return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "upstream stream contained no valid data events"}
	}
	if !sawDone {
		cfg.StreamClose(streamID, "stream ended without terminal event")
		return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "stream ended without terminal event"}
	}
	cfg.StreamClose(streamID, "")
	return nil
}

// CountTokens 估算 token 数（粗略按 4 字节/token）。
func CountTokens(payload []byte) int {
	tokens := len(payload) / 4
	if tokens == 0 && len(payload) > 0 {
		tokens = 1
	}
	return tokens
}

// doUpstream 构造请求并执行，返回 HTTP 响应和可能的错误。
func doUpstream(ctx context.Context, cfg Config, req ExecuteRequest, body []byte) (*http.Response, *ExecError) {
	if cfg.Doer == nil {
		return nil, &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: "HTTP doer not configured"}
	}
	httpReq, reqErr := buildUpstreamRequest(ctx, req, body)
	if reqErr != nil {
		return nil, reqErr
	}
	resp, err := cfg.Doer(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, &ExecError{Kind: ErrClient, Status: 499, Msg: "request canceled"}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &ExecError{Kind: ErrServer, Status: http.StatusGatewayTimeout, Msg: "upstream timed out"}
		}
		return nil, &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: sanitizeMsg(err.Error())}
	}
	return resp, nil
}

// setModel 在 JSON body 中设置 model 字段。
func setModel(body []byte, model string) error {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return err
	}
	obj["model"] = model
	out, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	copy(body, out)
	return nil
}

// injectModel 在 JSON 中把 model 字段替换为公开 ID（响应侧）。
func injectModel(raw []byte, publicModel string) []byte {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	obj["model"] = publicModel
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// checkBusinessError 检查 HTTP 200 body 是否内嵌业务错误（code 非 0）。
func checkBusinessError(raw []byte) *ExecError {
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil
	}
	if env.Code == 0 {
		return nil
	}
	kind := Classify(http.StatusOK, string(raw))
	if kind == ErrNone {
		kind = ErrClient
	}
	return &ExecError{Kind: kind, Status: http.StatusOK,
		Msg: fmt.Sprintf("code=%d msg=%s", env.Code, truncateBody([]byte(env.Msg)))}
}

// truncateBody 截断 body 用于错误消息（保留 code/type/message，不含 token）。
func truncateBody(raw []byte) string {
	s := string(raw)
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
