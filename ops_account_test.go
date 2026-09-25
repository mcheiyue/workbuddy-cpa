package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// --- buildWakes 槽位 ---

func TestBuildWakes_FutureSlotsToday(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now)
	if len(wakes) != len(opsDailyTasks) {
		t.Fatalf("wakes=%d, want %d", len(wakes), len(opsDailyTasks))
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
	wakes := buildWakes(now)
	if len(wakes) != 4 {
		t.Fatalf("wakes=%d, want 4", len(wakes))
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

// --- 保活 refresh 互斥 ---

func TestLockAuthRefresh_Serializes(t *testing.T) {
	var wg sync.WaitGroup
	var concurrent int
	var mu sync.Mutex
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockAuthRefresh("auth-x")
			defer unlock()
			mu.Lock()
			concurrent++
			if concurrent != 1 {
				t.Errorf("concurrent holders=%d, want 1", concurrent)
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			concurrent--
			mu.Unlock()
		}()
	}
	wg.Wait()
	unlock := lockAuthRefresh("")
	unlock() // 空 authID 空操作不卡死
}

// --- 活跃上报契约 ---

func TestRunActivity_PostsChatRequestSend(t *testing.T) {
	var gotReq *http.Request
	var body []byte
	var hookErrs []error
	var hookIDs []string
	ticker := &opsTicker{
		callHostFn: mockCallHost(t),
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				gotReq = r
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
			}}}, nil
		},
		tickHook: func(id string, err error) {
			hookIDs = append(hookIDs, id)
			hookErrs = append(hookErrs, err)
		},
	}
	acct := accountInfo{authIndex: "cn1", callbackID: "cn1", realm: wbauth.RealmCN}
	ticker.runActivity(acct)

	if len(hookIDs) != 1 || hookIDs[0] != "cn1" || hookErrs[0] != nil {
		t.Fatalf("hook: ids=%v errs=%v", hookIDs, hookErrs)
	}
	if gotReq == nil {
		t.Fatal("no upstream request")
	}
	if gotReq.URL.Host != "www.codebuddy.cn" {
		t.Fatalf("host=%q, want www.codebuddy.cn", gotReq.URL.Host)
	}
	if gotReq.URL.Path != "/v2/report" {
		t.Fatalf("path=%q, want /v2/report", gotReq.URL.Path)
	}
	if gotReq.Header.Get("X-User-Id") != "cn_uid_123" {
		t.Fatalf("x-user-id=%q", gotReq.Header.Get("X-User-Id"))
	}
	var events []map[string]any
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("body not JSON array: %v (%s)", err, body)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	ev := events[0]
	if ev["eventCode"] != "chat_request_send" {
		t.Fatalf("eventCode=%v", ev["eventCode"])
	}
	if ev["mode"] != "craft" {
		t.Fatalf("mode=%v", ev["mode"])
	}
	if ev["userId"] != "cn_uid_123" {
		t.Fatalf("userId=%v", ev["userId"])
	}
	if ev["agentName"] != "default" {
		t.Fatalf("agentName=%v", ev["agentName"])
	}
	conv, _ := ev["conversationId"].(string)
	if !strings.HasPrefix(conv, "wbcpa-") {
		t.Fatalf("conversationId=%q", conv)
	}
	if ev["rootRequestId"] != ev["conversationId"] {
		t.Fatalf("rootRequestId=%v conversationId=%v", ev["rootRequestId"], ev["conversationId"])
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
