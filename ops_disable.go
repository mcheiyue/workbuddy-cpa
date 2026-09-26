package main

import (
	"log"
	"sync"
)

// sessionDeadThreshold 连续 12153 达到该次数才禁用账号（逐字对照 ref pool/entry.go sessionDeadThreshold）。
// 12153 在真实环境会被临时触发，一次失败不杀号（ref state.go NoteSessionDead 语义）。
const sessionDeadThreshold = 3

// sessionDeadReason 12153 判定为 session 死亡时的禁用原因（对照 ref pool/entry.go sessionDeadReason）。
const sessionDeadReason = "12153 session dead"

// deadCounter 连续 12153 计次与禁用态（键 = authIndex）。
// 进程内存态：重启清零；重登产生新 authIndex 即自然解禁（旧 index 上的禁用残留不影响新登录）。
type deadCounter struct {
	mu       sync.Mutex
	fails    map[string]int    // authIndex → 连续 12153 次数
	disabled map[string]string // authIndex → 禁用原因
}

var deadSessions = newDeadCounter()

func newDeadCounter() *deadCounter {
	return &deadCounter{
		fails:    make(map[string]int),
		disabled: make(map[string]string),
	}
}

// note 记一次 12153；达到阈值转禁用（计次清零、记原因）并返回 true。
func (d *deadCounter) note(authID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fails[authID]++
	if d.fails[authID] < sessionDeadThreshold {
		return false
	}
	delete(d.fails, authID)
	d.disabled[authID] = sessionDeadReason
	log.Printf("[wbops] account disabled auth=%s reason=%s (consecutive x%d)", authID, sessionDeadReason, sessionDeadThreshold)
	return true
}

// clear 成功路径清计次（ref ClearSessionDead：成功证明 session 未死，误判有复活路径）。
// 已禁用态不受影响——解禁只走重登换新 authIndex。
func (d *deadCounter) clear(authID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.fails, authID)
}

// isDisabled 报告账号是否已禁用。
func (d *deadCounter) isDisabled(authID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.disabled[authID]
	return ok
}

// reason 返回禁用原因；未禁用返回空串。
func (d *deadCounter) reason(authID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.disabled[authID]
}

// count 返回当前连续计次（管理面展示 12153×N 进度）。
func (d *deadCounter) count(authID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fails[authID]
}
