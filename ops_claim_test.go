package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- isClaimAlreadyDone ---

func TestIsClaimAlreadyDone_409Duplicate(t *testing.T) {
	err := &upstreamError{status: 409, msg: "duplicate: already claimed this tier this month"}
	if !isClaimAlreadyDone(err) {
		t.Fatal("want true for 409 duplicate")
	}
}

func TestIsClaimAlreadyDone_409Chinese(t *testing.T) {
	err := &upstreamError{status: 409, msg: "重复领取"}
	if !isClaimAlreadyDone(err) {
		t.Fatal("want true for 409 重复领取")
	}
}

func TestIsClaimAlreadyDone_Nil(t *testing.T) {
	if isClaimAlreadyDone(nil) {
		t.Fatal("nil error should return false")
	}
}

func TestIsClaimAlreadyDone_403(t *testing.T) {
	err := &upstreamError{status: 403, msg: "记录天数不够"}
	if isClaimAlreadyDone(err) {
		t.Fatal("403 should not be claim-already-done")
	}
}

func TestIsClaimAlreadyDone_500(t *testing.T) {
	err := &upstreamError{status: 500, msg: "internal error"}
	if isClaimAlreadyDone(err) {
		t.Fatal("500 should not be claim-already-done")
	}
}

// --- isClaimNotEnoughDays ---

func TestIsClaimNotEnoughDays_403(t *testing.T) {
	err := &upstreamError{status: 403, msg: "记录天数不够，无法领取该奖励"}
	if !isClaimNotEnoughDays(err) {
		t.Fatal("want true for 403 记录天数不够")
	}
}

func TestIsClaimNotEnoughDays_Nil(t *testing.T) {
	if isClaimNotEnoughDays(nil) {
		t.Fatal("nil should return false")
	}
}

func TestIsClaimNotEnoughDays_409(t *testing.T) {
	err := &upstreamError{status: 409, msg: "duplicate"}
	if isClaimNotEnoughDays(err) {
		t.Fatal("409 should not be not-enough-days")
	}
}

// --- tierStatus ---

