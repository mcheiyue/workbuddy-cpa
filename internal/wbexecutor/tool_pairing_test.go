package wbexecutor_test

import (
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// E2 A3a：请求侧重排 + 孤儿清理（ref tool_pairing.go repack/cleanup 经 PreparePayload 落地）。

func prepareMessages(t *testing.T, messagesJSON string) []any {
	t.Helper()
	src := `{"model":"m","messages":` + messagesJSON + `}`
	out := wbexecutor.PreparePayload([]byte(src))
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	msgs, _ := obj["messages"].([]any)
	return msgs
}

func toolCallsOf(t *testing.T, msg map[string]any) []any {
	t.Helper()
	tcs, _ := msg["tool_calls"].([]any)
	return tcs
}

// 插在 assistant.tool_calls 与其 tool 结果之间的 developer 消息必须后移到整组结果之后，
// 否则上游判 11148 tool_call_sequence_broken 顶死会话。
func TestPreparePayload_RepacksInterleavedMessage(t *testing.T) {
	msgs := prepareMessages(t, `[
		{"role":"assistant","tool_calls":[
			{"id":"c0","type":"function","function":{"name":"f","arguments":"{}"}},
			{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c0","content":"r0"},
		{"role":"developer","content":"notice"},
		{"role":"tool","tool_call_id":"c1","content":"r1"}
	]`)
	// 04753c4c：developer 归一 system 后挪到首位，tool 配对相对顺序不变 → 仍 4 条。
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d: %#v", len(msgs), msgs)
	}
	wantRoles := []string{"system", "assistant", "tool", "tool"}
	wantIDs := []string{"", "", "c0", "c1"}
	for i, want := range wantRoles {
		m, _ := msgs[i].(map[string]any)
		if m == nil {
			t.Fatalf("msg[%d] not object: %#v", i, msgs[i])
		}
		if got, _ := m["role"].(string); got != want {
			t.Fatalf("msg[%d] role=%q, want %q", i, got, want)
		}
		if wantIDs[i] != "" {
			if got, _ := m["tool_call_id"].(string); got != wantIDs[i] {
				t.Fatalf("msg[%d] tool_call_id=%q, want %q", i, got, wantIDs[i])
			}
		}
	}
}

// 历史 bug：批内只回了 c1 的结果时，调用侧不能整批删空（会留下孤儿 tool→11148），
// 必须按 keepCalls 双侧对称裁剪，只留配对齐全的 c1。
func TestPreparePayload_PartialBatchKeepsPaired(t *testing.T) {
	msgs := prepareMessages(t, `[
		{"role":"assistant","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}},
			{"id":"c2","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"ok"}
	]`)
	// 04753c4c：注入 leading system → [system, assistant, tool]，配对断言后移。
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d: %#v", len(msgs), msgs)
	}
	first, _ := msgs[1].(map[string]any)
	tcs := toolCallsOf(t, first)
	if len(tcs) != 1 {
		t.Fatalf("want 1 kept tool_call, got %d: %#v", len(tcs), tcs)
	}
	tc0, _ := tcs[0].(map[string]any)
	if id, _ := tc0["id"].(string); id != "c1" {
		t.Fatalf("kept tool_call id=%q, want c1", id)
	}
	second, _ := msgs[2].(map[string]any)
	if id, _ := second["tool_call_id"].(string); id != "c1" {
		t.Fatalf("kept tool result id=%q, want c1", id)
	}
}

// 孤儿 tool 结果（无对应调用）整条删除；配对全灭时 assistant 的 tool_calls 键删除。
func TestPreparePayload_OrphanToolResultDropped(t *testing.T) {
	msgs := prepareMessages(t, `[
		{"role":"assistant","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c2","content":"orphan"}
	]`)
	// 04753c4c：孤儿清理后仅剩 assistant，注入 leading system → [system, assistant]。
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d: %#v", len(msgs), msgs)
	}
	first, _ := msgs[1].(map[string]any)
	if _, has := first["tool_calls"]; has {
		t.Fatalf("tool_calls key must be deleted when all unpaired: %#v", first)
	}
}

// ref b262660b：notice 在首条结果之前时也必须整组后移，否则 11148 残留。
// 既有测试只覆盖"夹在两条结果之间"；这里补首结果前形状（WorkBuddy 实证插入位，
// #1977 家族）。注意断言含 leading system 场景——那是 ensureLeadingSystem 拉不走的
// 真实现网形状，无 leading system 时旧逻辑会被 ensureLeadingSystem 掩盖。
func TestPreparePayload_NoticeBeforeFirstResultMovesAfterGroup(t *testing.T) {
	msgs := prepareMessages(t, `[
		{"role":"system","content":"main"},
		{"role":"user","content":"Read three files"},
		{"role":"assistant","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"developer","content":"notice"},
		{"role":"tool","tool_call_id":"c1","content":"r1"}
	]`)
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages, got %d: %#v", len(msgs), msgs)
	}
	// assistant 紧邻 tool；developer 归一 system 后整组后移到尾部。
	wantRoles := []string{"system", "user", "assistant", "tool", "system"}
	for i, want := range wantRoles {
		m, _ := msgs[i].(map[string]any)
		if got, _ := m["role"].(string); got != want {
			t.Fatalf("msg[%d] role=%q, want %q (msgs=%#v)", i, got, want, msgs)
		}
	}
	tool, _ := msgs[3].(map[string]any)
	if id, _ := tool["tool_call_id"].(string); id != "c1" {
		t.Fatalf("msg[3] tool_call_id=%q, want c1", id)
	}
	notice, _ := msgs[4].(map[string]any)
	if c, _ := notice["content"].(string); c != "notice" {
		t.Fatalf("msg[4] content=%q, want notice", c)
	}
}

// ref b262660b：user 是硬轮次边界——结果收集遇到 user 立即断组，
// 不得把 user 吞进 between 后移到结果之后（跨轮改写历史）。
func TestPreparePayload_UserBoundaryNotCrossed(t *testing.T) {
	msgs := prepareMessages(t, `[
		{"role":"system","content":"main"},
		{"role":"assistant","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}},
			{"id":"c2","type":"function","function":{"name":"f","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"r1"},
		{"role":"user","content":"interrupt"},
		{"role":"tool","tool_call_id":"c2","content":"r2"}
	]`)
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages, got %d: %#v", len(msgs), msgs)
	}
	// user 留在原位（r1 之后、r2 之前），绝不后移。
	wantRoles := []string{"system", "assistant", "tool", "user", "tool"}
	for i, want := range wantRoles {
		m, _ := msgs[i].(map[string]any)
		if got, _ := m["role"].(string); got != want {
			t.Fatalf("msg[%d] role=%q, want %q (msgs=%#v)", i, got, want, msgs)
		}
	}
}

// 无工具流量的普通会话零改动（除 PreparePayload 既有的 stream 等改写外，messages 原样）。
func TestPreparePayload_NoToolTrafficUnchanged(t *testing.T) {
	msgs := prepareMessages(t, `[
		{"role":"system","content":"sys"},
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"}
	]`)
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d: %#v", len(msgs), msgs)
	}
	second, _ := msgs[1].(map[string]any)
	if c, _ := second["content"].(string); c != "hi" {
		t.Fatalf("msg[1] content=%q, want hi", c)
	}
	if _, has := second["tool_calls"]; has {
		t.Fatal("no tool_calls key expected")
	}
}
