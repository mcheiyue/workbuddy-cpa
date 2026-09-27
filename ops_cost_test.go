package main

import (
	"encoding/json"
	"testing"
	"time"
)

// E4/C4：ref NoteModelCost 同款聚合口径 —— per1k=credit/tokens*1000，
// EMA α=0.3（首见直存），samples 累计，credit 缺失不入账由提取层保证。
func TestCostLedgerEMA(t *testing.T) {
	l := newCostLedger()
	l.note("a1", "m1", 2.0, 2000) // per1k = 1.0，首见直存
	l.note("a1", "m1", 2.5, 1000) // per1k = 2.5 → EMA 0.3*2.5 + 0.7*1.0 = 1.45

	got, ok := l.get("a1", "m1")
	if !ok {
		t.Fatal("entry missing")
	}
	if got.CostPer1k < 1.4499 || got.CostPer1k > 1.4501 {
		t.Errorf("CostPer1k=%v want 1.45", got.CostPer1k)
	}
	if got.Samples != 2 {
		t.Errorf("Samples=%d want 2", got.Samples)
	}
	if got.LastSeen.IsZero() {
		t.Error("LastSeen not set")
	}
}

func TestCostLedgerGuards(t *testing.T) {
	l := newCostLedger()
	l.note("a1", "m1", 2.0, 0) // tokens<=0 不入账
	if _, ok := l.get("a1", "m1"); ok {
		t.Error("tokens<=0 should not create entry")
	}
	l.note("", "m1", 1, 100) // 空 authID 不入账
	l.note("a1", "", 1, 100) // 空 model 不入账
	if l.len() != 0 {
		t.Errorf("len=%d want 0", l.len())
	}
}

// handler 响应必须带上 model_costs 段（WebUI 卡片消费源）。
func TestCreditsLedgerHandlerIncludesModelCosts(t *testing.T) {
	globalCosts.note("a1", "m1", 2.0, 2000)
	defer func() { globalCosts.reset() }()

	resp, err := defaultOpsManagementService.creditsLedgerHandler()
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body struct {
		Entries    []ledgerEntry                        `json:"entries"`
		ModelCosts map[string]map[string]modelCostEntry `json:"model_costs"`
		Note       string                               `json:"note"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal resp.Body: %v", err)
	}
	mc, ok := body.ModelCosts["a1"]["m1"]
	if !ok {
		t.Fatalf("model_costs missing a1/m1: %v", body.ModelCosts)
	}
	if mc.Samples != 1 {
		t.Errorf("Samples=%d want 1", mc.Samples)
	}
}

func TestCostLedgerSnapshotIsolation(t *testing.T) {
	l := newCostLedger()
	l.note("a1", "m1", 1, 100)
	snap := l.snapshot()
	l.note("a1", "m1", 1, 100)
	if snap["a1"]["m1"].Samples != 1 {
		t.Errorf("snapshot mutated: %d", snap["a1"]["m1"].Samples)
	}
	_ = time.Now
}
