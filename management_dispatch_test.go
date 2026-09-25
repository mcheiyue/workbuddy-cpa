package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// decodeLikeHost 模拟宿主 rpc_client.callPlugin 的解包路径：
// Envelope → envelope.Result → json.Unmarshal 到 ManagementResponse
// （Body 是 []byte，JSON 里必须是 base64 字符串，否则正是现网 502 的 illegal base64 错误）。
func decodeLikeHost(t *testing.T, rawRequest pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	t.Helper()
	raw, err := json.Marshal(rawRequest)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	out, err := managementHandle(raw)
	if err != nil {
		t.Fatalf("managementHandle: %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("envelope unmarshal: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope not ok: %s", out)
	}
	var resp pluginapi.ManagementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("host-side result decode failed (this is the production 502): %v", err)
	}
	return resp
}

func TestManagementHandle_IndexHTML_BodyRoundTrip(t *testing.T) {
	resp := decodeLikeHost(t, pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/workbuddy/index.html",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if got := resp.Headers.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if !bytes.Equal(resp.Body, workbuddyWebUI) {
		t.Fatalf("body round-trip mismatch: got %d bytes, want %d", len(resp.Body), len(workbuddyWebUI))
	}
}

func TestManagementHandle_AccountsJSON_BodyRoundTrip(t *testing.T) {
	orig := hostJSONCall
	defer func() { hostJSONCall = orig }()
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthList:
			return json.RawMessage(`{"files":[]}`), nil
		default:
			return nil, errors.New("unexpected host method: " + method)
		}
	}
	resp := decodeLikeHost(t, pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/workbuddy/accounts",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("body is not decodable JSON after host decode: %v", err)
	}
	if _, ok := payload["accounts"]; !ok {
		t.Fatalf("payload missing accounts key: %s", resp.Body)
	}
}
