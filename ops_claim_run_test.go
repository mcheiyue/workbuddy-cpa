package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

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
	if gotReq.URL.Host != "www.workbuddy.cn" {
		t.Fatalf("host=%q, want www.workbuddy.cn", gotReq.URL.Host)
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
