package wbexecutor_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// 锁：11140 账号受限文案（"restricted due to unusual activity" / 中文"账号存在异常行为"）
// 归 ErrAccountFault（换号有用）；但同一 code 的模型级限流文案必须仍归软限流——
// 按裸码判定会误杀健康账号（ref magpie workbuddy.go WBRestricted + workbuddy2api-panel）。
func TestClassify_11140RestrictedVsRateLimit(t *testing.T) {
	restricted := `{"code":11140,"msg":"request illegal","displayMsg":"Your account has been restricted due to unusual activity"}`
	if got := wbexecutor.Classify(http.StatusForbidden, restricted); got != wbexecutor.ErrAccountFault {
		t.Fatalf("restricted 11140 = %v, want ErrAccountFault", got)
	}
	zh := `{"code":11140,"displayMsg":"账号存在异常行为，已被限制使用"}`
	if got := wbexecutor.Classify(http.StatusForbidden, zh); got != wbexecutor.ErrAccountFault {
		t.Fatalf("zh restricted 11140 = %v, want ErrAccountFault", got)
	}
	// 负例：同码的模型级限流文案不得判封禁。
	rateLimit := `{"code":11140,"msg":"The model provider is rate-limiting requests."}`
	if got := wbexecutor.Classify(http.StatusTooManyRequests, rateLimit); got != wbexecutor.ErrSoftRate {
		t.Fatalf("rate-limit 11140 = %v, want ErrSoftRate (must not be banned)", got)
	}
}

// 锁：14018 积分耗尽不限 429——403/402 亦须归 ErrHardCredit（ref codebuddy-intl-cpa
// failover.go 要求 status>=400；上游实测 403 带 14018）。
func TestClassify_14018Any4xx(t *testing.T) {
	for _, st := range []int{http.StatusForbidden, http.StatusPaymentRequired, http.StatusTooManyRequests} {
		if got := wbexecutor.Classify(st, `{"code":14018,"msg":"credits exhausted"}`); got != wbexecutor.ErrHardCredit {
			t.Errorf("status %d: got %v, want ErrHardCredit", st, got)
		}
	}
	// 负例：200 业务信封带 14018 不在此层（由 checkBusinessError 走 Classify(200,..)）。
	if got := wbexecutor.Classify(http.StatusOK, `{"code":14018}`); got != wbexecutor.ErrNone {
		t.Fatalf("200+14018 at Classify level = %v, want ErrNone (handled by checkBusinessError)", got)
	}
}

// 锁：业务码判定必须容忍上游 JSON 空白/引号形态差异（`"code":14018` / `"code": 14018` /
// `"code":"14018"` / `"code": "14018"`）——字面量拼接会漏判带空白形态，导致 14018 退化
// 为软限流/客户端错误（ref workbuddy2api codeMarker 注释：上游 JSON 含空白时字面量 marker 漏判）。
func TestClassify_BusinessCodeWhitespaceForms(t *testing.T) {
	bodies := []string{
		`{"code":14018}`,
		`{"code": 14018}`,
		`{"code":"14018"}`,
		`{"code": "14018"}`,
	}
	for _, b := range bodies {
		if got := wbexecutor.Classify(http.StatusTooManyRequests, b); got != wbexecutor.ErrHardCredit {
			t.Errorf("body %s: got %v, want ErrHardCredit", b, got)
		}
	}
	// 6004 同款形态也必须命中（共享 helper，防只修一个码）。
	for _, b := range []string{`{"code":6004}`, `{"code": 6004}`, `{"code": "6004"}`} {
		if got := wbexecutor.Classify(http.StatusTooManyRequests, b); got != wbexecutor.ErrModelRateLimit {
			t.Errorf("body %s: got %v, want ErrModelRateLimit", b, got)
		}
	}
	// 负例：`error_code` 等带下划线的字段名不得误命中（`"code":` 要求引号紧邻 code）。
	if got := wbexecutor.Classify(http.StatusTooManyRequests, `{"error_code": 14018}`); got == wbexecutor.ErrHardCredit {
		t.Fatal("error_code field must not match \"code\": marker")
	}
}

