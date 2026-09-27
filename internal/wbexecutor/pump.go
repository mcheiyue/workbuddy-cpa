package wbexecutor

import (
	"bufio"
	"io"
	"net/http"
	"strings"
)

// pumpStream 从上游 SSE 流逐帧读取，提取裸 JSON chunk，发给宿主。
// 禁止预包 "data: "（CPA 宿主统一加 SSE 前缀）。
//
// E2 A3b：tool_calls 帧经 toolCallHold 缓存到 finish/[DONE] 决策点——
// finish_reason=length 时剔除半截 arguments（ref truncation 语义），
// 其余场景按 index 聚合后完整放行；EOF 无 [DONE] 随既有错误关闭丢弃缓存。
// 无 tool_calls 的常见流走 bytes.Contains 快路径，行为与旧版逐帧直通一致。
func pumpStream(cfg Config, streamID string, r io.Reader, publicModel string) *ExecError {
	br := bufio.NewReaderSize(r, 64*1024)
	sawDone := false
	sawData := false
	hold := &toolCallHold{}
	for {
		line, err := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed != "" {
			payload, done, ok := ParseSSELine(trimmed)
			if done {
				sawDone = true
				if execErr := hold.flush(cfg, streamID, publicModel, false, nil); execErr != nil {
					return execErr
				}
				break
			}
			if ok {
				sawData = true
				// 规范化上游全字段平铺格式 + 注入公开 model ID。
				chunk := NormalizeChunk([]byte(payload), publicModel)
				if execErr := hold.feed(cfg, streamID, publicModel, chunk); execErr != nil {
					return execErr
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
	// EOF 断流：缓存的 tool_calls（可能截断）不外发，随下方错误关闭一并作废。
	hold.reset()
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

// toolCallHold 缓存 tool_calls 帧并按 index 聚合参数，供 finish/[DONE] 决策点一次性放行。
// 只在帧真实携带 tool_calls 时激活；激活前所有帧字节级直通。
type toolCallHold struct {
	active bool
	calls  map[int]*aggregateToolCall
	order  []int
}
