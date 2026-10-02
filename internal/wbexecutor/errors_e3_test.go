package wbexecutor_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// ref 拒绝原文（workbuddy2api deff7ae writeOpenAIError，HTTP 429 + 紧凑 JSON）。
const dailyBudgetRefBody = `{"error":{"message":"daily credit budget exhausted (used 5.00 of 5.00, resets at 00:00 CST)","type":"api_error","code":"daily_budget_exceeded"}}`

// E3：daily_budget_exceeded 独立业务限额分类——不落 ErrSoftRate 通用 429 兜底，不落 ErrSessionDead（B1）。
func TestClassify_DailyBudgetExceeded(t *testing.T) {
	got := wbexecutor.Classify(http.StatusTooManyRequests, dailyBudgetRefBody)
	if got != wbexecutor.ErrDailyBudget {
		t.Fatalf("Classify(429, daily_budget_exceeded) = %v, want ErrDailyBudget", got)
	}
	if got == wbexecutor.ErrSoftRate || got == wbexecutor.ErrSessionDead {
		t.Fatalf("must not merge into soft_rate/session_dead, got %v", got)
	}
}

// failure code 字符串 = ref 原码（classifyExecutorError 经 Kind.String() 透传给宿主）。
func TestErrDailyBudgetString(t *testing.T) {
	if s := wbexecutor.ErrDailyBudget.String(); s != "daily_budget_exceeded" {
		t.Fatalf("String() = %q, want daily_budget_exceeded", s)
	}
}

// E3 bullet1：纳入 DegradeGate 已知集——连败 5 次达阈值后第 6 次 fail-fast 不打上游。
func TestDegrade_DailyBudgetCounted(t *testing.T) {
	gate := wbexecutor.NewDegradeGate()
	cfg := wbexecutor.Config{Doer: errDoer(http.StatusTooManyRequests, dailyBudgetRefBody), Degrade: gate}
	for i := 0; i < 5; i++ {
		_, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
		if execErr == nil {
			t.Fatalf("round %d: expected failure", i)
		}
		if execErr.Kind != wbexecutor.ErrDailyBudget {
			t.Fatalf("round %d: kind = %v, want ErrDailyBudget", i, execErr.Kind)
		}
	}
	calls := 0
	healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
	_, execErr := wbexecutor.Execute(context.Background(), healthy, dummyReq("m"))
	if execErr == nil || execErr.Kind != wbexecutor.ErrSoftRate {
		t.Fatalf("expected fail-fast after 5 daily_budget failures, got %v", execErr)
	}
	if calls != 0 {
		t.Fatalf("degraded account must not hit upstream, calls=%d", calls)
	}
}
