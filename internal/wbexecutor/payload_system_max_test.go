package wbexecutor_test

import (
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// --- leading system normalization (Orchids 04753c4c) ---

func mustPrepare(t *testing.T, src string) map[string]any {
	t.Helper()
	out := wbexecutor.PreparePayload([]byte(src))
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v (out=%s)", err, out)
	}
	return obj
}

func msgRole(t *testing.T, obj map[string]any, i int) string {
	t.Helper()
	msgs, ok := obj["messages"].([]any)
	if !ok || i >= len(msgs) {
		t.Fatalf("messages[%d] missing: %#v", i, obj["messages"])
	}
	m, _ := msgs[i].(map[string]any)
	role, _ := m["role"].(string)
	return role
}

// 上游 11128 实证：messages[0] 非 system 必拒。无 system 时注入缺省。
func TestPreparePayload_MissingSystemInjectsLeadingDefault(t *testing.T) {
	obj := mustPrepare(t, `{"model":"m","messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"}
	]}`)
	if got := msgRole(t, obj, 0); got != "system" {
		t.Fatalf("messages[0] role=%q, want system (11128 guard)", got)
	}
	msgs := obj["messages"].([]any)
	m0, _ := msgs[0].(map[string]any)
	if c, _ := m0["content"].(string); c != "You are a helpful assistant." {
		t.Errorf("default system content=%q, want Orchids defaultSystem", c)
	}
	if got := msgRole(t, obj, 1); got != "user" {
		t.Errorf("messages[1] role=%q, want user (original order kept)", got)
	}
	if len(msgs) != 3 {
		t.Errorf("len=%d, want 3 (default injected, none dropped)", len(msgs))
	}
}

// system 不在首位时挪到首位，其余消息相对顺序不变，content 原样（含数组形态）。
func TestPreparePayload_MiddleSystemMovesToFront(t *testing.T) {
	obj := mustPrepare(t, `{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"system","content":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}]},
		{"role":"assistant","content":"a"}
	]}`)
	if got := msgRole(t, obj, 0); got != "system" {
		t.Fatalf("messages[0] role=%q, want system", got)
	}
	msgs := obj["messages"].([]any)
	m0, _ := msgs[0].(map[string]any)
	parts, ok := m0["content"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("moved system content not array-preserved: %#v", m0["content"])
	}
	p, _ := parts[0].(map[string]any)
	if _, has := p["cache_control"]; !has {
		t.Errorf("cache_control lost in move: %#v", p)
	}
	if got := msgRole(t, obj, 1); got != "user" || msgRole(t, obj, 2) != "assistant" {
		t.Errorf("relative order broken: %s,%s,%s", msgRole(t, obj, 0), msgRole(t, obj, 1), msgRole(t, obj, 2))
	}
}

// 零漂移护栏：首条已是 system 时完全不动（消息数、顺序、content）。
func TestPreparePayload_LeadingSystemUntouched(t *testing.T) {
	obj := mustPrepare(t, `{"model":"m","messages":[
		{"role":"system","content":"sys"},
		{"role":"user","content":"hi"}
	]}`)
	msgs := obj["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("len=%d, want 2 (no injection when system leads)", len(msgs))
	}
	m0, _ := msgs[0].(map[string]any)
	if c, _ := m0["content"].(string); c != "sys" {
		t.Errorf("leading system content=%q, want sys (untouched)", c)
	}
}

// --- max_tokens default 8192 (Orchids 92cc1391) ---

func TestPreparePayload_MissingMaxTokensDefaults8192(t *testing.T) {
	obj := mustPrepare(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	v, has := obj["max_tokens"]
	if !has {
		t.Fatal("max_tokens default missing (upstream working shape expects 8192)")
	}
	if fv, ok := v.(float64); !ok || int(fv) != 8192 {
		t.Errorf("max_tokens=%v, want 8192", v)
	}
}

func TestPreparePayload_ExplicitMaxTokensKept(t *testing.T) {
	obj := mustPrepare(t, `{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	if fv, _ := obj["max_tokens"].(float64); int(fv) != 100 {
		t.Errorf("explicit max_tokens overwritten: %v, want 100", obj["max_tokens"])
	}
}

// --- top-level prompt_cache_key / parallel_tool_calls stripped (Orchids d3b099ef) ---

func TestPreparePayload_StripsCacheKeyAndParallelControls(t *testing.T) {
	obj := mustPrepare(t, `{"model":"m","prompt_cache_key":"pc-1","parallel_tool_calls":false,
		"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`)
	if _, has := obj["prompt_cache_key"]; has {
		t.Error("prompt_cache_key should be omitted (leave cache selection to upstream)")
	}
	if _, has := obj["parallel_tool_calls"]; has {
		t.Error("parallel_tool_calls should be omitted (leave scheduling to upstream)")
	}
}
