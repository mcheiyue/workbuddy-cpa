package wbexecutor_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// E2 协议回归切片：请求侧历史经 PreparePayload 往返后，reasoning history、
// reasoning_item、tool_calls index、cache_control、is_retry 必须原样存活。
// ref: PLAN E2（2026-10-01 上游增量复核）；输入形状取自 OpenAI 兼容客户端
// 实际会发送的历史形态。

func e2RoundTrip(t *testing.T, src string) map[string]any {
	t.Helper()
	out := wbexecutor.PreparePayload([]byte(src))
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v (out=%s)", err, out)
	}
	return obj
}

func e2Messages(t *testing.T, obj map[string]any) []any {
	t.Helper()
	msgs, ok := obj["messages"].([]any)
	if !ok {
		t.Fatalf("messages missing: %#v", obj["messages"])
	}
	return msgs
}

// reasoning-only assistant（content 为空/缺席）不得被任何重排或清理逻辑丢弃。
func TestE2ReasoningOnlyAssistantSurvives(t *testing.T) {
	obj := e2RoundTrip(t, `{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","reasoning_content":"think","content":""},
		{"role":"assistant","reasoning_content":"only","content":null}
	]}`)
	msgs := e2Messages(t, obj)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3 (reasoning-only assistant dropped)", len(msgs))
	}
	for i, want := range []string{"think", "only"} {
		m, _ := msgs[i+1].(map[string]any)
		if m == nil || m["reasoning_content"] != want {
			t.Errorf("msg[%d] = %#v, want reasoning_content %q", i+1, msgs[i+1], want)
		}
	}
}

// reasoning_item 键整块透传（值不参与任何改写）。
func TestE2ReasoningItemPassthrough(t *testing.T) {
	obj := e2RoundTrip(t, `{"model":"m","messages":[
		{"role":"assistant","content":"","reasoning_content":"s",
		 "reasoning_item":[{"type":"reasoning_text","text":"s"}]}
	]}`)
	m, _ := e2Messages(t, obj)[0].(map[string]any)
	item, present := m["reasoning_item"]
	if !present {
		t.Fatalf("reasoning_item stripped: %#v", m)
	}
	// map 往返会把对象键按字母序重排（无语义变化），按字段断言。
	raw, _ := json.Marshal(item)
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 1 {
		t.Fatalf("reasoning_item = %s, want one element", raw)
	}
	if parts[0]["type"] != "reasoning_text" || parts[0]["text"] != "s" {
		t.Errorf("reasoning_item = %s, want type/text unchanged", raw)
	}
}

// tool_calls 的 index 在配对重排 + 孤儿清理全链后保留。
func TestE2ToolCallIndexSurvivesPairing(t *testing.T) {
	obj := e2RoundTrip(t, `{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":"","tool_calls":[
			{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}},
			{"index":1,"id":"c2","type":"function","function":{"name":"g","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"r1"},
		{"role":"developer","content":"interleave"},
		{"role":"tool","tool_call_id":"c2","content":"r2"}
	]}`)
	msgs := e2Messages(t, obj)
	var assist map[string]any
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm != nil && mm["role"] == "assistant" {
			assist = mm
			break
		}
	}
	if assist == nil {
		t.Fatalf("assistant with tool_calls missing: %#v", msgs)
	}
	calls, _ := assist["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("tool_calls = %d, want 2", len(calls))
	}
	for i, c := range calls {
		cm, _ := c.(map[string]any)
		if idx, ok := cm["index"].(float64); !ok || int(idx) != i {
			t.Errorf("tool_calls[%d].index = %#v, want %d", i, cm["index"], i)
		}
	}
	// 夹在两条结果之间的 developer 后移到组尾（A3a 既有行为，
	// ref tool_pairing.go；developer 已被 normalizeRoles 归一为 system）。
	var roles []string
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		roles = append(roles, mm["role"].(string))
	}
	if joined := strings.Join(roles, ","); joined != "user,assistant,tool,tool,system" {
		t.Errorf("order = %s, want user,assistant,tool,tool,system", joined)
	}
}

// 内容分片上的 cache_control 提示原样透传。
func TestE2CacheControlPassthrough(t *testing.T) {
	obj := e2RoundTrip(t, `{"model":"m","messages":[
		{"role":"system","content":[
			{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}]}
	]}`)
	m, _ := e2Messages(t, obj)[0].(map[string]any)
	parts, _ := m["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("content parts = %d, want 1", len(parts))
	}
	p, _ := parts[0].(map[string]any)
	cc, present := p["cache_control"]
	if !present {
		t.Fatalf("cache_control stripped: %#v", p)
	}
	raw, _ := json.Marshal(cc)
	if string(raw) != `{"type":"ephemeral"}` {
		t.Errorf("cache_control = %s, want unchanged", raw)
	}
}

// 顶层 is_retry=false 等未知键在改写后不被剥离（当前上游无此字段，
// 透传是为协议对齐；若上游将来消费它，行为由线上验证决定）。
func TestE2IsRetryFalseSurvives(t *testing.T) {
	obj := e2RoundTrip(t, `{"model":"m","is_retry":false,"messages":[
		{"role":"user","content":"q"}]}`)
	v, present := obj["is_retry"]
	if !present {
		t.Fatalf("is_retry stripped by PreparePayload")
	}
	if v != false {
		t.Errorf("is_retry = %#v, want false", v)
	}
}
