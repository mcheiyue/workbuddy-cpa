package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// controlHTTPTimeout 控制面直连上游的超时。
const controlHTTPTimeout = 30 * time.Second

// hostHTTPResponse 是宿主 HTTP 回调的响应。
type hostHTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func (r *hostHTTPResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		StatusCodeCamel int         `json:"StatusCode"`
		StatusCodeSnake int         `json:"status_code"`
		Headers         http.Header `json:"Headers"`
		HeadersLower    http.Header `json:"headers"`
		Body            []byte      `json:"Body"`
		BodyLower       []byte      `json:"body"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.StatusCode = wire.StatusCodeCamel
	if r.StatusCode == 0 {
		r.StatusCode = wire.StatusCodeSnake
	}
	r.Headers = wire.Headers
	if r.Headers == nil {
		r.Headers = wire.HeadersLower
	}
	r.Body = wire.Body
	if r.Body == nil {
		r.Body = wire.BodyLower
	}
	return nil
}

// hostRoundTripper 通过 CPA 宿主回调路由 HTTP 请求。
type hostRoundTripper struct {
	callbackID string
	call       func(string, any) (json.RawMessage, error)
}

func (t hostRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}
	request := map[string]any{
		"host_callback_id": t.callbackID,
		"request": map[string]any{
			"Method":  req.Method,
			"URL":     req.URL.String(),
			"Headers": req.Header.Clone(),
			"Body":    body,
		},
	}
	raw, err := t.call(pluginabi.MethodHostHTTPDo, request)
	if err != nil {
		return nil, fmt.Errorf("host HTTP call: %w", err)
	}
	var result hostHTTPResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode host HTTP response: %w", err)
	}
	if result.StatusCode == 0 {
		return nil, fmt.Errorf("host HTTP response missing status code")
	}
	return &http.Response{
		StatusCode: result.StatusCode,
		Header:     result.Headers,
		Body:       io.NopCloser(bytes.NewReader(result.Body)),
		Request:    req,
	}, nil
}

// newHostHTTPClient 创建通过宿主回调路由的 HTTP 客户端。
func newHostHTTPClient(callbackID string) (*http.Client, error) {
	return newHostHTTPClientWithCall(callbackID, callHostJSON)
}

// hostHTTPStreamResponse 是 host.http.do_stream 的响应（不含 body）。
type hostHTTPStreamResponse struct {
	StatusCode int
	Headers    http.Header
	StreamID   string
}

func (r *hostHTTPStreamResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		StatusCodeCamel int         `json:"StatusCode"`
		StatusCodeSnake int         `json:"status_code"`
		Headers         http.Header `json:"Headers"`
		HeadersLower    http.Header `json:"headers"`
		StreamIDCamel   string      `json:"StreamID"`
		StreamIDSnake   string      `json:"stream_id"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.StatusCode = wire.StatusCodeCamel
	if r.StatusCode == 0 {
		r.StatusCode = wire.StatusCodeSnake
	}
	r.Headers = wire.Headers
	if r.Headers == nil {
		r.Headers = wire.HeadersLower
	}
	r.StreamID = wire.StreamIDCamel
	if r.StreamID == "" {
		r.StreamID = wire.StreamIDSnake
	}
	return nil
}

// hostHTTPStreamChunk 是 host.http.stream.read 的单次响应。
type hostHTTPStreamChunk struct {
	Payload []byte
	Done    bool
	Error   string
}

