package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- quota.identifier ---

func TestQuotaIdentifierReturnsWorkBuddy(t *testing.T) {
	raw, err := handleMethod("quota.identifier", nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, got %s", raw)
	}
	var result struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Identifier != wbauth.Provider {
		t.Fatalf("identifier=%q, want %q", result.Identifier, wbauth.Provider)
	}
}

// --- quota.describe ---

func TestQuotaDescribeReturnsSupportedProviders(t *testing.T) {
	raw, err := handleMethod("quota.describe", nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, got %s", raw)
	}
	var result struct {
		SupportedProviders []string `json:"supported_providers"`
		DisplayName        string   `json:"display_name"`
		SupportsReset      bool     `json:"supports_reset"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.SupportedProviders) != 1 || result.SupportedProviders[0] != wbauth.Provider {
		t.Fatalf("supported_providers=%v", result.SupportedProviders)
	}
	if result.DisplayName != "WorkBuddy" {
		t.Fatalf("display_name=%q", result.DisplayName)
	}
	if result.SupportsReset {
		t.Fatal("supports_reset should be false")
	}
}

// --- quota.reset (不支持) ---

func TestQuotaResetReturnsNotSupported(t *testing.T) {
	raw, err := handleMethod("quota.reset", nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, got %s", raw)
	}
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Fatal("success should be false")
	}
	if !strings.Contains(result.Message, "does not support quota reset") {
		t.Fatalf("message=%q", result.Message)
	}
}

// --- quota.fetch: invalid credential ---

func TestQuotaFetchInvalidCredentialReturnsError(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"provider":    wbauth.Provider,
		"storage_json": []byte("garbage"),
	})
	resp, err := handleMethod("quota.fetch", raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected error for invalid credential")
	}
	if env.Error == nil || env.Error.Code != "auth_error" {
		t.Fatalf("error code=%v, want auth_error", env.Error)
	}
}

// --- quota.fetch: unsupported provider ---

func TestQuotaFetchUnsupportedProvider(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"provider": "openai",
	})
	resp, err := handleMethod("quota.fetch", raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected error for unsupported provider")
	}
	if env.Error == nil || env.Error.Code != "unsupported_provider" {
		t.Fatalf("error code=%v, want unsupported_provider", env.Error)
	}
}

// --- quota.fetch: mock upstream success ---

// mockTransport 拦截所有请求，按路径返回 mock 响应。
type mockTransport struct {
	handler http.HandlerFunc
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rr := httptest.NewRecorder()
	m.handler.ServeHTTP(rr, req)
	return &http.Response{
		StatusCode: rr.Code,
		Header:     rr.Header(),
		Body:       io.NopCloser(bytes.NewReader(rr.Body.Bytes())),
		Request:    req,
	}, nil
}

func TestQuotaFetchUpstreamSuccess(t *testing.T) {
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "",
			"data": map[string]any{
				"Response": map[string]any{
					"Data": map[string]any{
						"TotalDosage": 10000,
						"Accounts": []any{
							map[string]any{
								"PackageName":         "P1",
								"CapacitySize":        5000,
								"CapacityRemain":      3000,
								"CycleCapacitySize":   5000,
								"CycleCapacityRemain": 3000,
							},
						},
					},
				},
			},
		})
	})

	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	remain, used, size, packs, err := resourceSummary(client, "global", "test-token")
	if err != nil {
		t.Fatalf("resourceSummary error: %v", err)
	}
	if remain != 3000 {
		t.Fatalf("remain=%d, want 3000", remain)
	}
	// size 被 TotalDosage 提升到 10000
	if size != 10000 {
		t.Fatalf("size=%d, want 10000 (TotalDosage)", size)
	}
	if used != 7000 {
		t.Fatalf("used=%d, want 7000 (size-remain)", used)
	}
	if packs != 1 {
		t.Fatalf("packs=%d, want 1", packs)
	}
}

// --- quota.fetch: upstream error masked ---

func TestQuotaFetchUpstreamErrorMasked(t *testing.T) {
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":401,"msg":"unauthorized"}`))
	})

	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	_, _, _, _, err := resourceSummary(client, "global", "bad-token")
	if err == nil {
		t.Fatal("expected error from upstream 401")
	}
	// 错误消息不应包含 token
	if strings.Contains(err.Error(), "bad-token") {
		t.Fatalf("error leaks token: %v", err)
	}
}
