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

// F3 回归：宿主 Envelope 错误必须透传真实 message，不再吞成固定串。
func TestUnwrapHostEnvelopePassesThroughErrorMessage(t *testing.T) {
	body := []byte(`{"ok":false,"error":{"code":"invalid_argument","message":"host callback ID is not open"}}`)
	_, err := unwrapHostEnvelope(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err.Error(), "host callback ID is not open"; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}

// 错误无 message 时回退固定文案，调用方仍能感知失败。
func TestUnwrapHostEnvelopeFallbackWhenMessageEmpty(t *testing.T) {
	body := []byte(`{"ok":false,"error":{"code":"internal"}}`)
	_, err := unwrapHostEnvelope(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err.Error(), "host callback failed"; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}

// 无 error 字段同样回退固定文案。
func TestUnwrapHostEnvelopeFallbackWhenErrorAbsent(t *testing.T) {
	body := []byte(`{"ok":false}`)
	_, err := unwrapHostEnvelope(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := err.Error(), "host callback failed"; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
}

// 成功信封返回 Result。
func TestUnwrapHostEnvelopeSuccessReturnsResult(t *testing.T) {
	result, err := unwrapHostEnvelope([]byte(`{"ok":true,"result":{"a":1}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := string(result), `{"a":1}`; got != want {
		t.Fatalf("result = %s, want %s", got, want)
	}
}

// body 非 Envelope 形状时原样返回，不报错（与旧行为一致）。
func TestUnwrapHostEnvelopeNonEnvelopeBodyPassthrough(t *testing.T) {
	body := []byte(`plain-text`)
	result, err := unwrapHostEnvelope(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := string(result), "plain-text"; got != want {
		t.Fatalf("result = %q, want %q", got, want)
	}
}
