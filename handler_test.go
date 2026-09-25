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
	// Given: methods that should return not_implemented in P0.
	notImplMethods := []string{
		pluginabi.MethodAuthIdentifier,
		pluginabi.MethodAuthParse,
		pluginabi.MethodAuthLoginStart,
		pluginabi.MethodAuthLoginPoll,
		pluginabi.MethodAuthRefresh,
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