func (c *hostHTTPStreamChunk) UnmarshalJSON(data []byte) error {
	var wire struct {
		PayloadCamel []byte `json:"Payload"`
		PayloadSnake []byte `json:"payload"`
		DoneCamel    bool   `json:"Done"`
		DoneSnake    bool   `json:"done"`
		ErrorCamel   string `json:"Error"`
		ErrorSnake   string `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	c.Payload = wire.PayloadCamel
	if c.Payload == nil {
		c.Payload = wire.PayloadSnake
	}
	c.Done = wire.DoneCamel || wire.DoneSnake
	c.Error = wire.ErrorCamel
	if c.Error == "" {
		c.Error = wire.ErrorSnake
	}
	return nil
}

// hostHTTPDoStream 发起宿主流式 HTTP 请求，返回 stream_id；body 经
// readHostHTTPStream 逐 chunk 拉取。与 do（全缓冲等 EOF）不同：do_stream
// 立即返回响应头，数据到齐 [DONE] 即可收尾，上游 h2 流不 END_STREAM
// （tcpdump 实证 FIN=0）也不会卡死。
func hostHTTPDoStream(callbackID, method, url string, header http.Header, body []byte, call func(string, any) (json.RawMessage, error)) (hostHTTPStreamResponse, error) {
	raw, err := call(pluginabi.MethodHostHTTPDoStream, map[string]any{
		"host_callback_id": callbackID,
		"request": map[string]any{
			"Method":  method,
			"URL":     url,
			"Headers": header,
			"Body":    body,
		},
	})
	if err != nil {
		return hostHTTPStreamResponse{}, fmt.Errorf("host http do_stream: %w", err)
	}
	var resp hostHTTPStreamResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return hostHTTPStreamResponse{}, fmt.Errorf("decode host http stream response: %w", err)
	}
	if strings.TrimSpace(resp.StreamID) == "" {
		return hostHTTPStreamResponse{}, fmt.Errorf("host http stream returned no stream_id")
	}
	return resp, nil
}

// readHostHTTPStream 拉取下一个上游 chunk；done=true 表示上游流结束。
func readHostHTTPStream(callbackID, streamID string, call func(string, any) (json.RawMessage, error)) (hostHTTPStreamChunk, error) {
	raw, err := call(pluginabi.MethodHostHTTPStreamRead, map[string]any{
		"host_callback_id": callbackID,
		"stream_id":        streamID,
	})
	if err != nil {
		return hostHTTPStreamChunk{}, fmt.Errorf("host http stream read: %w", err)
	}
	var chunk hostHTTPStreamChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return hostHTTPStreamChunk{}, fmt.Errorf("decode host http stream chunk: %w", err)
	}
	return chunk, nil
}

// closeHostHTTPStream 关闭宿主上游流（幂等，失败忽略）。
func closeHostHTTPStream(callbackID, streamID string, call func(string, any) (json.RawMessage, error)) {
	_, _ = call(pluginabi.MethodHostHTTPStreamClose, map[string]any{
		"host_callback_id": callbackID,
		"stream_id":        streamID,
	})
}

// makeHostStreamDoer 把宿主流式桥适配为 wbexecutor.StreamDoer。
// callHostJSON 是同步 RPC：do_stream 立即返回 stream_id，随后逐次
// stream.read 拉 chunk（宿主阻塞到下一段到达或流结束）。
func makeHostStreamDoer(callbackID string) wbexecutor.StreamDoer {
	return func(_ context.Context, method, url string, header http.Header, body []byte) (wbexecutor.StreamHandle, error) {
		resp, err := hostHTTPDoStream(callbackID, method, url, header, body, callHostJSON)
		if err != nil {
			return wbexecutor.StreamHandle{}, err
		}
		sid := resp.StreamID
		return wbexecutor.StreamHandle{
			StatusCode: resp.StatusCode,
			Headers:    resp.Headers,
			Read: func() ([]byte, bool, error) {
				chunk, rerr := readHostHTTPStream(callbackID, sid, callHostJSON)
				if rerr != nil {
					return nil, false, rerr
				}
				if chunk.Error != "" {
					return chunk.Payload, false, fmt.Errorf("upstream stream: %s", chunk.Error)
				}
				return chunk.Payload, chunk.Done, nil
			},
			Close: func() { closeHostHTTPStream(callbackID, sid, callHostJSON) },
		}, nil
	}
}

// newDirectControlClient 返回控制面直连上游的 HTTP 客户端。
// 管理 API 与后台 ticker 都不在宿主分发上下文中，没有合法 host_callback_id，
// 走宿主 HTTP 桥会被拒绝（host callback ID is not open），故直连。
// 代价：绕过宿主代理配置；上游经 VPS 直连可达（2026-09-26 实测）。
func newDirectControlClient() (*http.Client, error) {
	return &http.Client{Timeout: controlHTTPTimeout}, nil
}

func newHostHTTPClientWithCall(callbackID string, call func(string, any) (json.RawMessage, error)) (*http.Client, error) {
	if callbackID == "" {
		return nil, fmt.Errorf("host callback ID is required")
	}
	if call == nil {
		return nil, fmt.Errorf("host callback is required")
	}
	return &http.Client{Transport: hostRoundTripper{callbackID: callbackID, call: call}}, nil
}

// isHostHTTPNotFoundError 报告错误是否为宿主 HTTP 404（宿主未实现 MethodHostHTTPDo）。
func isHostHTTPNotFoundError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "unknown method") ||
		strings.Contains(err.Error(), "not_implemented"))
}
