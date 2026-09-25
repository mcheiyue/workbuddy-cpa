package main

import (
	"encoding/json"
	"testing"
)

func TestRegistrationSchema6WorkBuddyCapabilities(t *testing.T) {
	// Given: registration must roundtrip through JSON like a real ABI call.
	var reg struct {
		SchemaVersion uint32 `json:"schema_version"`
		Metadata      struct {
			Name    string `json:"Name"`
			Version string `json:"Version"`
			Author  string `json:"Author"`
			Repo    string `json:"GitHubRepository"`
		} `json:"metadata"`
		Capabilities struct {
			AuthProvider           bool     `json:"auth_provider"`
			ModelProvider          bool     `json:"model_provider"`
			Executor               bool     `json:"executor"`
			ManagementAPI          bool     `json:"management_api"`
			QuotaProvider          bool     `json:"quota_provider"`
			UsagePlugin            bool     `json:"usage_plugin"`
			RequestLifecyclePlugin bool     `json:"request_lifecycle_plugin"`
			ExecutorModelScope     string   `json:"executor_model_scope"`
			ExecutorInputFormats   []string `json:"executor_input_formats"`
			ExecutorOutputFormats  []string `json:"executor_output_formats"`
			ModelRouter            *bool    `json:"model_router,omitempty"`
			RequestInterceptor     *bool    `json:"request_interceptor,omitempty"`
			Scheduler              *bool    `json:"scheduler,omitempty"`
		} `json:"capabilities"`
	}

	// When: serialize and parse the registration result.
	raw, err := json.Marshal(registration())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}

	// Then: schema = 6, plugin = workbuddy, capabilities correct.
	if reg.SchemaVersion != 6 {
		t.Fatalf("schema=%d, want 6", reg.SchemaVersion)
	}
	if reg.Metadata.Name != "workbuddy" {
		t.Fatalf("Name=%q, want %q", reg.Metadata.Name, "workbuddy")
	}
	if reg.Metadata.Version != "0.1.4" {
		t.Fatalf("Version=%q, want %q", reg.Metadata.Version, "0.1.4")
	}
	if reg.Metadata.Author != "mcheiyue" {
		t.Fatalf("Author=%q, want %q", reg.Metadata.Author, "mcheiyue")
	}
	if reg.Metadata.Repo != "https://github.com/mcheiyue/workbuddy-cpa" {
		t.Fatalf("Repo=%q, want %q", reg.Metadata.Repo, "https://github.com/mcheiyue/workbuddy-cpa")
	}

	// 能力集断言
	for _, tc := range []struct {
		name string
		got  bool
	}{
		{"auth_provider", reg.Capabilities.AuthProvider},
		{"model_provider", reg.Capabilities.ModelProvider},
		{"executor", reg.Capabilities.Executor},
		{"management_api", reg.Capabilities.ManagementAPI},
		{"quota_provider", reg.Capabilities.QuotaProvider},
		{"usage_plugin", reg.Capabilities.UsagePlugin},
		{"request_lifecycle_plugin", reg.Capabilities.RequestLifecyclePlugin},
	} {
		if !tc.got {
			t.Fatalf("%s should be true", tc.name)
		}
	}
	if reg.Capabilities.ExecutorModelScope != "oauth" {
		t.Fatalf("executor_model_scope=%q, want %q", reg.Capabilities.ExecutorModelScope, "oauth")
	}
	if len(reg.Capabilities.ExecutorInputFormats) != 1 || reg.Capabilities.ExecutorInputFormats[0] != "chat-completions" {
		t.Fatalf("executor_input_formats=%v, want [chat-completions]", reg.Capabilities.ExecutorInputFormats)
	}
	if len(reg.Capabilities.ExecutorOutputFormats) != 1 || reg.Capabilities.ExecutorOutputFormats[0] != "chat-completions" {
		t.Fatalf("executor_output_formats=%v, want [chat-completions]", reg.Capabilities.ExecutorOutputFormats)
	}
	if reg.Capabilities.Scheduler == nil || !*reg.Capabilities.Scheduler {
		t.Fatal("scheduler should be true")
	}
}

func TestRegistrationDoesNotDeclareUnwantedCapabilities(t *testing.T) {
	// Given: the registration map.
	reg := registration()

	// When: serialize and inspect capabilities keys.
	raw, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	capsJSON, ok := m["capabilities"]
	if !ok {
		t.Fatal("missing capabilities key")
	}
	var caps map[string]json.RawMessage
	if err := json.Unmarshal(capsJSON, &caps); err != nil {
		t.Fatal(err)
	}

	// Then: must not declare model_router, request_interceptor.
	for _, forbidden := range []string{"model_router", "request_interceptor"} {
		if _, exists := caps[forbidden]; exists {
			t.Fatalf("capabilities must not contain %q", forbidden)
		}
	}
	// Then: must declare scheduler.
	if _, exists := caps["scheduler"]; !exists {
		t.Fatal("capabilities must contain scheduler")
	}
}
