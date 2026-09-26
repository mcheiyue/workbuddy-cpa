package main

import (
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func assertStreamWireProfile(payload any) error {
	request, ok := payload.(map[string]any)["request"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing stream request")
	}
	profile, ok := request["WireProfile"].(*pluginapi.HTTPWireProfile)
	if !ok || profile == nil || !profile.HTTP1Only || !profile.DisableAutoCompression {
		return fmt.Errorf("stream wire profile not forced to HTTP/1.1: %#v", request["WireProfile"])
	}
	return nil
}
