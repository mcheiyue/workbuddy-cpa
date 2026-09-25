package wbexecutor_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// --- Payload tests ---

func TestPreparePayload_ForceStreamAndUsage(t *testing.T) {
	out := wbexecutor.PreparePayload([]byte(`{"model":"m","stream":false}`))
	var obj map[string]any
	json.Unmarshal(out, &obj)
	if obj["stream"] != true {
		t.Fatal("stream not forced")
	}
	so, _ := obj["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Fatal("include_usage missing")
	}
}

func TestPreparePayload_MaxCompletionTokens(t *testing.T) {
	out := wbexecutor.PreparePayload([]byte(`{"model":"m","max_completion_tokens":4096}`))
	var obj map[string]any
	json.Unmarshal(out, &obj)
	if _, has := obj["max_completion_tokens"]; has {
		t.Fatal("alias not removed")
	}
	if obj["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens wrong: %v", obj["max_tokens"])
	}
}

func TestPreparePayload_ToolChoiceNone(t *testing.T) {
	out := wbexecutor.PreparePayload([]byte(`{"model":"m","tool_choice":"none","tools":[[]]}`))
	var obj map[string]any
	json.Unmarshal(out, &obj)
	if _, has := obj["tool_choice"]; has {
		t.Fatal("tool_choice not removed")
	}
}

func TestPreparePayload_ToolChoiceObjectFunction(t *testing.T) {
	out := wbexecutor.PreparePayload([]byte(`{"model":"m","tool_choice":{"type":"function","function":{"name":"fn1"}}}`))
	var obj map[string]any
	json.Unmarshal(out, &obj)
	tc, _ := obj["tool_choice"].(string)
	if tc != "fn1" {
		t.Fatalf("tool_choice object not normalized: %v", obj["tool_choice"])
	}
}

func TestPreparePayload_DeveloperRole(t *testing.T) {
	out := wbexecutor.PreparePayload([]byte(`{"model":"m","messages":[{"role":"developer","content":"x"}]}`))
	var obj map[string]any
	json.Unmarshal(out, &obj)
	role := obj["messages"].([]any)[0].(map[string]any)["role"]
	if role != "system" {
		t.Fatalf("developer not normalized: %v", role)
	}
}

// --- Headers tests ---

func TestBuildChatHeaders_CN(t *testing.T) {
	req, _ := http.NewRequest("POST", "/", nil)
	cred := wbexecutor.Credential{
		AccessToken: "tok123", UID: "u1", DeviceToken: "d1",
		EnterpriseID: "e1", Domain: "corp.com", Realm: "cn",
	}
	wbexecutor.BuildChatHeaders(req, cred, "conv1", "", "")
	h := req.Header
	assert := func(k, v string) {
		t.Helper()
		if h.Get(k) != v {
			t.Errorf("Header %s = %q, want %q", k, h.Get(k), v)
		}
	}
	assert("Authorization", "Bearer tok123")
	assert("X-User-Id", "u1")
	assert("X-Enterprise-Id", "e1")
	assert("X-Domain", "corp.com")
	assert("X-Agent-Purpose", "conversation")
	assert("X-Product", "WorkBuddy")
	assert("X-IDE-Type", "WorkBuddy")
	assert("X-Conversation-ID", "conv1")
	assert("X-B3-Sampled", "1")
	assert("X-Device-Token", "d1")
	assert("Origin", "https://www.workbuddy.cn")
	if !strings.Contains(h.Get("User-Agent"), "WorkBuddy/") {
		t.Error("UA missing WorkBuddy/")
	}
}

func TestBuildChatHeaders_Global(t *testing.T) {
	req, _ := http.NewRequest("POST", "/", nil)
	cred := wbexecutor.Credential{AccessToken: "t", UID: "u", Realm: "global"}
	wbexecutor.BuildChatHeaders(req, cred, "", "", "")
	h := req.Header
	if h.Get("Origin") != "https://www.workbuddy.ai" {
		t.Error("global origin wrong")
	}
	if h.Get("X-No-Enterprise-Id") != "1" {
		t.Error("global should X-No-Enterprise-Id")
	}
	if h.Get("Accept-Language") != "en-US" {
		t.Error("global Accept-Language wrong")
	}
}

func TestBuildChatHeaders_NoTokenNoUID(t *testing.T) {
	req, _ := http.NewRequest("POST", "/", nil)
	wbexecutor.BuildChatHeaders(req, wbexecutor.Credential{Realm: "cn"}, "", "", "")
	if req.Header.Get("X-No-Authorization") != "1" {
		t.Error("missing X-No-Authorization")
	}
	if req.Header.Get("X-No-User-Id") != "1" {
		t.Error("missing X-No-User-Id")
	}
}

// --- Classify tests ---

func TestClassify_BusinessCodes(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   wbexecutor.ErrKind
	}{
		{429, `{"code":6004}`, wbexecutor.ErrModelRateLimit},
		{200, `{"code":12153,"msg":"Offline user session not found"}`, wbexecutor.ErrSessionDead},
		{400, `{"code":11128}`, wbexecutor.ErrBadParams},
		{400, `{"code":11102,"msg":"service info not found"}`, wbexecutor.ErrModelBlocked},
		{400, `{"code":11115,"msg":"prompt is too long"}`, wbexecutor.ErrPromptTooLong},
		{502, `bad gateway`, wbexecutor.ErrServer},
		{429, `{"msg":"Rate limit"}`, wbexecutor.ErrSoftRate},
	}
	for _, tt := range tests {
		got := wbexecutor.Classify(tt.status, tt.body)
		if got != tt.want {
			t.Errorf("Classify(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
		}
	}
}

// --- SSE Aggregate tests ---

func TestAggregate_Basic(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	result, err := wbexecutor.Aggregate(strings.NewReader(sse))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	json.Unmarshal(result, &obj)
	content := obj["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"]
	if content != "hello world" {
		t.Fatalf("wrong content: %v", content)
	}
}

func TestAggregate_Reasoning(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"think\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ans\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	result, err := wbexecutor.Aggregate(strings.NewReader(sse))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	json.Unmarshal(result, &obj)
	msg := obj["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["reasoning_content"] != "think" {
		t.Errorf("reasoning wrong: %v", msg["reasoning_content"])
	}
}

func TestAggregate_ToolCalls(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"loc\\\":\\\"NYC\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	result, err := wbexecutor.Aggregate(strings.NewReader(sse))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	json.Unmarshal(result, &obj)
	tcs := obj["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	fn := tcs[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Error("tool name wrong")
	}
	if fn["arguments"] != `{"loc":"NYC"}` {
		t.Errorf("tool args wrong: %v", fn["arguments"])
	}
}

func TestAggregate_EmptyStream(t *testing.T) {
	_, err := wbexecutor.Aggregate(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAggregate_NoDone(t *testing.T) {
	sse := "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"
	_, err := wbexecutor.Aggregate(strings.NewReader(sse))
	if !errors.Is(err, wbexecutor.ErrMissingTerminal) {
		t.Fatalf("expected ErrMissingTerminal, got %v", err)
	}
}
