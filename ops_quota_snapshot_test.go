package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- global 每日余额快照任务（2026-10-14） ---
// 背景：CN 的余额快照挂在 checkin 成功路径（ops_scheduler.go runCheckin），
// global 不跑 checkin（ref D4 防风控）→ global 的每日 +30 活跃奖在 ledger 零记录。
// 本批给 global 补独立 quota 任务（hour 23）；CN 不排，避免一日双记。

// TestBuildWakes_GlobalIncludesQuotaSnapshot global 域含每日 quota 快照任务（hour 23）；
// CN 不排（CN 快照仍走 checkin 路径）。
func TestBuildWakes_GlobalIncludesQuotaSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.Local)
	found := false
	for _, w := range buildWakes(now, wbauth.RealmGlobal) {
		if w.task.name == "quota" {
			found = true
			if w.task.hour != 23 {
				t.Fatalf("quota hour=%d, want 23", w.task.hour)
			}
		}
	}
	if !found {
		t.Fatal("global scope missing daily quota snapshot task")
	}
	for _, w := range buildWakes(now, wbauth.RealmCN) {
		if w.task.name == "quota" {
			t.Fatal("CN must not schedule quota task (CN snapshot lives in checkin path)")
		}
	}
}

// TestRunQuotaSnapshot_RecordsLedgerTicker 快照成功后写 credits-ledger ticker 行
// （auth_id/balance/source 三字段与 checkin 路径同款）；finishTask 记成功。
func TestRunQuotaSnapshot_RecordsLedgerTicker(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"","data":{"Response":{"Data":{"TotalDosage":10000,"Accounts":[{"PackageName":"P1","CapacitySize":5000,"CapacityRemain":3000,"CycleCapacitySize":5000,"CycleCapacityRemain":3000}]}}}}`))
	}}}
	hookErr := "unset"
	ticker := &opsTicker{
		now:        time.Now,
		hostHTTPFn: func(string) (*http.Client, error) { return client, nil },
		tickHook: func(id string, err error) {
			if id != "gq1" {
				t.Errorf("hook id=%q, want gq1", id)
			}
			if err != nil {
				t.Errorf("hook err=%v, want nil", err)
			}
			hookErr = "fired"
		},
	}
	before := len(globalLedger.snapshot())
	ticker.runQuotaSnapshot(accountInfo{
		authIndex:  "gq1",
		callbackID: "gq1",
		realm:      wbauth.RealmGlobal,
		cred:       wbauth.Credential{AccessToken: "tok"},
	})
	if hookErr != "fired" {
		t.Fatal("tickHook not fired")
	}
	snap := globalLedger.snapshot()
	if len(snap) != before+1 {
		t.Fatalf("ledger entries=%d, want %d", len(snap), before+1)
	}
	last := snap[len(snap)-1]
	if last.AuthID != "gq1" || last.Balance != 3000 || last.Source != "ticker" {
		t.Fatalf("last=%+v, want auth=gq1 bal=3000 src=ticker", last)
	}
}

// TestRunQuotaSnapshot_UpstreamErrorNoLedger 上游失败：记 FAIL、不写 ledger（防脏快照）。
func TestRunQuotaSnapshot_UpstreamErrorNoLedger(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":401,"msg":"unauthorized"}`))
	}}}
	var hookErr error
	ticker := &opsTicker{
		now:        time.Now,
		hostHTTPFn: func(string) (*http.Client, error) { return client, nil },
		tickHook:   func(_ string, err error) { hookErr = err },
	}
	before := len(globalLedger.snapshot())
	ticker.runQuotaSnapshot(accountInfo{
		authIndex:  "gq2",
		callbackID: "gq2",
		realm:      wbauth.RealmGlobal,
		cred:       wbauth.Credential{AccessToken: "bad"},
	})
	if hookErr == nil {
		t.Fatal("upstream 401 must record FAIL")
	}
	if len(globalLedger.snapshot()) != before {
		t.Fatal("failed snapshot must not append ledger")
	}
}
