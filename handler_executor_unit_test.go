package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// TestExecutorIdentifierReturnsWorkBuddy 验证 executor.identifier 返回 "workbuddy"。
func TestExecutorIdentifierReturnsWorkBuddy(t *testing.T) {
	raw, err := handleMethod(pluginabi.MethodExecutorIdentifier, nil)
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
		t.Fatal("expected ok=true")
	}
	var id struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(env.Result, &id); err != nil {
		t.Fatal(err)
	}
	if id.Identifier != "workbuddy" {
		t.Fatalf("identifier=%q, want workbuddy", id.Identifier)
	}
}

// TestExecutorHTTPRequestReturnsNotImplemented 验证 executor.http_request 仍返回 not_implemented。
func TestExecutorHTTPRequestReturnsNotImplemented(t *testing.T) {
	raw, err := handleMethod(pluginabi.MethodExecutorHTTPRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
	if env.Error == nil || env.Error.Code != "not_implemented" {
		t.Fatalf("expected not_implemented, got: %v", env.Error)
	}
}

// TestExecutorCountTokensLocalEstimate 验证 executor.count_tokens 本地估算。
func TestExecutorCountTokensLocalEstimate(t *testing.T) {
	storageJSON := []byte(`{"auth":{"accessToken":"at","refreshToken":"rt","expiresAt":9999999999,"domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u1"}}`)
	payload := []byte(`{"model":"m","messages":[{"role":"user","content":"hello world"}]}`)
	rawReq, _ := json.Marshal(executorRequestHelper("workbuddy", "auth-u1", "m", storageJSON, payload, ""))

	raw, err := handleMethod(pluginabi.MethodExecutorCountTokens, rawReq)
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
		t.Fatalf("expected ok=true, envelope: %s", raw)
	}
	var resp struct {
		Payload []byte `json:"Payload"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	var tokens struct {
		TotalTokens int  `json:"total_tokens"`
		InputTokens int  `json:"input_tokens"`
		Estimated   bool `json:"estimated"`
	}
	if err := json.Unmarshal(resp.Payload, &tokens); err != nil {
		t.Fatal(err)
	}
	if !tokens.Estimated {
		t.Fatal("expected estimated=true")
	}
	// len(payload)/4 ≈ 70/4 = 17
	expected := len(payload) / 4
	if tokens.TotalTokens != expected {
		t.Fatalf("total_tokens=%d, want %d", tokens.TotalTokens, expected)
	}
	if tokens.InputTokens != expected {
		t.Fatalf("input_tokens=%d, want %d", tokens.InputTokens, expected)
	}
}
