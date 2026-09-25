package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// --- tokenScrub ---

func TestTokenScrub_BearerToken(t *testing.T) {
	in := "GET https://api.example.com failed: Bearer abc123def456 was rejected"
	got := tokenScrub(in)
	if strings.Contains(got, "abc123def456") {
		t.Fatalf("Bearer token leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected [REDACTED], got: %q", got)
	}
}

func TestTokenScrub_AccessTokenField(t *testing.T) {
	cases := []string{
		`accessToken":"super_secret_value"`,
		`access_token: super_secret_value`,
		`"access_token"="super_secret_value"`,
		`access-token=super_secret_value`,
	}
	for _, in := range cases {
		got := tokenScrub(in)
		if strings.Contains(got, "super_secret_value") {
			t.Fatalf("accessToken leaked in %q → %q", in, got)
		}
	}
}

func TestTokenScrub_RefreshTokenField(t *testing.T) {
	in := `response contains refreshToken":"rt_secret123`
	got := tokenScrub(in)
	if strings.Contains(got, "rt_secret123") {
		t.Fatalf("refreshToken leaked: %q", got)
	}
}

func TestTokenScrub_DeviceTokenField(t *testing.T) {
	cases := []string{
		`"device_token":"dt_abcdef"`,
		`device_token=dt_abcdef`,
		`device_token:dt_abcdef`,
	}
	for _, in := range cases {
		got := tokenScrub(in)
		if strings.Contains(got, "dt_abcdef") {
			t.Fatalf("device_token leaked in %q → %q", in, got)
		}
	}
}

func TestTokenScrub_PlainMessageUnchanged(t *testing.T) {
	in := "upstream 502: service unavailable"
	got := tokenScrub(in)
	if got != in {
		t.Fatalf("plain message altered: %q → %q", in, got)
	}
}

// --- quotaErrorMask ---

func TestQuotaErrorMask_RedactsBearerToken(t *testing.T) {
	err := fmt.Errorf("upstream 401: Bearer secret_token_123 was rejected")
	got := quotaErrorMask(err)
	if strings.Contains(got, "secret_token_123") {
		t.Fatalf("quotaErrorMask leaks token: %q", got)
	}
}

func TestQuotaErrorMask_RedactsAccessTokenField(t *testing.T) {
	err := fmt.Errorf(`upstream 500: accessToken":"super_secret" invalid`)
	got := quotaErrorMask(err)
	if strings.Contains(got, "super_secret") {
		t.Fatalf("quotaErrorMask leaks accessToken: %q", got)
	}
}

func TestQuotaErrorMask_Truncates(t *testing.T) {
	long := "x" + strings.Repeat("y", 200)
	err := fmt.Errorf("%s", long)
	got := quotaErrorMask(err)
	if len(got) > 120 {
		t.Fatalf("quotaErrorMask should truncate to ≤120, got %d", len(got))
	}
}

// --- checkinErrorMask ---

func TestCheckinErrorMask_RedactsBearerToken(t *testing.T) {
	err := fmt.Errorf("request failed: Bearer mytoken42")
	got := checkinErrorMask(err)
	if strings.Contains(got, "mytoken42") {
		t.Fatalf("checkinErrorMask leaks token: %q", got)
	}
}

func TestCheckinErrorMask_RedactsAccessTokenField(t *testing.T) {
	err := fmt.Errorf(`error: access_token = s3cret`)
	got := checkinErrorMask(err)
	if strings.Contains(got, "s3cret") {
		t.Fatalf("checkinErrorMask leaks access_token: %q", got)
	}
}

// --- classifyExecutorError fallback path ---

func TestClassifyExecutorError_FallbackSanitizesToken(t *testing.T) {
	// Plain error (not ExecError / executorFailure / context) hits the fallback.
	err := fmt.Errorf("weird error: Bearer top_secret_token")
	f := classifyExecutorError(err)
	if f.code != "upstream_error" {
		t.Fatalf("code=%q, want upstream_error", f.code)
	}
	if f.status != 502 {
		t.Fatalf("status=%d, want 502", f.status)
	}
	if strings.Contains(f.message, "top_secret_token") {
		t.Fatalf("fallback leaks token in message: %q", f.message)
	}
}

func TestClassifyExecutorError_ExecErrorPassesThrough(t *testing.T) {
	// ExecError.Msg is already sanitized by wbexecutor — verify passthrough.
	err := &wbexecutor.ExecError{Kind: wbexecutor.ErrServer, Status: 502, Msg: "clean message"}
	f := classifyExecutorError(err)
	if f.code != "server" {
		t.Fatalf("code=%q, want server", f.code)
	}
	if f.message != "clean message" {
		t.Fatalf("message=%q, want clean message", f.message)
	}
}
