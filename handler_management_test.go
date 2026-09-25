package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

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
		if route.Method != http.MethodGet {
			t.Fatalf("route method=%q, want GET", route.Method)
		}
		if len(route.Path) == 0 {
			t.Fatal("route path is empty")
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
