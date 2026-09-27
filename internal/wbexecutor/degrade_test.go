package wbexecutor_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// countingDoer wraps a healthy raw response and counts calls.
func countingDoer(calls *int) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		*calls++
		resp := newDoer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		})
		return resp(req)
	}
}

// B2：已知原因连败 5 次（ref defaultDegradeThreshold）→ 第 6 次 fail-fast，不打上游。
func TestDegrade_FailFastAfterKnownFailures(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	cfg := wbexecutor.Config{Doer: errDoer(http.StatusInternalServerError, `{"code":500}`), Degrade: gate}
	for i := 0; i < 5; i++ {
		if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m")); execErr == nil {
			t.Fatalf("round %d: expected failure", i)
		}
	}
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	_, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m"))
	if execErr == nil || execErr.Kind != wbexecutor.ErrSoftRate || execErr.Status != http.StatusTooManyRequests {
		t.Fatalf("expected fail-fast soft_rate/429, got %v", execErr)
	}
	if calls != 0 {
		t.Fatalf("degraded account must not hit upstream, doer calls=%d", calls)
	}
}

// 成功清零连败计数：4 败 + 1 成功（清零）+ 1 败（计数=1<阈值）后仍可用。
// 若成功不清零：4+1=5 达阈值开窗，健康调用会被 fail-fast 挡下。
func TestDegrade_SuccessResets(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	failing := wbexecutor.Config{Doer: errDoer(http.StatusInternalServerError, `{"code":500}`), Degrade: gate}
	for i := 0; i < 4; i++ {
		wbexecutor.Execute(context.Background(), failing, dummyReq("m"))
	}
	ok := wbexecutor.Config{Doer: rawDoer(`{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`), Degrade: gate}
	if _, execErr := wbexecutor.Execute(context.Background(), ok, dummyReq("m")); execErr != nil {
		t.Fatalf("success call failed: %v", execErr)
	}
	wbexecutor.Execute(context.Background(), failing, dummyReq("m")) // fails=1，未达阈值
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	if _, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m")); execErr != nil {
		t.Fatalf("post-reset call must reach upstream, got %v", execErr)
	}
	if calls != 1 {
		t.Fatalf("post-reset doer calls=%d, want 1", calls)
	}
}

// 未知 4xx（ErrClient / prompt too long）不计数：ref applyErrorPolicy 同口径。
func TestDegrade_UnknownClientErrorsIgnored(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	cfg := wbexecutor.Config{Doer: errDoer(http.StatusBadRequest, `{"code":11115,"msg":"prompt is too long"}`), Degrade: gate}
	for i := 0; i < 10; i++ {
		if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m")); execErr == nil {
			t.Fatal("expected failure")
		}
	}
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	if _, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m")); execErr != nil {
		t.Fatalf("unknown-client-failures must not degrade, got %v", execErr)
	}
	if calls != 1 {
		t.Fatalf("doer calls=%d, want 1", calls)
	}
}

// 冷却窗过期后恢复（now seam 推进 10 分钟）。
func TestDegrade_WindowExpiryRestores(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	now := time.Now()
	gate.Now = func() time.Time { return now }
	cfg := wbexecutor.Config{Doer: errDoer(http.StatusInternalServerError, `{"code":500}`), Degrade: gate}
	for i := 0; i < 5; i++ {
		wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	}
	if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m")); execErr == nil || execErr.Kind != wbexecutor.ErrSoftRate {
		t.Fatalf("expected fail-fast inside window, got %v", execErr)
	}
	now = now.Add(11 * time.Minute)
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	if _, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m")); execErr != nil {
		t.Fatalf("window expired must restore, got %v", execErr)
	}
	if calls != 1 {
		t.Fatalf("doer calls=%d, want 1", calls)
	}
}

// 流式路径同样接入降权（入口 fail-fast + 头部成功清零）。
func TestDegrade_StreamFailFast(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	cfg := wbexecutor.Config{
		StreamDoer:  streamDoerOf(http.StatusInternalServerError, `{"code":500}`),
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
		Degrade:     gate,
	}
	for i := 0; i < 5; i++ {
		if err := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("m")); err == nil {
			t.Fatalf("round %d: expected failure", i)
		}
	}
	streamCalls := 0
	streamCfg := wbexecutor.Config{
		StreamDoer: func(context.Context, string, string, http.Header, []byte) (wbexecutor.StreamHandle, error) {
			streamCalls++
			return streamDoerOf(http.StatusOK, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\ndata: [DONE]\n\n")(context.TODO(), "", "", nil, nil)
		},
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
		Degrade:     gate,
	}
	err := wbexecutor.ExecuteStream(context.Background(), streamCfg, dummyReq("m"))
	if err == nil || err.Kind != wbexecutor.ErrSoftRate {
		t.Fatalf("expected stream fail-fast soft_rate, got %v", err)
	}
	if streamCalls != 0 {
		t.Fatalf("degraded stream must not hit upstream, calls=%d", streamCalls)
	}
}
