package main

import (
	"net/http"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- billing 域/头回归（对照 reference：chat=copilot、billing=codebuddy.cn 分域 + BillingHeaders） ---

func TestDoBillingJSON_UsesBillingDomainAndHeaders(t *testing.T) {
	var gotReq *http.Request
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"ok":true}}`))
	})
	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	cred := wbauth.Credential{
		AccessToken:  "tk123",
		UID:          "u-1",
		EnterpriseID: "ent-1",
		Domain:       "www.codebuddy.cn",
		DeviceToken:  "dt-1",
	}
	if _, err := doBillingJSON(client, wbauth.RealmCN, cred, http.MethodPost, "/v2/report", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if gotReq == nil {
		t.Fatal("no request captured")
	}
	if gotReq.URL.Host != "www.codebuddy.cn" {
		t.Fatalf("host=%q, want www.codebuddy.cn", gotReq.URL.Host)
	}
	if gotReq.Header.Get("Authorization") != "Bearer tk123" {
		t.Fatalf("authorization=%q", gotReq.Header.Get("Authorization"))
	}
	if gotReq.Header.Get("X-CodeBuddy-Request") != "1" {
		t.Fatalf("x-codebuddy-request=%q", gotReq.Header.Get("X-CodeBuddy-Request"))
	}
	if gotReq.Header.Get("X-User-Id") != "u-1" {
		t.Fatalf("x-user-id=%q", gotReq.Header.Get("X-User-Id"))
	}
	if gotReq.Header.Get("X-Enterprise-Id") != "ent-1" || gotReq.Header.Get("X-Tenant-Id") != "ent-1" {
		t.Fatalf("enterprise headers: %q/%q", gotReq.Header.Get("X-Enterprise-Id"), gotReq.Header.Get("X-Tenant-Id"))
	}
	if gotReq.Header.Get("X-Domain") != "www.codebuddy.cn" {
		t.Fatalf("x-domain=%q", gotReq.Header.Get("X-Domain"))
	}
	if gotReq.Header.Get("X-Device-Token") != "dt-1" {
		t.Fatalf("x-device-token=%q", gotReq.Header.Get("X-Device-Token"))
	}
	if gotReq.Header.Get("Accept-Language") != "zh-CN" {
		t.Fatalf("accept-language=%q", gotReq.Header.Get("Accept-Language"))
	}
	if gotReq.Header.Get("User-Agent") != billingUA {
		t.Fatalf("user-agent=%q", gotReq.Header.Get("User-Agent"))
	}
	if gotReq.Header.Get("Origin") != "" {
		t.Fatalf("origin should not be set on billing path, got %q", gotReq.Header.Get("Origin"))
	}
}

func TestDoBillingJSON_GlobalHostAndLanguage(t *testing.T) {
	var gotReq *http.Request
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
	})
	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	if _, err := doBillingJSON(client, wbauth.RealmGlobal, wbauth.Credential{AccessToken: "tk"}, http.MethodPost, "/v2/report", nil); err != nil {
		t.Fatal(err)
	}
	if gotReq == nil {
		t.Fatal("no request captured")
	}
	if gotReq.URL.Host != "www.workbuddy.ai" {
		t.Fatalf("host=%q, want www.workbuddy.ai", gotReq.URL.Host)
	}
	if gotReq.Header.Get("Accept-Language") != "en-US" {
		t.Fatalf("accept-language=%q", gotReq.Header.Get("Accept-Language"))
	}
	if gotReq.Header.Get("X-User-Id") != "" {
		t.Fatalf("x-user-id should be empty without uid, got %q", gotReq.Header.Get("X-User-Id"))
	}
}
