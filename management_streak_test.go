package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// TestStreakHandler_DirectNotHostBridged 控制面 streak 直连：
// 返回连登天数与三档领奖状态，且响应体不得含 token（管理面脱敏铁律 #1819）。
func TestStreakHandler_DirectNotHostBridged(t *testing.T) {
	var violations []string
	files, _ := json.Marshal(map[string]any{
		"files": []any{map[string]any{"auth_index": "cn1", "provider": "workbuddy", "name": "cn1"}},
	})
	svc := &managementService{
		hostCall: controlPlaneHostCall(&violations, files, controlTestCredential()),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				// growth streak 形状（ops_claim.go growthStreakResp）：doGrowthJSON 解 envelope 取 data。
				w.Write([]byte(`{"code":0,"msg":"","data":{"streak":{"days":7},"redemption_status":{"tier_7d_status":"claimed","tier_14d_status":"available","tier_28d_status":"locked"}}}`))
			}}}, nil
		},
	}
	resp, err := svc.streakHandler([]byte(`{"auth_index":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var payload struct {
		Streaks []struct {
			AuthIndex string `json:"auth_index"`
			Days      int    `json:"days"`
			Tier7d    string `json:"tier_7d"`
			Tier14d   string `json:"tier_14d"`
			Tier28d   string `json:"tier_28d"`
			Error     string `json:"error"`
		} `json:"streaks"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Streaks) != 1 {
		t.Fatalf("streaks=%d, want 1: %s", len(payload.Streaks), resp.Body)
	}
	s := payload.Streaks[0]
	if s.AuthIndex != "cn1" || s.Days != 7 || s.Tier14d != "available" || s.Error != "" {
		t.Fatalf("streak payload=%s (want cn1 days=7 tier_14d=available, no error)", resp.Body)
	}
	body := string(resp.Body)
	if strings.Contains(body, "cp-token") || strings.Contains(body, "cp-refresh") {
		t.Fatalf("streak response leaks token: %s", body)
	}
	if len(violations) != 0 {
		t.Fatalf("control plane used host HTTP bridge: %v", violations)
	}
}

// TestStreakRouteRegistered 路由表含 POST /workbuddy/streak。
func TestStreakRouteRegistered(t *testing.T) {
	raw, err := handleMethod("management.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		t.Fatalf("envelope=%s", raw)
	}
	var result struct {
		Routes []struct{ Method, Path string } `json:"routes"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range result.Routes {
		if r.Method == http.MethodPost && r.Path == "/workbuddy/streak" {
			found = true
		}
	}
	if !found {
		t.Fatalf("POST /workbuddy/streak not registered: %+v", result.Routes)
	}
	_ = pluginapi.ManagementRequest{} // keep import stable when dispatch changes
}
