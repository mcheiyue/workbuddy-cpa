package wbexecutor

import (
	"net/http"
	"sync"
	"time"
)

// B2 账号级连续失败降权（对齐 ref pool/degrade.go issue #114）：
// 已知原因连败达阈值 → 冷却窗内 fail-fast 不打上游（宿主 conductor 自然换号）；
// 成功清零；未知 4xx（ErrClient/ErrPromptTooLong 等）不计——ref applyErrorPolicy 同口径。
const (
	degradeThreshold = 5                // ref defaultDegradeThreshold
	degradeCooldown  = 10 * time.Minute // ref defaultDegradeCooldown
)

type degradeState struct {
	fails int
	until time.Time
}

// DegradeGate 按 AuthID 记连败与降权窗；用 NewDegradeGate 构造。
// Now 为测试时间 seam（缺省 time.Now）。nil gate 恒放行（未启用）。
type DegradeGate struct {
	mu   sync.Mutex
	byID map[string]*degradeState
	Now  func() time.Time
}

func NewDegradeGate() *DegradeGate {
	return &DegradeGate{byID: map[string]*degradeState{}, Now: time.Now}
}

func (g *DegradeGate) clock() time.Time {
	if g != nil && g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// enter 降权窗内返回 fail-fast 错误；否则 nil。
func (g *DegradeGate) enter(authID string) *ExecError {
	if g == nil || authID == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.byID[authID]
	if s == nil || !g.clock().Before(s.until) {
		return nil
	}
	return &ExecError{
		Kind:   ErrSoftRate,
		Status: http.StatusTooManyRequests,
		Msg:    "account degraded: consecutive upstream failures (backing off)",
	}
}

// note 记录一次结果：成功清零；已知原因连败计数，达阈值开冷却窗（窗内不叠加）。
func (g *DegradeGate) note(authID string, err *ExecError) {
	if g == nil || authID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.byID[authID]
	if s == nil {
		s = &degradeState{}
		g.byID[authID] = s
	}
	if err == nil {
		s.fails, s.until = 0, time.Time{}
		return
	}
	if !degradeKnownKind(err.Kind) {
		return
	}
	if g.clock().Before(s.until) {
		return // 窗内失败只等窗结束，不叠加（ref NoteFailures 同款）
	}
	s.fails++
	if s.fails >= degradeThreshold {
		s.fails = 0
		s.until = g.clock().Add(degradeWindow(err))
	}
}

// degradeWindow 开窗时长：上游带 Retry-After 用真实值，但封顶既有最大档
// （degradeCooldown）防超长挂起；无/坏值回退固定档。
func degradeWindow(err *ExecError) time.Duration {
	if err != nil && err.RetryAfterSec > 0 {
		if d := time.Duration(err.RetryAfterSec) * time.Second; d < degradeCooldown {
			return d
		}
	}
	return degradeCooldown
}

// degradeKnownKind：已知原因集（429/5xx/12153/11140/11102/6004/欠费/daily_budget 族）。
func degradeKnownKind(k ErrKind) bool {
	switch k {
	case ErrModelRateLimit, ErrSessionDead, ErrModelBlocked, ErrAccountFault,
		ErrHardCredit, ErrSoftRate, ErrServer, ErrDailyBudget:
		return true
	}
	return false
}
