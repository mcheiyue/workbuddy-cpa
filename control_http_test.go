package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// controlPlaneHostCall 构造管理面 hostCall mock：
// 只放行 auth.list / auth.get；若收到 MethodHostHTTPDo（宿主 HTTP 桥）
// 即记录违规——控制面必须直连，后台无宿主分发回调，走桥必被拒。
func controlPlaneHostCall(violations *[]string, filesJSON, credJSON []byte) func(string, any) (json.RawMessage, error) {
	return func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPDo:
			*violations = append(*violations, method)
			return nil, errHostCallbackNotOpen
		case pluginabi.MethodHostAuthList:
			return filesJSON, nil
		case pluginabi.MethodHostAuthGet:
			return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "cn1", JSON: credJSON})
		default:
			return nil, errUnexpectedMethod
		}
	}
}

var (
	errHostCallbackNotOpen = &hostCallTestError{"host callback ID is not open"}
	errUnexpectedMethod    = &hostCallTestError{"unexpected method"}
)

type hostCallTestError struct{ msg string }

func (e *hostCallTestError) Error() string { return e.msg }

func controlTestCredential() []byte {
	raw, _ := json.Marshal(map[string]any{
		"auth": map[string]any{
			"accessToken":  "cp-token",
			"refreshToken": "cp-refresh",
			"expiresAt":    time.Now().Add(time.Hour).Unix(),
			"domain":       "www.workbuddy.cn",
			"realm":        "cn",
		},
		"account": map[string]any{"uid": "uid-control-1", "nickname": "ctl"},
	})
	return raw
}

// TestQuotaRefreshHandler_DirectNotHostBridged 控制面 quota 必须直连：
// 宿主桥调用（MethodHostHTTPDo）是违规；真实配额经注入的直连 client 返回。
func TestQuotaRefreshHandler_DirectNotHostBridged(t *testing.T) {
	var violations []string
	svc := &managementService{
		hostCall: controlPlaneHostCall(&violations, nil, controlTestCredential()),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"","data":{"Response":{"Data":{"TotalDosage":10000,"Accounts":[{"PackageName":"P1","CapacitySize":5000,"CapacityRemain":3000,"CycleCapacitySize":5000,"CycleCapacityRemain":3000}]}}}}`))
			}}}, nil
		},
	}
	resp, err := svc.quotaRefreshHandler([]byte(`{"auth_index":"cn1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var payload struct {
		Remain int64  `json:"remain"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error != "" || payload.Remain != 3000 {
		t.Fatalf("quota payload=%s (want remain=3000, no error)", resp.Body)
	}
	if len(violations) != 0 {
		t.Fatalf("control plane used host HTTP bridge: %v", violations)
	}
}

// TestCheckinHandler_DirectNotHostBridged 控制面签到必须直连。
func TestCheckinHandler_DirectNotHostBridged(t *testing.T) {
	var violations []string
	files, _ := json.Marshal(map[string]any{
		"files": []any{map[string]any{"auth_index": "cn1", "provider": "workbuddy", "name": "cn1"}},
	})
	svc := &managementService{
		hostCall: controlPlaneHostCall(&violations, files, controlTestCredential()),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "daily-checkin") {
					w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
					return
				}
				w.Write([]byte(`{"code":0,"msg":"","data":{"Response":{"Data":{"TotalDosage":10000,"Accounts":[{"PackageName":"P1","CapacitySize":5000,"CapacityRemain":3000,"CycleCapacitySize":5000,"CycleCapacityRemain":3000}]}}}}`))
			}}}, nil
		},
	}
	resp, err := svc.checkinHandler([]byte(`{"auth_index":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var payload struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Results) != 1 || payload.Results[0].Status != "OK" {
		t.Fatalf("checkin payload=%s (want one OK result)", resp.Body)
	}
	if len(violations) != 0 {
		t.Fatalf("control plane used host HTTP bridge: %v", violations)
	}
}

// TestModelsHandler_DirectNotHostBridged 控制面模型目录必须直连。
func TestModelsHandler_DirectNotHostBridged(t *testing.T) {
	var violations []string
	files, _ := json.Marshal(map[string]any{
		"files": []any{map[string]any{"auth_index": "cn1", "provider": "workbuddy", "name": "cn1"}},
	})
	svc := &managementService{
		hostCall: controlPlaneHostCall(&violations, files, controlTestCredential()),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"data":{"models":[{"id":"m-ctl-a","name":"Model A","maxInputTokens":8192}]}}`))
			}}}, nil
		},
	}
	resp, err := svc.modelsHandler()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var payload struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Models) == 0 || payload.Models[0].ID != "workbuddy/Model A" {
		t.Fatalf("models payload=%s (want workbuddy/Model A; public ID derives from display name)", resp.Body)
	}
	if len(violations) != 0 {
		t.Fatalf("control plane used host HTTP bridge: %v", violations)
	}
}

// TestNewDirectControlClient_NotHostBridged 直连工厂不得返回宿主桥 Transport。
func TestNewDirectControlClient_NotHostBridged(t *testing.T) {
	client, err := newDirectControlClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Transport.(hostRoundTripper); ok {
		t.Fatal("direct control client must not use hostRoundTripper")
	}
	if client.Timeout != controlHTTPTimeout {
		t.Fatalf("timeout=%v, want %v", client.Timeout, controlHTTPTimeout)
	}
}

// TestOpsHTTPClient_DefaultDirect ops 缺省（hostHTTPFn 未注入）也必须直连。
func TestOpsHTTPClient_DefaultDirect(t *testing.T) {
	ticker := &opsTicker{}
	client, err := ticker.httpClient("any-callback")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Transport.(hostRoundTripper); ok {
		t.Fatal("ops default httpClient must not use hostRoundTripper")
	}
}
