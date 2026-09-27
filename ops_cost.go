package main

import (
	"sync"
	"time"
)

// E4/C4 消耗台账：ref pool.NoteModelCost 同款语义 —— per (authID, model)，
// per1k = credit/tokens*1000，EMA α=0.3 聚合（首见直存），samples 累计。
// credit 缺失（非 0）在 wbexecutor 提取层就不触发 OnUsage，本层只接已判定的观测。
type modelCostEntry struct {
	CostPer1k float64   `json:"cost_per_1k"`
	LastSeen  time.Time `json:"last_seen"`
	Samples   int       `json:"samples"`
}

const costEMAAlpha = 0.3

type costLedger struct {
	mu sync.Mutex
	// authID -> model -> entry
	by map[string]map[string]modelCostEntry
}

func newCostLedger() *costLedger {
	return &costLedger{by: make(map[string]map[string]modelCostEntry)}
}

var globalCosts = newCostLedger()

// note 记录一次已判定存在的 usage.credit 观测。
func (l *costLedger) note(authID, model string, credit float64, tokens int) {
	if authID == "" || model == "" || tokens <= 0 {
		return
	}
	per1k := credit / float64(tokens) * 1000
	if per1k < 0 {
		per1k = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.by[authID]
	if m == nil {
		m = make(map[string]modelCostEntry)
		l.by[authID] = m
	}
	now := time.Now()
	prev, ok := m[model]
	if !ok {
		m[model] = modelCostEntry{CostPer1k: per1k, LastSeen: now, Samples: 1}
		return
	}
	m[model] = modelCostEntry{
		CostPer1k: costEMAAlpha*per1k + (1-costEMAAlpha)*prev.CostPer1k,
		LastSeen:  now,
		Samples:   prev.Samples + 1,
	}
}

func (l *costLedger) get(authID, model string) (modelCostEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.by[authID][model]
	return e, ok
}

func (l *costLedger) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.by)
}

// snapshot 返回深拷贝，隔离后续 note。
func (l *costLedger) snapshot() map[string]map[string]modelCostEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]map[string]modelCostEntry, len(l.by))
	for uid, mm := range l.by {
		cp := make(map[string]modelCostEntry, len(mm))
		for m, e := range mm {
			cp[m] = e
		}
		out[uid] = cp
	}
	return out
}

func (l *costLedger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.by = make(map[string]map[string]modelCostEntry)
}
