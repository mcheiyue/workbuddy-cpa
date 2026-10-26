package wbexecutor

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// 出口级瞬时退避（ref workbuddy2api-hub wb_egress.py issue #271）。
//
// 账号级降权（degrade.go）把「已知原因连败达阈值」记在**账号**头上，窗口 10 分钟起。
// 当失败来自出口/链路抖动而不是账号本身时，这个口径会把一次抖动记到每个账号头上：
// 参考项目现场实测 4 小时内 196 次 URLError 陆续关掉 13 个账号（最长窗口约 3 小时），
// 期间池子几乎空掉。本文件相反地把这类失败按**出口**聚合：同一出口的账号共用一个
// 计数器，一次抖动只换来一段**秒级** fail-fast，而不是 N 个 10 分钟降权窗。
//
// 出口身份用 realm base URL：我们的账号虽然分 global/cn 两个 realm，但都从同一台
// VPS 出去，所以真正的出口身份由 realm 决定（不同 realm 打不同上游域名）。
//
// 尺度是重点：账号级窗口是**分钟**级，出口级退避是**秒**级（1 秒起、翻倍、封顶 15 秒）。
const (
	egressThreshold   = 3
	egressStreakReset = 60 * time.Second
	egressBackoffBase = time.Second
	egressBackoffMax  = 15 * time.Second
)

type egressState struct {
	streak int
	last   time.Time
}

// EgressGate 按出口键记录连续瞬时失败并给出秒级退避；用 NewEgressGate 构造。
// Now 为测试时间 seam（缺省 time.Now）。nil gate 恒放行。
type EgressGate struct {
	mu    sync.Mutex
	byKey map[string]*egressState
	Now   func() time.Time
}

func NewEgressGate() *EgressGate {
	return &EgressGate{byKey: map[string]*egressState{}, Now: time.Now}
}

func (g *EgressGate) clock() time.Time {
	if g != nil && g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// enter 退避窗内返回 fail-fast 错误（不带上游请求）；否则 nil。
// 只做短暂 skip：连续 3 次瞬时失败后最多挡 15 秒，任一成功立即清零。
func (g *EgressGate) enter(key string) *ExecError {
	if g == nil || key == "" {
		return nil
	}
	g.mu.Lock()
	s := g.byKey[key]
	if s == nil {
		g.mu.Unlock()
		return nil
	}
	streak, last := s.streak, s.last
	g.mu.Unlock()
	if streak < egressThreshold {
		return nil
	}
	now := g.clock()
	wait := egressBackoff(streak)
	if !now.Before(last.Add(wait)) {
		return nil
	}
	left := int(last.Add(wait).Sub(now).Round(time.Second).Seconds())
	if left < 1 {
		left = 1
	}
	return &ExecError{
		Kind:          ErrTransport,
		Status:        http.StatusServiceUnavailable,
		Msg:           fmt.Sprintf("egress backing off after %d consecutive transport failures", streak),
		RetryAfterSec: left,
	}
}

// note 记录一次结果：任一「拿到 HTTP 响应」的结果或成功都清零；
// 只有 ErrTransport（无 HTTP 响应）才计入出口连败。
func (g *EgressGate) note(key string, err *ExecError) {
	if g == nil || key == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil || err.Kind != ErrTransport {
		delete(g.byKey, key)
		return
	}
	now := g.clock()
	s := g.byKey[key]
	if s == nil || now.Sub(s.last) > egressStreakReset {
		s = &egressState{}
		g.byKey[key] = s
	}
	s.streak++
	s.last = now
}

// egressBackoff 连续 streak 次瞬时失败后的退避时长。
func egressBackoff(streak int) time.Duration {
	step := streak - egressThreshold
	if step < 0 {
		step = 0
	}
	if step > 20 {
		step = 20
	}
	d := egressBackoffBase << step
	if d > egressBackoffMax {
		d = egressBackoffMax
	}
	return d
}
