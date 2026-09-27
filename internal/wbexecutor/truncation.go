package wbexecutor

import "encoding/json"

// E2 A3b：流/组装侧截断剔除。
// ref: workbuddy2api/internal/upstream/truncation.go
//
// finish_reason=length 截断在工具调用参数中间时 arguments 是半截 JSON；
// 客户端 JSON.parse 会炸。判据：非空且无法解析为 JSON 对象才算截断——
// 空串是合法无参工具，原样保留（ref 语义，区别于直接判空删）。

// isTruncatedArguments 判断参数是否为截断残留：非空且不是合法 JSON 对象。
func isTruncatedArguments(raw string) bool {
	if raw == "" {
		return false
	}
	var tmp map[string]any
	return json.Unmarshal([]byte(raw), &tmp) != nil
}

// dropTruncatedCalls 剔除半截参数的调用，保留完整与空参调用。
func dropTruncatedCalls(calls []aggregateToolCall) []aggregateToolCall {
	kept := make([]aggregateToolCall, 0, len(calls))
	for _, c := range calls {
		if isTruncatedArguments(c.Function.Arguments) {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}
