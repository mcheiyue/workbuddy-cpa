package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// --- 手动签到 realm 门控（2026-10-15 修 D4 漏口） ---
// 自动侧 global 不排 checkin（ref D4 防风控）；手动入口此前无门控会给
// global 号打 daily-checkin 端点。本测锁：global 记 SKIP、零上游请求。

// TestCheckinHandler_SkipGlobalRealm 手动全部签到时 global 跳过：
// 上游 daily-checkin 只被 CN 触发；结果含 global 行 status=SKIP。
func TestCheckinHandler_SkipGlobalRealm(t *testing.T) {
	files, _ := json.Marshal(map[string]any{
		"files": []any{
			map[string]any{"auth_index": "cn1", "provider": "workbuddy", "name": "cn1"},
			map[string]any{"auth_index": "gl1", "provider": "workbuddy", "name": "gl1"},
		},
	})
	cnCred, _ := json.Marshal(map[string]any{
		"auth": map[string]any{
			"accessToken": "cn-tok", "refreshToken": "cn-rt",
			"expiresAt": time.Now().Add(time.Hour).Unix(),
			"domain":    "www.workbuddy.cn", "realm": "cn",
		},
		"account": map[string]any{"uid": "uid-cn", "nickname": "cn"},
	})
	glCred, _ := json.Marshal(map[string]any{
		"auth": map[string]any{
			"accessToken": "gl-tok", "refreshToken": "gl-rt",
			"expiresAt": time.Now().Add(time.Hour).Unix(),
			"domain":    "www.workbuddy.ai", "realm": "global",
		},
		"account": map[string]any{"uid": "uid-gl", "nickname": "gl"},
	})
	var checkinHits []string
	svc := &managementService{
		hostCall: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthList:
				return files, nil
			case pluginabi.MethodHostAuthGet:
				req := payload.(pluginapi.HostAuthGetRequest)
				cred := cnCred
				if req.AuthIndex == "gl1" {
					cred = glCred
				}
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: req.AuthIndex, JSON: cred})
			default:
				return nil, errUnexpectedMethod
			}
		},
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "daily-checkin") {
					checkinHits = append(checkinHits, r.Host+r.URL.Path)
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
			UID    string `json:"uid"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, resp.Body)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("results=%d, want 2 (cn+global rows both present)", len(payload.Results))
	}
	skips, oks := 0, 0
	for _, r := range payload.Results {
		switch r.Status {
		case "SKIP":
			skips++
		case "OK":
			oks++
		}
	}
	if skips != 1 || oks != 1 {
		t.Fatalf("statuses=%v (skip=%d ok=%d), want one SKIP(global)+one OK(cn)", payload.Results, skips, oks)
	}
	for _, h := range checkinHits {
		if strings.Contains(h, "workbuddy.ai") {
			t.Fatalf("global must not hit daily-checkin upstream, got %s", h)
		}
	}
	if len(checkinHits) == 0 {
		t.Fatal("cn daily-checkin must hit upstream at least once")
	}
}
