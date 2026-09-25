package main

import (
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAuthIdentifierReturnsWorkBuddy(t *testing.T) {
	// Given: auth.identifier method.
	raw, err := handleMethod(pluginabi.MethodAuthIdentifier, nil)
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
	if id.Identifier != wbauth.Provider {
		t.Fatalf("identifier=%q, want %q", id.Identifier, wbauth.Provider)
	}
}

func TestAuthParseValidNestedCredential(t *testing.T) {
	// Given: valid nested WorkBuddy credential JSON.
	req := pluginapi.AuthParseRequest{
		Provider: wbauth.Provider,
		RawJSON:  []byte(`{"auth":{"accessToken":"at123","refreshToken":"rt456","expiresAt":999,"domain":"www.workbuddy.ai","realm":"global"},"account":{"uid":"u1","nickname":"nick"}}`),
	}
	raw, _ := json.Marshal(req)

	// When: handleMethod processes it.
	envelope, err := handleMethod(pluginabi.MethodAuthParse, raw)
	if err != nil {
		t.Fatal(err)
	}
	var okEnv struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(envelope, &okEnv); err != nil {
		t.Fatal(err)
	}
	if !okEnv.OK {
		t.Fatalf("expected ok=true, envelope=%s", envelope)
	}
	var resp struct {
		Handled bool `json:"Handled"`
		Auth    struct {
			ID       string `json:"ID"`
			Label    string `json:"Label"`
			Provider string `json:"Provider"`
		} `json:"Auth"`
	}
	if err := json.Unmarshal(okEnv.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Handled {
		t.Fatal("expected Handled=true")
	}
	if resp.Auth.Provider != wbauth.Provider {
		t.Errorf("provider=%q, want %q", resp.Auth.Provider, wbauth.Provider)
	}
	if resp.Auth.ID != "workbuddy-u1" {
		t.Errorf("id=%q, want workbuddy-u1", resp.Auth.ID)
	}
}

func TestAuthParseRejectsOtherProvider(t *testing.T) {
	// Given: parse request for a different provider.
	req := pluginapi.AuthParseRequest{
		Provider: "qoder",
		RawJSON:  []byte(`{"accessToken":"at"}`),
	}
	raw, _ := json.Marshal(req)

	// When: handleMethod processes it.
	envelope, err := handleMethod(pluginabi.MethodAuthParse, raw)
	if err != nil {
		t.Fatal(err)
	}
	var okEnv struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(envelope, &okEnv); err != nil {
		t.Fatal(err)
	}
	if !okEnv.OK {
		t.Fatal("expected ok=true for non-matching provider")
	}
	var resp struct {
		Handled bool `json:"Handled"`
	}
	if err := json.Unmarshal(okEnv.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Handled {
		t.Fatal("expected Handled=false for qoder provider")
	}
}

func TestAuthParseInvalidJSONReturnsError(t *testing.T) {
	// Given: malformed JSON.
	raw := []byte(`{invalid`)

	// When: handleMethod processes it.
	envelope, err := handleMethod(pluginabi.MethodAuthParse, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected ok=false for invalid JSON")
	}
	if env.Error == nil || env.Error.Code != "auth_error" {
		t.Fatalf("expected auth_error, got: %v", env.Error)
	}
}

func TestAuthRefreshNoHostReturnsError(t *testing.T) {
	// Given: refresh request without host callback.
	req := rpcAuthRefreshRequest{
		AuthRefreshRequest: pluginapi.AuthRefreshRequest{
			StorageJSON: []byte(`{"auth":{"accessToken":"at","refreshToken":"rt","expiresAt":100},"account":{"uid":"u1"}}`),
		},
		HostCallbackID: "",
	}
	raw, _ := json.Marshal(req)

	// When: handleMethod processes it.
	envelope, err := handleMethod(pluginabi.MethodAuthRefresh, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected ok=false when host not connected")
	}
	if env.Error == nil || env.Error.Code != "auth_error" {
		t.Fatalf("expected auth_error, got: %v", env.Error)
	}
}
