package main

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- E1a：ref CheckinHours=[9,21] 双档（scheduler.go:78 配置 + scheduler.go:276 两时点无条件都发） ---

// TestBuildWakes_CheckinDualSlotsCN CN 任务表含 09:00 与 21:00 两个签到槽。
func TestBuildWakes_CheckinDualSlotsCN(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now, wbauth.RealmCN)
	var hours []int
	for _, w := range wakes {
		if w.task.name == "checkin" {
			hours = append(hours, w.task.hour)
		}
	}
	if len(hours) != 2 || hours[0] != 9 || hours[1] != 21 {
		t.Fatalf("CN checkin hours=%v, want [9 21]", hours)
	}
}

// TestBuildWakes_CheckinDualSlotsGlobalExcluded Global 两时点签到槽都不排（ref D4 防风控）。
func TestBuildWakes_CheckinDualSlotsGlobalExcluded(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.Local)
	for _, w := range buildWakes(now, wbauth.RealmGlobal) {
		if w.task.name == "checkin" {
			t.Fatalf("global must not schedule checkin at hour %d", w.task.hour)
		}
	}
}

// TestRunCheckin_AlreadyIsNotErr 21 点补签档 9 点成功后必回 10001；
// ref CheckinAlready 归「幂等重复视为正常」，tickHook 必须收到 nil（不再打 err= 行）。
func TestRunCheckin_AlreadyIsNotErr(t *testing.T) {
	swapDeadCounter(t)
	var gotErr error
	var hooked atomic.Bool
	ticker := &opsTicker{
		now: time.Now,
		hostHTTPFn: func(_ string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":10001,"msg":"今天已签到","data":null}`))
			}}}, nil
		},
		tickHook: func(_ string, err error) {
			gotErr = err
			hooked.Store(true)
		},
	}
	acct := accountInfo{
		authIndex:  "al1",
		callbackID: "al1",
		realm:      wbauth.RealmCN,
		cred:       wbauth.Credential{AccessToken: "tok"},
	}
	ticker.runCheckin(acct)
	if !hooked.Load() {
		t.Fatal("tickHook not fired")
	}
	if gotErr != nil {
		t.Fatalf("already(10001) must count as ok per ref CheckinAlready, got err=%v", gotErr)
	}
}
