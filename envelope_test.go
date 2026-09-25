package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestOkEnvelopeRoundTrip(t *testing.T) {
	// Given: a result value.
	result := map[string]string{"key": "value"}

	// When: encode with okEnvelope.
	raw, err := okEnvelope(result)
	if err != nil {
		t.Fatal(err)
	}

	// Then: decode and verify.
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	var decoded map[string]string
	if err := json.Unmarshal(env.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["key"] != "value" {
		t.Fatalf("decoded=%v, want key=value", decoded)
	}
}

func TestErrorEnvelopeFormat(t *testing.T) {
	// Given: error code and message.
	// When: encode with errorEnvelope.
	raw := errorEnvelope("test_code", "test message")

	// Then: decode and verify structure.
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
	if env.Error == nil {
		t.Fatal("expected error")
	}
	if env.Error.Code != "test_code" {
		t.Fatalf("code=%q, want %q", env.Error.Code, "test_code")
	}
	if env.Error.Message != "test message" {
		t.Fatalf("message=%q, want %q", env.Error.Message, "test message")
	}
}

func TestErrorEnvelopeStatusIncludesHTTPStatus(t *testing.T) {
	// Given: code, message and HTTP status.
	// When: encode with errorEnvelopeStatus.
	raw := errorEnvelopeStatus("not_found", "route missing", 404)

	// Then: HTTP status is present.
	var env struct {
		Error *struct {
			Code       string `json:"code"`
			HTTPStatus int    `json:"http_status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.HTTPStatus != 404 {
		t.Fatalf("http_status=%d, want 404", env.Error.HTTPStatus)
	}
}

func TestErrorEnvelopeZeroStatusOmitted(t *testing.T) {
	// Given: errorEnvelope (status=0).
	// When: encode.
	raw := errorEnvelope("code", "msg")

	// Then: http_status should be omitted (0).
	var env struct {
		Error *struct {
			HTTPStatus int `json:"http_status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.HTTPStatus != 0 {
		t.Fatalf("http_status=%d, want 0 (omitted)", env.Error.HTTPStatus)
	}
}
