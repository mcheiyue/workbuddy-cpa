package wbexecutor_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// E2 A3b（非流路径）：Aggregate 组装后按 ref truncation 语义剔除半截 arguments。
// 只把「非空但无法解析」视为截断；空串是合法无参工具（ref truncation.go）。

type aggCall struct {
	Index    int `json:"index"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func aggregateCalls(t *testing.T, sse string) ([]aggCall, string) {
	t.Helper()
	out, err := wbexecutor.Aggregate(strings.NewReader(sse))
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []aggCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason any `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("want 1 choice, got %d", len(resp.Choices))
	}
	fr, _ := resp.Choices[0].FinishReason.(string)
	return resp.Choices[0].Message.ToolCalls, fr
}

// finish_reason=length 且 arguments 是半截 JSON → 整条 tool_call 剔除。
func TestAggregate_LengthDropsTruncatedToolCall(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"partial\"}}]},\"finish_reason\":\"length\"}]}\n\n" +
		"data: [DONE]\n\n"
	calls, fr := aggregateCalls(t, sse)
	if fr != "length" {
		t.Fatalf("finish=%q, want length", fr)
	}
	if len(calls) != 0 {
		t.Fatalf("truncated tool_call must be dropped, got %#v", calls)
	}
}

// finish_reason=length 但该条 arguments 是完整 JSON → 保留（只剔截断的）。
func TestAggregate_LengthKeepsValidToolCall(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
		"data: [DONE]\n\n"
	calls, fr := aggregateCalls(t, sse)
	if fr != "length" {
		t.Fatalf("finish=%q, want length", fr)
	}
	if len(calls) != 1 {
		t.Fatalf("valid tool_call must survive length, got %#v", calls)
	}
}

// 正常结束（stop）不剔除：完整参数原样保留。
func TestAggregate_StopKeepsToolCalls(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":1}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	calls, fr := aggregateCalls(t, sse)
	if fr != "stop" {
		t.Fatalf("finish=%q, want stop", fr)
	}
	if len(calls) != 1 || calls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("want merged valid call, got %#v", calls)
	}
}

// 空 arguments（合法无参工具）在 length 下也不剔（区别于半截 JSON）。
func TestAggregate_LengthKeepsEmptyArguments(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call1\",\"type\":\"function\",\"function\":{\"name\":\"ping\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
		"data: [DONE]\n\n"
	calls, _ := aggregateCalls(t, sse)
	if len(calls) != 1 {
		t.Fatalf("empty-args tool_call is valid, must survive: %#v", calls)
	}
}
