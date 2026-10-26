package wbexecutor

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// 锁：无 HTTP 响应的失败必须归 ErrTransport，且 ErrTransport 不得进账号降权集合。
// 归 ErrServer 会让一次出口抖动同时罚掉多个共用出口的账号（ref wb_egress.py #271）。
func TestTransportClassIsNotAccountDegrading(t *testing.T) {
	if degradeKnownKind(ErrTransport) {
		t.Fatal("ErrTransport must not degrade the account")
	}
}

func TestClassifyUpstreamErrorMapsNoResponseToTransport(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrKind
	}{
		{"connection refused", errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), ErrTransport},
		{"deadline", context.DeadlineExceeded, ErrTransport},
		{"canceled", context.Canceled, ErrClient},
	}
	for _, c := range cases {
		got := classifyUpstreamError(c.err)
		if got.Kind != c.want {
			t.Fatalf("%s: kind=%v want=%v", c.name, got.Kind, c.want)
		}
		if c.want == ErrTransport && degradeKnownKind(got.Kind) {
			t.Fatalf("%s: transport failure must not be a degrading kind", c.name)
		}
	}
}

// 锁：连续 egressThreshold 次瞬时失败后出口级 fail-fast；退避到期放行；
// 任一「拿到 HTTP 响应」的结果立即清零（出口已通就不要再背着阶梯）。
func TestEgressGateFailsFastAfterTransientStreak(t *testing.T) {
	g := NewEgressGate()
	now := time.Unix(1_700_000_000, 0)
	g.Now = func() time.Time { return now }
	key := "https://www.workbuddy.ai"
	transient := &ExecError{Kind: ErrTransport, Status: http.StatusBadGateway}

	if e := g.enter(key); e != nil {
		t.Fatalf("no history must pass, got %v", e)
	}
	for i := 0; i < egressThreshold; i++ {
		g.note(key, transient)
	}
	e := g.enter(key)
	if e == nil {
		t.Fatal("expect fail-fast once streak reaches threshold")
	}
	if e.Kind != ErrTransport || e.RetryAfterSec < 1 {
		t.Fatalf("fail-fast=%+v want Kind=transport RetryAfterSec>=1", e)
	}

	now = now.Add(egressBackoffMax + time.Second)
	if e := g.enter(key); e != nil {
		t.Fatalf("after backoff window must pass, got %v", e)
	}

	g.note(key, &ExecError{Kind: ErrServer, Status: http.StatusInternalServerError})
	if e := g.enter(key); e != nil {
		t.Fatalf("non-transport result must clear the streak, got %v", e)
	}
}

// 锁：egressStreakReset 内无新瞬时失败即当新一轮，不接着翻倍。
func TestEgressGateStreakResetsAfterIdle(t *testing.T) {
	g := NewEgressGate()
	now := time.Unix(1_700_000_000, 0)
	g.Now = func() time.Time { return now }
	key := "cn"
	for i := 0; i < egressThreshold; i++ {
		g.note(key, &ExecError{Kind: ErrTransport})
	}
	now = now.Add(egressStreakReset + time.Second)
	g.note(key, &ExecError{Kind: ErrTransport})
	if e := g.enter(key); e != nil {
		t.Fatalf("idle gap must restart the streak, got %v", e)
	}
}

func TestEgressBackoffDoublesAndCaps(t *testing.T) {
	if got := egressBackoff(egressThreshold); got != egressBackoffBase {
		t.Fatalf("first backoff=%v want %v", got, egressBackoffBase)
	}
	if got := egressBackoff(egressThreshold + 1); got != 2*egressBackoffBase {
		t.Fatalf("second backoff=%v want 2x %v", got, egressBackoffBase)
	}
	if got := egressBackoff(99); got != egressBackoffMax {
		t.Fatalf("backoff must cap at %v, got %v", egressBackoffMax, got)
	}
}
