package wbexecutor_test

import (
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// workbuddy 上游原始帧形状（全字段平铺，实测抓包 wb3）：
// delta 同时带 content:""、refusal:""、tool_calls:[]、function_call:null、
// extra_fields:null，finish_reason 为 ""。客户端按 key 存在性判定思考块边界，
// 会把每个 token 切成独立 Thought 块（log.txt 74 块问题）。
const wbRawReasoningFrame = `{"choices":[{"delta":{"content":"","extra_fields":null,"function_call":null,"reasoning_content":"We","refusal":"","role":"assistant","tool_calls":[]},"finish_reason":"","index":0,"logprobs":null}],"created":1790440061,"id":"c1","model":"internal-key","object":"chat.completion.chunk","usage":null}`

func TestNormalizeChunk_StripsEmptyDeltaFields(t *testing.T) {
	out := wbexecutor.NormalizeChunk([]byte(wbRawReasoningFrame), "workbuddy/Deepseek-V4.1-Flash")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if obj["model"] != "workbuddy/Deepseek-V4.1-Flash" {
		t.Fatalf("model not injected: %v", obj["model"])
	}
	ch := obj["choices"].([]any)[0].(map[string]any)
	delta := ch["delta"].(map[string]any)
	// 空值字段必须剥离：客户端不再按帧判定边界。
	for _, k := range []string{"content", "refusal", "tool_calls", "function_call", "extra_fields"} {
		if _, has := delta[k]; has {
			t.Errorf("delta.%s should be stripped, got %v", k, delta[k])
		}
	}
	// 活跃字段保留。
	if delta["reasoning_content"] != "We" {
		t.Errorf("reasoning_content lost: %v", delta["reasoning_content"])
	}
	if delta["role"] != "assistant" {
		t.Errorf("role lost: %v", delta["role"])
	}
	// finish_reason "" → null（对齐 OpenAI/qoder 惯例）。
	if v, has := ch["finish_reason"]; !has || v != nil {
		t.Errorf("finish_reason should be null, has=%v v=%v", has, v)
	}
	// 顶层 null usage 删除。
	if _, has := obj["usage"]; has {
		t.Errorf("null usage should be stripped")
	}
}

func TestNormalizeChunk_PreservesActiveContentAndTerminalUsage(t *testing.T) {
	raw := `{"choices":[{"delta":{"content":"Hi","extra_fields":null,"function_call":null,"reasoning_content":"","refusal":"","tool_calls":[]},"finish_reason":"stop","index":0}],"id":"c1","model":"k","object":"chat.completion.chunk","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	out := wbexecutor.NormalizeChunk([]byte(raw), "pub/model")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	ch := obj["choices"].([]any)[0].(map[string]any)
	delta := ch["delta"].(map[string]any)
	if delta["content"] != "Hi" {
		t.Errorf("content lost: %v", delta["content"])
	}
	if _, has := delta["reasoning_content"]; has {
		t.Errorf("empty reasoning_content should be stripped")
	}
	if ch["finish_reason"] != "stop" {
		t.Errorf("finish_reason stop must survive: %v", ch["finish_reason"])
	}
	usage, has := obj["usage"].(map[string]any)
	if !has || usage["total_tokens"] != float64(3) {
		t.Errorf("terminal usage must survive: %v", obj["usage"])
	}
}

func TestNormalizeChunk_InvalidJSONPassthrough(t *testing.T) {
	out := wbexecutor.NormalizeChunk([]byte("not-json"), "m")
	if string(out) != "not-json" {
		t.Errorf("invalid json must pass through, got %q", out)
	}
}