func TestTierStatus(t *testing.T) {
	redemption := growthRedemptionStatus{
		Tier7dStatus:  "claimed",
		Tier14dStatus: "available",
		Tier28dStatus: "locked",
	}
	tests := []struct {
		tier, want string
	}{
		{"7d", "claimed"},
		{"14d", "available"},
		{"28d", "locked"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := tierStatus(redemption, tt.tier); got != tt.want {
			t.Errorf("tierStatus(%s) = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

// --- Path constants ---

func TestGrowthPaths(t *testing.T) {
	if growthStreakPath != "/activity/growth/streak" {
		t.Errorf("streakPath = %q", growthStreakPath)
	}
	if growthRedeemPath != "/activity/growth/redeem" {
		t.Errorf("redeemPath = %q", growthRedeemPath)
	}
}

// --- OpsDailyTasks schedule contains claim at slot 3 ---

func TestOpsDailyTasksHasClaimSlot(t *testing.T) {
	if len(opsDailyTasks) != 4 {
		t.Fatalf("opsDailyTasks len=%d, want 4", len(opsDailyTasks))
	}
	found := false
	for _, task := range opsDailyTasks {
		if task.name == "claim" && task.hour == 11 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("opsDailyTasks missing 'claim' at hour 11")
	}
}

// --- buildWakes returns 4 entries ---

func TestBuildWakesReturns4Entries(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now)
	if len(wakes) != 4 {
		t.Fatalf("wakes=%d, want 4", len(wakes))
	}
	names := make(map[string]bool)
	for _, w := range wakes {
		names[w.task.name] = true
	}
	if !names["claim"] {
		t.Fatal("wakes missing claim task")
	}
}

// --- claimCredits integration via mock ---

func TestClaimCredits_TierAvailable(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	mockHandler := func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		if r.Body != nil {
			gotBody, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/activity/growth/streak":
			w.Write([]byte(`{"code":0,"msg":"ok","data":{"streak":{"days":10},"redemption_status":{"tier_7d_status":"claimed","tier_14d_status":"available","tier_28d_status":"locked"}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/activity/growth/redeem":
			w.Write([]byte(`{"code":0,"msg":"ok","data":{"credit_granted":50,"energy_granted":3,"cards_granted":1,"chances_granted":1}}`))
		default:
			w.WriteHeader(404)
		}
	}
	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	result, err := claimCredits(client, wbauth.RealmCN, wbauth.Credential{AccessToken: "test-token", UID: "u1"})
	if err != nil {
		t.Fatalf("claimCredits error: %v", err)
	}
	if result != "claimed:14d" {
		t.Fatalf("result=%q, want claimed:14d", result)
	}
	if gotReq == nil {
		t.Fatal("no upstream request")
	}
	if gotReq.URL.Host != "copilot.tencent.com" {
		t.Fatalf("host=%q, want copilot.tencent.com", gotReq.URL.Host)
	}
	// Verify redeem body
	if gotReq.Method == http.MethodPost {
		var body map[string]any
		if err := json.Unmarshal(gotBody, &body); err != nil {
			t.Fatalf("body parse: %v", err)
		}
		if body["tier"] != "14d" {
			t.Fatalf("tier=%v, want 14d", body["tier"])
		}
	}
}

func TestClaimCredits_AllClaimed(t *testing.T) {
	mockHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"ok","data":{"streak":{"days":30},"redemption_status":{"tier_7d_status":"claimed","tier_14d_status":"claimed","tier_28d_status":"claimed"}}}`))
	}
	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	result, err := claimCredits(client, wbauth.RealmCN, wbauth.Credential{AccessToken: "tok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "none-available" {
		t.Fatalf("result=%q, want none-available", result)
	}
}

func TestClaimCredits_AlreadyClaimed409(t *testing.T) {
	redeemHits := 0
	mockHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"code":0,"msg":"ok","data":{"streak":{"days":10},"redemption_status":{"tier_7d_status":"claimed","tier_14d_status":"available","tier_28d_status":"locked"}}}`))
		case r.Method == http.MethodPost:
			redeemHits++
			w.WriteHeader(409)
			w.Write([]byte(`{"code":409,"msg":"duplicate: already claimed this tier this month","requestId":"x"}`))
		}
	}
	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	result, err := claimCredits(client, wbauth.RealmCN, wbauth.Credential{AccessToken: "tok"})
	if err != nil {
		t.Fatalf("409 should be swallowed, got: %v", err)
	}
	if redeemHits != 1 {
		t.Fatalf("redeem hits=%d, want 1", redeemHits)
	}
	if result != "none-available" {
		t.Fatalf("result=%q, want none-available (409 swallowed)", result)
	}
}

// --- runClaimCredits ticker integration ---

func TestRunClaimCredits_FiresHook(t *testing.T) {
	var hookErr error
	var hookID string
	ticker := &opsTicker{
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"ok","data":{"streak":{"days":5},"redemption_status":{"tier_7d_status":"locked","tier_14d_status":"locked","tier_28d_status":"locked"}}}`))
			}}}, nil
		},
		tickHook: func(id string, err error) {
			hookID = id
			hookErr = err
		},
	}
	ticker.runClaimCredits(accountInfo{authIndex: "cn1", callbackID: "cn1", realm: wbauth.RealmCN})
	if hookID != "cn1" {
		t.Fatalf("hookID=%q, want cn1", hookID)
	}
	if hookErr != nil {
		t.Fatalf("hookErr=%v, want nil", hookErr)
	}
}

func TestRunClaimCredits_HttpClientError(t *testing.T) {
	var hookErr error
	ticker := &opsTicker{
		hostHTTPFn: func(string) (*http.Client, error) {
			return nil, errors.New("connection refused")
		},
		tickHook: func(id string, err error) {
			hookErr = err
		},
	}
	ticker.runClaimCredits(accountInfo{authIndex: "cn2", callbackID: "cn2", realm: wbauth.RealmCN})
	if hookErr == nil || !strings.Contains(hookErr.Error(), "connection refused") {
		t.Fatalf("hookErr=%v, want connection refused", hookErr)
	}
}

// --- Missing imports for time in test ---

// time import is already in scope via the ops_account_test.go which shares the test binary.
