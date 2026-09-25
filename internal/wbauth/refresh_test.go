package wbauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshSuccess(t *testing.T) {
	// Given: refresh endpoint returns new tokens.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/v2/plugin/auth/token/refresh") {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Refresh-Token"); got != "oldRT" {
			t.Errorf("X-Refresh-Token=%q, want oldRT", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Code: 0,
			Data: mustJSON(map[string]any{
				"accessToken":  "newAT",
				"refreshToken": "newRT",
				"expiresIn":    3600,
			}),
		})
	}))
	defer srv.Close()

	cred := Credential{
		AccessToken: "oldAT", RefreshToken: "oldRT", ExpiresAt: 100,
		Domain: "www.codebuddy.cn", Realm: "cn", UID: "u1",
	}

	result, err := Refresh(context.Background(), srv.Client(), srv.URL, OriginCN, cred)
	if err != nil {
		t.Fatalf("Refresh err: %v", err)
	}
	if result.Credential.AccessToken != "newAT" {
		t.Errorf("accessToken=%q", result.Credential.AccessToken)
	}
	if result.Credential.RefreshToken != "newRT" {
		t.Errorf("refreshToken=%q", result.Credential.RefreshToken)
	}
	// Old credential not mutated.
	if cred.AccessToken != "oldAT" {
		t.Errorf("old credential mutated: accessToken=%q", cred.AccessToken)
	}
}

func TestRefreshNoRefreshToken(t *testing.T) {
	cred := Credential{AccessToken: "at"}
	_, err := Refresh(context.Background(), http.DefaultClient, "http://dummy", OriginCN, cred)
	if err == nil {
		t.Fatal("expected error for no refreshToken")
	}
	if !strings.Contains(err.Error(), "no refreshToken") {
		t.Errorf("error=%q", err.Error())
	}
}

func TestRefreshUpstreamErrorSanitizesTokens(t *testing.T) {
	// Given: upstream returns error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{Code: 401, Msg: "unauthorized"})
	}))
	defer srv.Close()

	cred := Credential{
		AccessToken: "at_secret_value_12345", RefreshToken: "rt_secret_value_67890",
		Domain: "www.codebuddy.cn",
	}

	_, err := Refresh(context.Background(), srv.Client(), srv.URL, OriginCN, cred)
	if err == nil {
		t.Fatal("expected error")
	}
	// Error must NOT contain the tokens.
	if strings.Contains(err.Error(), "at_secret_value_12345") {
		t.Error("error leaks accessToken")
	}
	if strings.Contains(err.Error(), "rt_secret_value_67890") {
		t.Error("error leaks refreshToken")
	}
}

func TestRefreshHTTPErrorNoTokenLeak(t *testing.T) {
	// Given: upstream returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	cred := Credential{
		AccessToken: "at_secret", RefreshToken: "rt_secret",
	}

	_, err := Refresh(context.Background(), srv.Client(), srv.URL, OriginCN, cred)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "at_secret") {
		t.Error("error leaks accessToken")
	}
}

func TestRefreshMissingAccessTokenInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Code: 0,
			Data: mustJSON(map[string]any{"expiresIn": 3600}),
		})
	}))
	defer srv.Close()

	cred := Credential{AccessToken: "at", RefreshToken: "rt"}
	_, err := Refresh(context.Background(), srv.Client(), srv.URL, OriginCN, cred)
	if err == nil {
		t.Fatal("expected error for missing accessToken")
	}
}

func TestRefreshGlobalRealm(t *testing.T) {
	// Given: global realm refresh hits workbuddy.ai path.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Code: 0,
			Data: mustJSON(map[string]any{
				"accessToken": "newAT", "expiresIn": 3600,
			}),
		})
	}))
	defer srv.Close()

	cred := Credential{
		AccessToken: "at", RefreshToken: "rt", Domain: "www.workbuddy.ai",
	}
	_, err := Refresh(context.Background(), srv.Client(), srv.URL, OriginGlobal, cred)
	if err != nil {
		t.Fatalf("Refresh err: %v", err)
	}
	if gotPath != "/v2/plugin/auth/token/refresh" {
		t.Errorf("path=%q", gotPath)
	}
}

func TestSanitizeErrorStripsTokens(t *testing.T) {
	cred := Credential{
		AccessToken:  "super_secret_access_token_value",
		RefreshToken: "super_secret_refresh_token_value",
		DeviceToken:  "super_secret_device_token_value",
	}
	err := sanitizeError(fmt.Errorf("error with super_secret_access_token_value and super_secret_refresh_token_value"), cred)
	msg := err.Error()
	if strings.Contains(msg, "super_secret_access_token_value") {
		t.Errorf("error leaks accessToken: %q", msg)
	}
	if strings.Contains(msg, "super_secret_refresh_token_value") {
		t.Errorf("error leaks refreshToken: %q", msg)
	}
	if strings.Contains(msg, "super_secret_device_token_value") {
		t.Errorf("error leaks deviceToken: %q", msg)
	}
}

func TestRefreshPreservesOldCredentialOnFailure(t *testing.T) {
	// Given: upstream always errors.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	orig := Credential{
		AccessToken: "oldAT", RefreshToken: "oldRT", ExpiresAt: 100,
		Domain: "www.codebuddy.cn",
	}

	_, err := Refresh(context.Background(), srv.Client(), srv.URL, OriginCN, orig)
	if err == nil {
		t.Fatal("expected error")
	}
	// Original credential unchanged.
	if orig.AccessToken != "oldAT" || orig.RefreshToken != "oldRT" || orig.ExpiresAt != 100 {
		t.Errorf("credential mutated on failure: %+v", orig)
	}
}

func TestRefreshMutexConcurrency(t *testing.T) {
	m := NewRefreshMutex()
	done := make(chan struct{})
	go func() {
		m.Lock("key1")
		m.Unlock("key1")
		close(done)
	}()
	<-done
}
