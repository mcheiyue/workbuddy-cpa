package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func assertStreamWireProfile(payload any) error {
	request, ok := payload.(map[string]any)["request"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing stream request")
	}
	profile, ok := request["wire_profile"].(*pluginapi.HTTPWireProfile)
	if !ok || profile == nil || !profile.HTTP1Only || !profile.DisableAutoCompression {
		return fmt.Errorf("stream wire profile not forced to HTTP/1.1: %#v", request["wire_profile"])
	}
	return nil
}

func TestStreamWireProfileUsesSDKJSONName(t *testing.T) {
	payload := map[string]any{
		"request": map[string]any{
			"wire_profile": &pluginapi.HTTPWireProfile{HTTP1Only: true, DisableAutoCompression: true},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	request, ok := decoded["request"].(map[string]any)
	if !ok {
		t.Fatal("request missing after JSON round-trip")
	}
	if _, ok := request["wire_profile"]; !ok {
		t.Fatalf("wire_profile missing from JSON: %s", raw)
	}
}
