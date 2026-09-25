package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
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
	// Given: methods that should return not_implemented（usage/lifecycle 未实现；quota 已在 P4 实现；model/executor 已在 P2/P3 实现）。
	notImplMethods := []string{
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
