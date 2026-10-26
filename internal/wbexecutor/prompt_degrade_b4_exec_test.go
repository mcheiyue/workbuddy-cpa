package wbexecutor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	blockedBody  = `{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`
	okBody       = `{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	clientSystem = `You are an AI agent powered by DeepSeek Harness.`
)

func contentBlockPayload() []byte {
	return []byte(`{"messages":[{"role":"system","content":"` + clientSystem +
		`"},{"role":"user","content":"hi"}]}`)
}

// 锁：内容/渠道拦截与账号无关（现网实证：同一请求连打 7 个账号全部 11128），
// 必须改用中性提示词重试一次；降级窗开启后，后续请求第一次就发中性提示词，
// 不再每个请求都白撞一次拦截。
func TestExecuteRetriesOnceWithNeutralPromptOnContentBlock(t *testing.T) {
	var bodies []string
	calls := 0
	doer := func(req *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(raw))
		status, body := http.StatusOK, okBody
		if calls == 1 {
			status, body = http.StatusForbidden, blockedBody
		}
		return &http.Response{StatusCode: status, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	gate := NewContentDegradeGate()
	cfg := Config{Doer: doer, ContentDegrade: gate}
	req := ExecuteRequest{AuthID: "a", PublicModelID: "m", Payload: contentBlockPayload()}

	out, err := Execute(context.Background(), cfg, req)
	if err != nil {
		t.Fatalf("neutral retry should succeed, got %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expect a completion body")
	}
	if len(bodies) != 2 {
		t.Fatalf("upstream calls=%d want 2 (initial + one neutral retry)", len(bodies))
	}
	if !strings.Contains(bodies[0], "DeepSeek Harness") {
		t.Fatalf("first attempt must carry the client system prompt, got %s", bodies[0])
	}
	if strings.Contains(bodies[1], "DeepSeek Harness") || !strings.Contains(bodies[1], "helpful assistant") {
		t.Fatalf("retry must use the neutral prompt, got %s", bodies[1])
	}
	if !gate.Active() {
		t.Fatal("a content block must open the degrade window")
	}

	bodies = nil
	if _, err := Execute(context.Background(), cfg, req); err != nil {
		t.Fatalf("execute inside the degrade window: %v", err)
	}
	if len(bodies) != 1 || strings.Contains(bodies[0], "DeepSeek Harness") {
		t.Fatalf("inside the window the first attempt must already be neutral: %d calls, %s",
			len(bodies), strings.Join(bodies, " | "))
	}
}

// 流式路径同款：命中拦截后只重试一次，且第二次已换成中性提示词。
func TestExecuteStreamRetriesOnceWithNeutralPromptOnContentBlock(t *testing.T) {
	var bodies []string
	calls := 0
	cfg := Config{
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
		StreamDoer: func(_ context.Context, _, _ string, _ http.Header, body []byte) (StreamHandle, error) {
			calls++
			bodies = append(bodies, string(body))
			if calls == 1 {
				return oneShotStream(http.StatusForbidden, blockedBody), nil
			}
			return oneShotStream(http.StatusOK,
				"data: "+okBody+"\n\ndata: [DONE]\n\n"), nil
		},
		ContentDegrade: NewContentDegradeGate(),
	}
	req := ExecuteRequest{AuthID: "a", PublicModelID: "m", StreamID: "s1", Payload: contentBlockPayload()}

	if err := ExecuteStream(context.Background(), cfg, req); err != nil {
		t.Fatalf("neutral retry should succeed, got %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("upstream attempts=%d want 2 (initial + one neutral retry)", len(bodies))
	}
	if !strings.Contains(bodies[0], "DeepSeek Harness") {
		t.Fatalf("first attempt must carry the client system prompt, got %s", bodies[0])
	}
	if strings.Contains(bodies[1], "DeepSeek Harness") || !strings.Contains(bodies[1], "helpful assistant") {
		t.Fatalf("retry must use the neutral prompt, got %s", bodies[1])
	}
}

func oneShotStream(status int, payload string) StreamHandle {
	sent := false
	return StreamHandle{
		StatusCode: status,
		Headers:    http.Header{},
		Read: func() ([]byte, bool, error) {
			if sent {
				return nil, true, nil
			}
			sent = true
			return []byte(payload), false, nil
		},
		Close: func() {},
	}
}
