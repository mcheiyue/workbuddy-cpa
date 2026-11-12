package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// 上游 429/403 携带的重试提示必须结构化传给宿主（pluginabi.Error.RetryAfterSeconds，
// 宿主 rpc_client.go 映射为 Retry-After 头与冷却提示），而不是只留在错误文案里。
func TestClassifyExecutorErrorCarriesRetryAfter(t *testing.T) {
	f := classifyExecutorError(&wbexecutor.ExecError{
		Kind:          wbexecutor.ErrSoftRate,
		Status:        429,
		Msg:           "rate limited",
		RetryAfterSec: 120,
	})
	if f == nil {
		t.Fatal("nil failure")
	}
	if f.retryAfter != 120 {
		t.Fatalf("retryAfter = %d, want 120", f.retryAfter)
	}
}

func TestClassifyExecutorErrorZeroRetryAfterStaysZero(t *testing.T) {
	f := classifyExecutorError(&wbexecutor.ExecError{
		Kind:   wbexecutor.ErrClient,
		Status: 400,
		Msg:    "bad request",
	})
	if f == nil {
		t.Fatal("nil failure")
	}
	if f.retryAfter != 0 {
		t.Fatalf("retryAfter = %d, want 0", f.retryAfter)
	}
}

func TestClassifyExecutorErrorMapsGlobalQueueToRequestScopedEnvelope(t *testing.T) {
	failure := classifyExecutorError(&wbexecutor.ExecError{
		Kind:          wbexecutor.ErrGlobalQueue,
		Status:        429,
		Msg:           "queue.waiting",
		RetryAfterSec: 120,
	})
	if failure.code != pluginabi.ErrorCodeRequestScoped {
		t.Fatalf("code = %q, want %q", failure.code, pluginabi.ErrorCodeRequestScoped)
	}

	var envelope pluginabi.Envelope
	if err := json.Unmarshal(errorEnvelopeRetry(failure.code, failure.message, failure.status, failure.retryAfter), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.OK || envelope.Error == nil {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if !envelope.Error.IsRequestScoped() {
		t.Fatalf("error = %+v, want request-scoped", envelope.Error)
	}
	if envelope.Error.HTTPStatus != 429 {
		t.Errorf("http_status = %d, want 429", envelope.Error.HTTPStatus)
	}
	if envelope.Error.RetryAfterSeconds != 120 {
		t.Errorf("retry_after_seconds = %d, want 120", envelope.Error.RetryAfterSeconds)
	}
}

func TestErrorEnvelopeRetryCarriesRetryAfterSeconds(t *testing.T) {
	raw := errorEnvelopeRetry("soft_rate", "rate limited", 429, 120)
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code              string `json:"code"`
			Message           string `json:"message"`
			HTTPStatus        int    `json:"http_status,omitempty"`
			RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.OK {
		t.Fatal("envelope must not be ok")
	}
	if envelope.Error.RetryAfterSeconds != 120 {
		t.Fatalf("retry_after_seconds = %d, want 120 (raw=%s)", envelope.Error.RetryAfterSeconds, string(raw))
	}
	if !strings.Contains(string(raw), `"retry_after_seconds":120`) {
		t.Fatalf("wire missing retry_after_seconds: %s", string(raw))
	}
}
