package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- realm 分表：buildWakes 按 scope 结构性排除（零唤醒零上游调用） ---

func TestBuildWakes_CNScopeExcludesTrialAndGlobalOnly(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now, wbauth.RealmCN)
	names := make(map[string]bool, len(wakes))
	for _, w := range wakes {
		names[w.task.name] = true
	}
	if len(wakes) != 4 {
		t.Fatalf("CN wakes=%d, want 4 (checkin/activity/claim/keepalive)", len(wakes))
	}
	if names["trial"] {
		t.Fatal("CN scope must not schedule trial (global-only)")
	}
	for _, want := range []string{"checkin", "activity", "claim", "keepalive"} {
		if !names[want] {
			t.Fatalf("CN scope missing %q", want)
		}
	}
}

func TestBuildWakes_GlobalScopeExcludesCheckinAndClaim(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now, wbauth.RealmGlobal)
	names := make(map[string]bool, len(wakes))
	for _, w := range wakes {
		names[w.task.name] = true
	}
	if len(wakes) != 3 {
		t.Fatalf("global wakes=%d, want 3 (activity/trial/keepalive)", len(wakes))
	}
	if names["checkin"] {
		t.Fatal("global scope must not schedule checkin (ref D4 防风控)")
	}
	if names["claim"] {
		t.Fatal("global scope must not schedule claim (ref global growth 500)")
	}
	for _, want := range []string{"activity", "trial", "keepalive"} {
		if !names[want] {
			t.Fatalf("global scope missing %q", want)
		}
	}
}

// --- claimTrial 幂等：14051 两种上游指纹都归成功 ---

func TestClaimTrial_Success(t *testing.T) {
	var got *http.Request
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
	}}}
	cred := wbauth.Credential{AccessToken: "at", UID: "global_uid_456"}
	if err := claimTrial(client, wbauth.RealmGlobal, cred); err != nil {
		t.Fatalf("claimTrial err=%v, want nil", err)
	}
	if got == nil {
		t.Fatal("no upstream request")
	}
	if got.Method != http.MethodPost {
		t.Fatalf("method=%q, want POST", got.Method)
	}
	if got.URL.Host != "www.workbuddy.ai" {
		t.Fatalf("host=%q, want www.workbuddy.ai", got.URL.Host)
	}
	if got.URL.Path != "/billing/ide/trial" {
		t.Fatalf("path=%q, want /billing/ide/trial", got.URL.Path)
	}
	if got.Header.Get("Authorization") != "Bearer at" {
		t.Fatalf("auth=%q", got.Header.Get("Authorization"))
	}
}

func TestClaimTrial_AlreadyEnvelopeIdempotent(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":14051,"msg":"already claimed"}`))
	}}}
	if err := claimTrial(client, wbauth.RealmGlobal, wbauth.Credential{AccessToken: "at"}); err != nil {
		t.Fatalf("14051 envelope should be nil, got %v", err)
	}
}

func TestClaimTrial_AlreadyHTTP4xxRawBodyIdempotent(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":14051,"message":"already claimed"}`))
	}}}
	if err := claimTrial(client, wbauth.RealmGlobal, wbauth.Credential{AccessToken: "at"}); err != nil {
		t.Fatalf("14051 raw body should be nil, got %v", err)
	}
}

func TestClaimTrial_OtherErrorPropagates(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}}}
	err := claimTrial(client, wbauth.RealmGlobal, wbauth.Credential{AccessToken: "at"})
	if err == nil {
		t.Fatal("500 must propagate")
	}
	if isTrialAlready(err) {
		t.Fatalf("500 must not be treated as 14051: %v", err)
	}
}

// --- runTrial 集成：freshCred 重读 + finishTask 记账 ---

func TestRunTrial_FreshCredAndHook(t *testing.T) {
	var gotPath string
	ticker := &opsTicker{
		callHostFn: mockCallHost(t),
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
			}}}, nil
		},
		tickHook: func(id string, err error) {
			if id != "global1" {
				t.Errorf("hook id=%q, want global1", id)
			}
			if err != nil {
				t.Errorf("hook err=%v, want nil", err)
			}
		},
	}
	ticker.runTrial(accountInfo{authIndex: "global1", callbackID: "global1", realm: wbauth.RealmGlobal})
	if gotPath != "/billing/ide/trial" {
		t.Fatalf("path=%q, want /billing/ide/trial", gotPath)
	}
}

func TestRunTrial_FreshCredFailureLogged(t *testing.T) {
	hookErrs := 0
	ticker := &opsTicker{
		callHostFn: func(method string, payload any) (json.RawMessage, error) {
			return nil, errors.New("auth get failed")
		},
		tickHook: func(id string, err error) {
			if err != nil {
				hookErrs++
			}
		},
	}
	ticker.runTrial(accountInfo{authIndex: "gx", callbackID: "gx", realm: wbauth.RealmGlobal})
	if hookErrs != 1 {
		t.Fatalf("hookErrs=%d, want 1 (freshCred failure must be recorded)", hookErrs)
	}
}
