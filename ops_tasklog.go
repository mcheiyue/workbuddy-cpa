package main

import (
	"sync"
	"time"
)

// taskLogEntry 单条运营任务完成记录（ref TaskLog 精简同构）。
type taskLogEntry struct {
	Ts     time.Time `json:"ts"`
	Task   string    `json:"task"`
	AuthID string    `json:"auth_id"`
	OK     bool      `json:"ok"`
	Err    string    `json:"err,omitempty"`
}

// taskRing 进程内任务完成环（与 globalLedger 同款：无持久化，重启即清，B4 触发式）。
type taskRing struct {
	mu      sync.Mutex
	entries []taskLogEntry
	max     int
}

func newTaskRing(max int) *taskRing {
	return &taskRing{max: max}
}

func (r *taskRing) append(e taskLogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	if r.max > 0 && len(r.entries) > r.max {
		// 保尾：丢最旧。
		r.entries = append([]taskLogEntry(nil), r.entries[len(r.entries)-r.max:]...)
	}
}

// snapshot 返回拷贝，防调用方改环。
func (r *taskRing) snapshot() []taskLogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == 0 {
		return nil
	}
	out := make([]taskLogEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// globalTaskLog 进程内任务环，cap 对齐 ledger 的 200。
var globalTaskLog = newTaskRing(200)
