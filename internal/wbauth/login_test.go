package wbauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartSuccess(t *testing.T) {
	// Given: mock state endpoint returns state + authUrl.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/v2/plugin/auth/state") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Code: 0,
			Data: mustJSON(map[string]string{"state": "abc", "authUrl": "https://example.com/auth"}),
		})
	}))
	defer srv.Close()

	// When: Start with test server URL.
	result, err := Start(context.Background(), srv.Client(), srv.URL, OriginCN)
	if err != nil {
		t.Fatalf("Start err: %v", err)
	}
	if result.State != "abc" {
		t.Errorf("state=%q", result.State)
	}
	if result.AuthURL != "https://example.com/auth" {
		t.Errorf("authURL=%q", result.AuthURL)
	}
}

func TestStartUpstreamError(t *testing.T) {
	// Given: upstream returns code!=0.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{Code: 500, Msg: "internal error"})
	}))
	defer srv.Close()

	// When/Then: must error.
	_, err := Start(context.Background(), srv.Client(), srv.URL, OriginCN)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPollSuccess(t *testing.T) {
	// Given: token endpoint returns tokens, account endpoint returns profile.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/v2/plugin/auth/token"):
			_ = json.NewEncoder(w).Encode(apiEnvelope{
				Code: 0,
				Data: mustJSON(map[string]any{
					"accessToken":  "at1",
					"refreshToken": "rt1",
					"expiresIn":    3600,
					"domain":       "www.workbuddy.ai",
				}),
			})
		case strings.Contains(r.URL.Path, "/v2/plugin/login/account"):
			_ = json.NewEncoder(w).Encode(apiEnvelope{
				Code: 0,
				Data: mustJSON(map[string]string{"uid": "u1", "nickname": "nick"}),
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	result, err := Poll(context.Background(), srv.Client(), srv.URL, OriginGlobal, "global", "st1")
	if err != nil {
		t.Fatalf("Poll err: %v", err)
	}
	if result.Pending {
		t.Fatal("expected not pending")
	}
	if result.Credential.AccessToken != "at1" {
		t.Errorf("accessToken=%q", result.Credential.AccessToken)
	}
	if result.Credential.UID != "u1" {
		t.Errorf("uid=%q", result.Credential.UID)
	}
	if result.Credential.Realm != "global" {
		t.Errorf("realm=%q", result.Credential.Realm)
	}
}

func TestPollPending(t *testing.T) {
	// Given: token endpoint returns non-zero code (pending state).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Code: 1,
			Msg:  "login ing",
		})
	}))
	defer srv.Close()

	result, err := Poll(context.Background(), srv.Client(), srv.URL, OriginCN, "cn", "st1")
	if err != nil {
		t.Fatalf("Poll err: %v", err)
	}
	if !result.Pending {
		t.Fatal("expected pending")
	}
}

func TestPollTokenEndpointError(t *testing.T) {
	// Given: token endpoint returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	_, err := Poll(context.Background(), srv.Client(), srv.URL, OriginCN, "cn", "st1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPollEmptyAccessTokenReturnsPending(t *testing.T) {
	// Given: token endpoint returns code=0 but empty accessToken.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Code: 0,
			Data: mustJSON(map[string]any{"expiresIn": 3600}),
		})
	}))
	defer srv.Close()

	result, err := Poll(context.Background(), srv.Client(), srv.URL, OriginCN, "cn", "st1")
	if err != nil {
		t.Fatalf("Poll err: %v", err)
	}
	if !result.Pending {
		t.Fatal("expected pending for empty accessToken")
	}
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}
