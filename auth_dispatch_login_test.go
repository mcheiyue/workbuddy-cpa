package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// startLoginMockHost 拦截宿主 HTTP 回调，记录真实请求 URL，并返回固定的 auth state 响应。
func startLoginMockHost(t *testing.T, gotURL *string) {
	t.Helper()
	orig := hostJSONCall
	t.Cleanup(func() { hostJSONCall = orig })
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostHTTPDo {
			t.Fatalf("unexpected host method: %s", method)
		}
		req, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var call struct {
			Request struct {
				URL string `json:"URL"`
			} `json:"request"`
		}
		if err := json.Unmarshal(req, &call); err != nil {
			t.Fatalf("decode call payload: %v", err)
		}
		*gotURL = call.Request.URL
		resp := hostHTTPResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": {"application/json"}},
			Body:       []byte(`{"code":0,"msg":"OK","data":{"state":"st-1","authUrl":"https://example.com/auth"}}`),
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		return raw, nil
	}
}

func startLoginRaw(t *testing.T, metadata map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(rpcAuthLoginStartRequest{
		AuthLoginStartRequest: pluginapi.AuthLoginStartRequest{
			Metadata: metadata,
		},
		HostCallbackID: "cb-test",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return raw
}

// realm=global：state 必须打 workbuddy.ai，且响应回填 realm 供 Poll 使用。
func TestHandleAuthLoginStart_GlobalRealm(t *testing.T) {
	var gotURL string
	startLoginMockHost(t, &gotURL)
	resp, err := handleAuthLoginStart(context.Background(), startLoginRaw(t, map[string]any{"realm": "global"}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(gotURL, "https://www.workbuddy.ai/v2/plugin/auth/state") {
		t.Fatalf("state URL=%q, want workbuddy.ai base", gotURL)
	}
	if resp.URL == "" || resp.State == "" {
		t.Fatalf("resp missing url/state: %+v", resp)
	}
	if got, _ := resp.Metadata["realm"].(string); got != "global" {
		t.Fatalf("Metadata.realm=%q, want global (poll must reuse it)", got)
	}
}

// 无 metadata：落默认 CN（copilot 域），回填 cn。
func TestHandleAuthLoginStart_DefaultCNRealm(t *testing.T) {
	var gotURL string
	startLoginMockHost(t, &gotURL)
	resp, err := handleAuthLoginStart(context.Background(), startLoginRaw(t, nil))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(gotURL, "https://www.workbuddy.cn/v2/plugin/auth/state") {
		t.Fatalf("state URL=%q, want workbuddy.cn CN base", gotURL)
	}
	if got, _ := resp.Metadata["realm"].(string); got != "cn" {
		t.Fatalf("Metadata.realm=%q, want cn", got)
	}
}
