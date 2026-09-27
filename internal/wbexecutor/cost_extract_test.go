package wbexecutor_test

import (
	"context"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// C4：usage.credit 消耗观测——credit 存在（含 0）才记账，缺失不记（ref
// handler_cost P0：缺失≠0，防零成本污染账本）；tokens 取 total_tokens。

type usageCall struct {
	authID string
	model  string
	credit float64
	tokens int
}

func captureUsage() (*[]usageCall, func(string, string, float64, int)) {
	calls := &[]usageCall{}
	return calls, func(a, m string, c float64, t int) {
		*calls = append(*calls, usageCall{authID: a, model: m, credit: c, tokens: t})
	}
}

func TestExecute_NonStreamUsageNoted(t *testing.T) {
	resp := `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"usage":{"credit":2.5,"total_tokens":2000,"prompt_tokens":800,"completion_tokens":1200}}`
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{Doer: rawDoer(resp), OnUsage: onUsage}
	if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("public-m")); execErr != nil {
		t.Fatal(execErr)
	}
	if len(*calls) != 1 {
		t.Fatalf("OnUsage calls=%d want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.authID != "test-auth" || got.model != "public-m" || got.credit != 2.5 || got.tokens != 2000 {
		t.Fatalf("got %+v want {test-auth public-m 2.5 2000}", got)
	}
}

func TestExecute_NonStreamUsageCreditMissingNotNoted(t *testing.T) {
	// usage 在但 credit 字段缺失（ref P0：不得按 0 记账）。
	resp := `{"choices":[],"usage":{"total_tokens":2000,"prompt_tokens":1000,"completion_tokens":1000}}`
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{Doer: rawDoer(resp), OnUsage: onUsage}
	if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m")); execErr != nil {
		t.Fatal(execErr)
	}
	if len(*calls) != 0 {
		t.Fatalf("missing credit must not be noted, got %+v", *calls)
	}
}

func TestExecute_NonStreamUsageCreditZeroNoted(t *testing.T) {
	resp := `{"choices":[],"usage":{"credit":0,"total_tokens":7,"prompt_tokens":6,"completion_tokens":1}}`
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{Doer: rawDoer(resp), OnUsage: onUsage}
	if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m")); execErr != nil {
		t.Fatal(execErr)
	}
	if len(*calls) != 1 || (*calls)[0].credit != 0 || (*calls)[0].tokens != 7 {
		t.Fatalf("credit=0 is a valid observation, got %+v", *calls)
	}
}

func TestExecuteStream_UsageNoted(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"credit":1.0,"total_tokens":1000,"prompt_tokens":400,"completion_tokens":600}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{
		StreamDoer:  streamDoerOf(200, body),
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
		OnUsage:     onUsage,
	}
	if err := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("public-s")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("stream usage calls=%d want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.authID != "test-auth" || got.model != "public-s" || got.credit != 1.0 || got.tokens != 1000 {
		t.Fatalf("got %+v", got)
	}
}

func TestExecuteStream_UsageCreditMissingNotNoted(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":1000}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{
		StreamDoer:  streamDoerOf(200, body),
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
		OnUsage:     onUsage,
	}
	if err := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("m")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("missing credit must not be noted, got %+v", *calls)
	}
}
