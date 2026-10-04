package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// --- buildWakes 槽位 ---

func TestBuildWakes_FutureSlotsToday(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now, wbauth.RealmCN)
	if len(wakes) != 5 {
		t.Fatalf("wakes=%d, want 5 (CN scope: checkin×2/activity/claim/keepalive)", len(wakes))
	}
	for _, w := range wakes {
		if !w.at.After(now) {
			t.Fatalf("%s wake not future: %v", w.task.name, w.at)
		}
		base := time.Date(w.at.Year(), w.at.Month(), w.at.Day(), w.task.hour, 0, 0, 0, w.at.Location())
		d := w.at.Sub(base)
		if d < 0 || d >= 30*time.Minute {
			t.Fatalf("%s jitter out of [0,30m): %v", w.task.name, d)
		}
	}
}

func TestBuildWakes_RollsPastSlotsToTomorrow(t *testing.T) {
	now := time.Date(2026, 9, 25, 23, 30, 0, 0, time.Local)
	wakes := buildWakes(now, wbauth.RealmCN)
	if len(wakes) != 5 {
		t.Fatalf("wakes=%d, want 5", len(wakes))
	}
	for _, w := range wakes {
		if !w.at.After(now) {
			t.Fatalf("%s not future: %v", w.task.name, w.at)
		}
		if w.at.Day() != 26 {
			t.Fatalf("%s expected tomorrow, got %v", w.task.name, w.at)
		}
	}
}

// --- 保活刷新+写回 ---

func TestRunKeepalive_RefreshesAndSaves(t *testing.T) {
	var savedName string
	var savedJSON []byte
	baseCall := mockCallHost(t)
	ticker := &opsTicker{
		callHostFn: func(method string, payload any) (json.RawMessage, error) {
			if method == pluginabi.MethodHostAuthSave {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				var req pluginapi.HostAuthSaveRequest
				if err := json.Unmarshal(raw, &req); err != nil {
					return nil, err
				}
				savedName, savedJSON = req.Name, req.JSON
				return json.Marshal(map[string]bool{"ok": true})
			}
			return baseCall(method, payload)
		},
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/v2/plugin/auth/token/refresh") {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"ok","data":{"accessToken":"new_at_123","refreshToken":"new_rt_456","expiresIn":3600}}`))
			}}}, nil
		},
		tickHook: func(id string, err error) {
			if err != nil {
				t.Errorf("keepalive hook err: %v", err)
			}
		},
	}
	acct := accountInfo{
		authIndex:  "cn1",
		callbackID: "cn1",
		realm:      wbauth.RealmCN,
		fileName:   "cn1.json",
		authID:     "auth-cn1",
	}
	ticker.runKeepalive(acct)

	if savedName != "cn1.json" {
		t.Fatalf("saved name=%q, want cn1.json", savedName)
	}
	if len(savedJSON) == 0 {
		t.Fatal("no storage written back")
	}
	var stored map[string]any
	if err := json.Unmarshal(savedJSON, &stored); err != nil {
		t.Fatalf("storage not JSON: %v", err)
	}
	auth, _ := stored["auth"].(map[string]any)
	if auth == nil {
		t.Fatalf("storage missing auth block: %s", savedJSON)
	}
	// 写回必须带顶层 type，否则宿主重启扫描（metadata["type"]）丢账号（挂起#12）。
	if stored["type"] != "workbuddy" {
		t.Fatalf("storage type=%v, want workbuddy: %s", stored["type"], savedJSON)
	}
	if auth["accessToken"] != "new_at_123" {
		t.Fatalf("accessToken=%v, want new_at_123", auth["accessToken"])
	}
	if auth["refreshToken"] != "new_rt_456" {
		t.Fatalf("refreshToken=%v, want new_rt_456", auth["refreshToken"])
	}
}

// 无物理文件（运行时注入）时保活应跳过，不报错。
func TestRunKeepalive_SkipsRuntimeAuth(t *testing.T) {
	hookFired := false
	ticker := &opsTicker{
		tickHook: func(id string, err error) {
			hookFired = true
			if err != nil {
				t.Errorf("unexpected err for skip: %v", err)
			}
		},
	}
	ticker.runKeepalive(accountInfo{authIndex: "rt1", callbackID: "rt1", realm: wbauth.RealmCN})
	if !hookFired {
		t.Fatal("hook should fire even on skip")
	}
}
