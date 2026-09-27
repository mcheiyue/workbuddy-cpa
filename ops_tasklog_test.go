package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// --- taskRing 环行为（截断保尾 + snapshot 拷贝） ---

func TestTaskRingAppendSnapshot(t *testing.T) {
	r := newTaskRing(3)
	for i := 0; i < 5; i++ {
		r.append(taskLogEntry{Task: "t", AuthID: "auth", OK: true})
	}
	got := r.snapshot()
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (cap)", len(got))
	}
	// 保尾：第 3-5 条（i=2,3,4）—— 用 Err 区分每条。
	r2 := newTaskRing(3)
	for i := 0; i < 5; i++ {
		r2.append(taskLogEntry{Task: "t", OK: true, Err: string(rune('a' + i))})
	}
	snap := r2.snapshot()
	if snap[0].Err != "c" || snap[2].Err != "e" {
		t.Fatalf("trim not tail-keeping: first=%q last=%q, want c..e", snap[0].Err, snap[2].Err)
	}
	// snapshot 是拷贝：改快照不影响环。
	snap[0].Err = "mutated"
	if r2.snapshot()[0].Err != "c" {
		t.Fatal("snapshot must be a copy")
	}
}

// --- 空环序列化必须是 [] 而非 null（线上 0.1.26 实测 {"tasks":null} 形状缺陷） ---

func TestTaskRingEmptySnapshotJSON(t *testing.T) {
	r := newTaskRing(10)
	raw, err := json.Marshal(map[string]any{"tasks": r.snapshot()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"tasks":[]}` {
		t.Fatalf("want {\"tasks\":[]}, got %s", raw)
	}
}

// --- finishTask 记环（ok/err 双分支） ---

func TestFinishTaskAppendsGlobal(t *testing.T) {
	before := len(globalTaskLog.snapshot())
	ticker := &opsTicker{now: time.Now}
	ticker.finishTask("keepalive", "authX", nil)
	ticker.finishTask("claim", "authY", errors.New("boom"))
	after := globalTaskLog.snapshot()
	if len(after) != before+2 {
		t.Fatalf("len after=%d, want %d", len(after), before+2)
	}
	ok := after[len(after)-2]
	fail := after[len(after)-1]
	if !ok.OK || ok.Task != "keepalive" || ok.AuthID != "authX" || ok.Err != "" {
		t.Fatalf("ok entry wrong: %+v", ok)
	}
	if fail.OK || fail.Task != "claim" || fail.Err != "boom" {
		t.Fatalf("fail entry wrong: %+v", fail)
	}
}

// --- runCheckin already 分支记环（ok=true, err="already"） ---

func TestRunCheckinAlreadyLogged(t *testing.T) {
	before := len(globalTaskLog.snapshot())
	ticker := &opsTicker{
		now: time.Now,
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":10001,"msg":"今日已签到"}`))
			})}}, nil
		},
	}
	acct := accountInfo{authIndex: "already-auth", realm: "cn", cred: wbauth.Credential{AccessToken: "tok"}, callbackID: "already-auth"}
	ticker.runCheckin(acct)
	after := globalTaskLog.snapshot()
	if len(after) != before+1 {
		t.Fatalf("len after=%d, want %d (already records once, finishTask skipped)", len(after), before+1)
	}
	e := after[len(after)-1]
	if !e.OK || e.Task != "checkin" || e.AuthID != "already-auth" || e.Err != "already" {
		t.Fatalf("already entry wrong: %+v", e)
	}
}

// --- tasksHandler 返回 tasks 键 ---

func TestTasksHandlerReturnsTasksKey(t *testing.T) {
	globalTaskLog.append(taskLogEntry{Task: "handler-pre", AuthID: "p1", OK: true})
	resp, err := defaultOpsManagementService.tasksHandler()
	if err != nil {
		t.Fatalf("tasksHandler: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if _, ok := payload["tasks"]; !ok {
		t.Fatalf("payload missing tasks key: %s", resp.Body)
	}
}

// --- 路由派发 /v0/management/workbuddy/tasks ---

func TestManagementHandle_TasksRoute(t *testing.T) {
	resp := decodeLikeHost(t, pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/workbuddy/tasks",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("body not decodable: %v", err)
	}
	if _, ok := payload["tasks"]; !ok {
		t.Fatalf("payload missing tasks key: %s", resp.Body)
	}
}
