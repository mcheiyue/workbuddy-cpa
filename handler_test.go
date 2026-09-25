package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestHandleMethodUnknownReturnsTypedError(t *testing.T) {
	// Given: an unknown method name.
	method := "nonexistent.method"

	// When: handleMethod dispatches it.
	raw, err := handleMethod(method, nil)
	if err != nil {
		t.Fatalf("handleMethod returned error: %v", err)
	}

	// Then: error envelope with code "unknown_method".
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
		t.Fatal("expected ok=false for unknown method")
	}
	if env.Error == nil {
		t.Fatal("expected error in envelope")
	}
	if env.Error.Code != "unknown_method" {
		t.Fatalf("code=%q, want %q", env.Error.Code, "unknown_method")
	}
	if !strings.Contains(env.Error.Message, method) {
		t.Fatalf("message=%q should contain %q", env.Error.Message, method)
	}
}

func TestHandleMethodEmptyMethod(t *testing.T) {
	// Given: an empty string method.
	// When: handleMethod dispatches it.
	// Then: must return unknown_method error, not panic.
	raw, err := handleMethod("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
		t.Fatal("expected ok=false for empty method")
	}
	if env.Error == nil || env.Error.Code != "unknown_method" {
		t.Fatalf("expected unknown_method error, got: %v", env.Error)
	}
}

func TestHandleMethodRegisterReturnsSchema6(t *testing.T) {
	// Given: the plugin.register method.
	// When: handleMethod processes it.
	raw, err := handleMethod("plugin.register", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Then: result must contain schema_version 6 and plugin name.
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatal("expected ok=true for plugin.register")
	}
	var reg struct {
		SchemaVersion uint32 `json:"schema_version"`
		Metadata      struct {
			Name string `json:"Name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.SchemaVersion != 6 {
		t.Fatalf("schema=%d, want 6", reg.SchemaVersion)
	}
	if reg.Metadata.Name != "workbuddy" {
		t.Fatalf("Name=%q, want %q", reg.Metadata.Name, "workbuddy")
	}
}

func TestHandleMethodReconfigureReturnsRegistration(t *testing.T) {
	// Given: plugin.reconfigure method.
	// When: handleMethod processes it.
	raw, err := handleMethod(pluginabi.MethodPluginReconfigure, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Then: returns same registration.
	var env struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatal("expected ok=true for plugin.reconfigure")
	}
}

func TestHandleMethodNotImplementedReturnsCorrectEnvelope(t *testing.T) {
	// Given: methods that should return not_implemented (auth 已实现，不再占位).
	notImplMethods := []string{
		pluginabi.MethodModelStatic,
		pluginabi.MethodModelForAuth,
		pluginabi.MethodExecutorIdentifier,
		pluginabi.MethodExecutorExecute,
		pluginabi.MethodExecutorExecuteStream,
		pluginabi.MethodExecutorCountTokens,
		pluginabi.MethodExecutorHTTPRequest,
		pluginabi.MethodQuotaIdentifier,
		pluginabi.MethodQuotaDescribe,
		pluginabi.MethodQuotaFetch,
		pluginabi.MethodQuotaReset,
		pluginabi.MethodUsageHandle,
		pluginabi.MethodRequestComplete,
		pluginabi.MethodPluginQuiesce,
		pluginabi.MethodPluginShutdown,
	}

	for _, method := range notImplMethods {
		// When: handleMethod processes it.
		raw, err := handleMethod(method, nil)
		if err != nil {
			t.Fatalf("handleMethod(%q) returned error: %v", method, err)
		}

		// Then: error envelope with code "not_implemented".
		var env struct {
			OK    bool `json:"ok"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("method %q: %v", method, err)
		}
		if env.OK {
			t.Fatalf("method %q: expected ok=false", method)
		}
		if env.Error == nil {
			t.Fatalf("method %q: expected error", method)
		}
		if env.Error.Code != "not_implemented" {
			t.Fatalf("method %q: code=%q, want %q", method, env.Error.Code, "not_implemented")
		}
		if !strings.Contains(env.Error.Message, method) {
			t.Fatalf("method %q: message=%q should contain method name", method, env.Error.Message)
		}
	}
}

func TestManagementRegisterReturnsRoutesAndResources(t *testing.T) {
	// Given: the management.register method.
	raw, err := handleMethod(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("envelope=%s", raw)
	}
	var result struct {
		Routes    []struct{ Method, Path string } `json:"routes"`
		Resources []struct{ Path, Menu string }   `json:"resources"`
	}
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatal(err)
	}
	// 验证路由表：3 条 API 路由 + 1 条资源路由
	if len(result.Routes) != 3 {
		t.Fatalf("routes count=%d, want 3", len(result.Routes))
	}
	for _, route := range result.Routes {
		if !strings.HasPrefix(route.Path, "/workbuddy") {
			t.Fatalf("route path=%q, want /workbuddy/* prefix", route.Path)
		}
	}
	if len(result.Resources) != 1 || result.Resources[0].Path != "/index.html" {
		t.Fatalf("resources=%v, want [/index.html]", result.Resources)
	}
}

func TestManagementHandleUnknownRouteReturns404(t *testing.T) {
	// Given: management.handle with unknown path.
	rawRequest, err := json.Marshal(pluginapi.ManagementRequest{
		Method: "GET",
		Path:   "/workbuddy/unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	rawResponse, err := handleMethod(pluginabi.MethodManagementHandle, rawRequest)
	if err != nil {
		t.Fatal(err)
	}

	// Then: 404 envelope.
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code       string `json:"code"`
			HTTPStatus int    `json:"http_status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected ok=false for unknown management route")
	}
	if env.Error == nil || env.Error.Code != "not_found" {
		t.Fatalf("expected not_found error, got: %v", env.Error)
	}
	if env.Error.HTTPStatus != 404 {
		t.Fatalf("http_status=%d, want 404", env.Error.HTTPStatus)
	}
}

func TestManagementHandleInvalidJSONReturns400(t *testing.T) {
	// Given: malformed JSON body.
	// When: management.handle processes it.
	rawResponse, err := handleMethod(pluginabi.MethodManagementHandle, []byte("{invalid"))
	if err != nil {
		t.Fatal(err)
	}

	// Then: 400 envelope.
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code       string `json:"code"`
			HTTPStatus int    `json:"http_status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected ok=false for invalid JSON")
	}
	if env.Error == nil || env.Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request error, got: %v", env.Error)
	}
	if env.Error.HTTPStatus != 400 {
		t.Fatalf("http_status=%d, want 400", env.Error.HTTPStatus)
	}
}

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
	if id.Identifier != "workbuddy" {
		t.Fatalf("identifier=%q, want workbuddy", id.Identifier)
	}
}

func TestAuthParseValidNestedCredential(t *testing.T) {
	// Given: valid nested WorkBuddy credential JSON.
	req := pluginapi.AuthParseRequest{
		Provider: "workbuddy",
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
	if resp.Auth.Provider != "workbuddy" {
		t.Errorf("provider=%q", resp.Auth.Provider)
	}
	if resp.Auth.ID != "workbuddy-u1" {
		t.Errorf("id=%q", resp.Auth.ID)
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
