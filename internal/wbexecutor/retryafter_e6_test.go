package wbexecutor_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// --- retryAfterDoer: 429 响应带 Retry-After 头 ---

func retryAfterDoer(status int, body, retryAfter string) func(*http.Request) (*http.Response, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", retryAfter)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	return func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = ts.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	}
}

// E6②：ParseRetryAfter 支持 delta-seconds 与 HTTP-date，坏值归零。
func TestParseRetryAfter_Formats(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want int
	}{
		{"30", 30},
		{"0", 0},
		{"-5", 0},
		{"", 0},
		{"garbage", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90},
		{now.Add(-30 * time.Second).Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		if got := wbexecutor.ParseRetryAfter(c.in, now); got != c.want {
			t.Errorf("ParseRetryAfter(%q)=%d, want %d", c.in, got, c.want)
		}
	}
}

// E6②：非流 429 带 Retry-After → ExecError.RetryAfterSec 透传真实值。
func TestExecute_RetryAfterParsedIntoError(t *testing.T) {
	cfg := wbexecutor.Config{
		Doer:    retryAfterDoer(http.StatusTooManyRequests, `{"code":429}`, "45"),
		Degrade: wbexecutor.NewDegradeGate(),
	}
	_, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	if execErr == nil {
		t.Fatal("expected failure")
	}
	if execErr.RetryAfterSec != 45 {
		t.Fatalf("RetryAfterSec=%d, want 45", execErr.RetryAfterSec)
	}
}

// E6②：流式 429 带 Retry-After → ExecError.RetryAfterSec 透传。
func TestExecuteStream_RetryAfterParsedIntoError(t *testing.T) {
	cfg := wbexecutor.Config{
		StreamDoer: func(context.Context, string, string, http.Header, []byte) (wbexecutor.StreamHandle, error) {
			return wbexecutor.StreamHandle{
				StatusCode: http.StatusTooManyRequests,
				Headers:    http.Header{"Retry-After": []string{"60"}},
				Read: func() ([]byte, bool, error) {
					return []byte(`{"code":429}`), true, nil
				},
				Close: func() {},
			}, nil
		},
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
		Degrade:     wbexecutor.NewDegradeGate(),
	}
	execErr := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("m"))
	if execErr == nil {
		t.Fatal("expected failure")
	}
	if execErr.RetryAfterSec != 60 {
		t.Fatalf("RetryAfterSec=%d, want 60", execErr.RetryAfterSec)
	}
}

// E6②：degrade 开窗取 Retry-After 真实值（30s），非固定 10 分钟。
func TestDegrade_RetryAfterOverridesCooldown(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	now := time.Now()
	gate.Now = func() time.Time { return now }
	cfg := wbexecutor.Config{
		Doer:    retryAfterDoer(http.StatusTooManyRequests, `{"code":429}`, "30"),
		Degrade: gate,
	}
	for i := 0; i < 5; i++ {
		if _, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m")); execErr == nil {
			t.Fatalf("round %d: expected failure", i)
		}
	}
	// 窗内（10s 后）仍 fail-fast。
	now = now.Add(10 * time.Second)
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	if _, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m")); execErr == nil {
		t.Fatal("inside retry-after window must fail-fast")
	}
	// 31s 后（> 30s 真实窗）恢复；若仍按固定 10m 开窗则此处会红。
	now = now.Add(21 * time.Second)
	calls = 0
	if _, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m")); execErr != nil {
		t.Fatalf("window must expire after real Retry-After: %v", execErr)
	}
	if calls != 1 {
		t.Fatalf("doer calls=%d, want 1", calls)
	}
}

// E6②：超大 Retry-After 封顶现有最大档（10 分钟），防超长挂起。
func TestDegrade_RetryAfterCappedAtMaxTier(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	now := time.Now()
	gate.Now = func() time.Time { return now }
	cfg := wbexecutor.Config{
		Doer:    retryAfterDoer(http.StatusTooManyRequests, `{"code":429}`, "86400"),
		Degrade: gate,
	}
	for i := 0; i < 5; i++ {
		wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	}
	// 11 分钟后恢复：若未封顶（窗=86400s）此处仍 fail-fast → 红。
	now = now.Add(11 * time.Minute)
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	var execErr *wbexecutor.ExecError
	_, execErr = wbexecutor.Execute(context.Background(), healthy, dummyReq("m"))
	if execErr != nil {
		t.Fatalf("capped window must expire after 11m: %v", execErr)
	}
	if calls != 1 {
		t.Fatalf("doer calls=%d, want 1", calls)
	}
}

// E6②：无 Retry-After 头时保持固定 10 分钟档（既有行为不回退）。
func TestDegrade_NoRetryAfterKeepsFixedCooldown(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	now := time.Now()
	gate.Now = func() time.Time { return now }
	cfg := wbexecutor.Config{Doer: errDoer(http.StatusInternalServerError, `{"code":500}`), Degrade: gate}
	for i := 0; i < 5; i++ {
		wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	}
	now = now.Add(6 * time.Minute) // < 10m 固定档
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	var execErr *wbexecutor.ExecError
	_, execErr = wbexecutor.Execute(context.Background(), healthy, dummyReq("m"))
	if execErr == nil {
		t.Fatal("6m inside fixed 10m window must fail-fast")
	}
	if calls != 0 {
		t.Fatalf("fail-fast must not hit upstream, calls=%d", calls)
	}
}
