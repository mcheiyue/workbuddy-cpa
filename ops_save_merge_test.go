package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// R1 纯函数：写回载荷必须保留旧文件里非自有顶层键（priority/disabled/note），
// 自有键（type/auth/account/device_token）以新载荷为准。
func TestMergeForeignAuthMetadata_PreservesHostKeys(t *testing.T) {
	old := []byte(`{"type":"workbuddy","auth":{"accessToken":"old","refreshToken":"ort"},
		"account":{"uid":"u1"},"disabled":true,"priority":7,"note":"keep-me"}`)
	fresh, _ := json.Marshal(map[string]any{
		"type":         "workbuddy",
		"auth":         map[string]any{"accessToken": "new", "refreshToken": "nrt"},
		"account":      map[string]any{"uid": "u1"},
		"device_token": "dt",
	})
	out, err := mergeForeignAuthMetadata(old, fresh)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["priority"] != float64(7) {
		t.Fatalf("priority=%v want 7 (host key wiped)", m["priority"])
	}
	if m["disabled"] != true {
		t.Fatalf("disabled=%v want true", m["disabled"])
	}
	if m["note"] != "keep-me" {
		t.Fatalf("note=%v want keep-me", m["note"])
	}
	auth, _ := m["auth"].(map[string]any)
	if auth == nil || auth["accessToken"] != "new" {
		t.Fatalf("own auth must come from fresh payload: %v", m["auth"])
	}
	if _, ok := m["device_token"]; !ok {
		t.Fatalf("fresh own key lost: %v", m)
	}
}

// R2 接线：refresh 写回前必须重新 auth.get 物理文件并合并非自有键——
// 相当于 Phase B marker 的回归（priority=7 注入后经写回必须幸存）。
func TestRunKeepalive_SavePreservesForeignKeys(t *testing.T) {
	view := &sharedAuthView{raw: func() []byte {
		raw, _ := json.Marshal(map[string]any{
			"type": "workbuddy",
			"auth": map[string]any{"accessToken": "at0", "refreshToken": "rt_0",
				"expiresAt": time.Now().Add(time.Hour).Unix(), "domain": "codebuddy.cn", "realm": "cn"},
			"account":  map[string]any{"uid": "u1"},
			"priority": 7, "disabled": true,
		})
		return raw
	}()}
	baseCall := mockCallHost(t)
	var saved map[string]any
	ticker := &opsTicker{
		callHostFn: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthGet:
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "cn1", JSON: view.get()})
			case pluginabi.MethodHostAuthSave:
				var req pluginapi.HostAuthSaveRequest
				raw, _ := json.Marshal(payload)
				if err := json.Unmarshal(raw, &req); err != nil {
					return nil, err
				}
				if err := json.Unmarshal(req.JSON, &saved); err != nil {
					return nil, err
				}
				view.set(req.JSON)
				return json.Marshal(map[string]bool{"ok": true})
			}
			return baseCall(method, payload)
		},
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"ok","data":{"accessToken":"at1","refreshToken":"rt_1","expiresIn":3600}}`))
			}}}, nil
		},
	}
	ticker.runKeepalive(raceAcct())
	if saved == nil {
		t.Fatal("no save happened (refresh path not exercised)")
	}
	if saved["priority"] != float64(7) {
		t.Fatalf("saved priority=%v want 7 (writeback wiped host key, Phase B regression)", saved["priority"])
	}
	if saved["disabled"] != true {
		t.Fatalf("saved disabled=%v want true", saved["disabled"])
	}
	if auth, _ := saved["auth"].(map[string]any); auth == nil || auth["refreshToken"] != "rt_1" {
		t.Fatalf("fresh refresh token must win: %v", saved["auth"])
	}
}