// 锁：429 + 业务码 14018 = 账号积分耗尽，必须归 ErrHardCredit（不可自愈的计费终态），
// 不得落 429 通用兜底 ErrSoftRate（软限流会指数退避等自愈，白等且反复选中该号）。
// ref workbuddy2api client.go Classify 第 4 层（issue #175）。
func TestClassify_14018HardCredit(t *testing.T) {
	body := `{"code":14018,"msg":"credits exhausted"}`
	got := wbexecutor.Classify(http.StatusTooManyRequests, body)
	if got != wbexecutor.ErrHardCredit {
		t.Fatalf("Classify(429, 14018) = %v, want ErrHardCredit", got)
	}
	if got == wbexecutor.ErrSoftRate {
		t.Fatal("14018 must not fall to soft_rate fallback")
	}
	// 负例：无 14018 的 429 仍是软限流（不得把普通 429 误升为硬欠费）。
	if g := wbexecutor.Classify(http.StatusTooManyRequests, `{"code":429}`); g != wbexecutor.ErrSoftRate {
		t.Fatalf("plain 429 = %v, want ErrSoftRate", g)
	}
	// 负例：14018 撞在 requestId 等字段不得误判（只认 code 字段值）。
	if g := wbexecutor.Classify(http.StatusTooManyRequests, `{"requestId":"14018abc"}`); g != wbexecutor.ErrSoftRate {
		t.Fatalf("14018 in requestId must not match, got %v", g)
	}
}

// 锁：上游 429 文案「将在 <ts> 重置」/「reset at <ts>」是比 Retry-After 头更权威的
// 恢复时刻口径（ref workbuddy2api ParseRateReset）。时间串固定按 UTC+8 解释，
// 与容器时区无关；无该文案时返回 false（绝不臆造时间）。
func TestParseRateReset_WallClock(t *testing.T) {
	// 固定 now：2026-10-16 12:00:00 UTC+8。目标 13:30:00 UTC+8 → 5400 秒后。
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	cases := []struct {
		name string
		body string
		want int
		ok   bool
	}{
		{"CN 文案", `{"code":11140,"msg":"限流中，将在 2026-10-16 13:30:00 UTC+8 重置"}`, 5400, true},
		{"EN 文案", `{"code":6004,"msg":"rate limited, reset at 2026-10-16 13:30:00"}`, 5400, true},
		{"无文案", `{"code":429,"msg":"too many requests"}`, 0, false},
		{"坏时间", `{"msg":"将在 明天早上 重置"}`, 0, false},
		{"已过期", `{"msg":"将在 2026-10-16 11:00:00 UTC+8 重置"}`, 0, false},
	}
	for _, c := range cases {
		got, ok := wbexecutor.ParseRateReset(c.body, now)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: ParseRateReset=%d,%v want %d,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// 锁：非流 429 同时带 Retry-After 头与 body 文案时，body 墙钟优先（上游更权威）。
func TestExecute_BodyResetBeatsHeader(t *testing.T) {
	now := time.Now()
	reset := now.In(time.FixedZone("UTC+8", 8*3600)).Add(20 * time.Minute)
	body := `{"code":11140,"msg":"将在 ` + reset.Format("2006-01-02 15:04:05") + ` UTC+8 重置"}`
	cfg := wbexecutor.Config{
		Doer:    retryAfterDoer(http.StatusTooManyRequests, body, "30"),
		Degrade: wbexecutor.NewDegradeGate(),
	}
	_, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	if execErr == nil {
		t.Fatal("expected failure")
	}
	if execErr.RetryAfterSec < 1180 || execErr.RetryAfterSec > 1220 {
		t.Fatalf("RetryAfterSec=%d, want ~1200 (body wall clock beats header 30)", execErr.RetryAfterSec)
	}
}
