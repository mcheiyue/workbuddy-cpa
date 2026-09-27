package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// 共享 auth 视图：模拟宿主记录，refresh 写回后 auth.get 必须见到新 refresh_token。
type sharedAuthView struct {
	mu  sync.Mutex
	raw []byte
}

func (v *sharedAuthView) get() []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]byte(nil), v.raw...)
}

func (v *sharedAuthView) set(raw []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.raw = append([]byte(nil), raw...)
}

func rtStorage(rt string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"type": "workbuddy",
		"auth": map[string]any{
			"accessToken":  "at0",
			"refreshToken": rt,
			"expiresAt":    time.Now().Add(time.Hour).Unix(),
			"domain":       "codebuddy.cn",
			"realm":        "cn",
		},
		"account": map[string]any{"uid": "u1"},
	})
	return raw
}

func raceAcct() accountInfo {
	return accountInfo{
		authIndex:  "cn1",
		callbackID: "cn1",
		realm:      wbauth.RealmCN,
		fileName:   "cn1.json",
		authID:     "auth-cn1",
	}
}

// B3（ref refresh_race_test 同款前置检查）：无 refresh_token 的账号不发 refresh。
func TestRunKeepalive_SkipsWithoutRefreshToken(t *testing.T) {
	var refreshCalls atomic.Int32
	view := &sharedAuthView{raw: rtStorage("")}
	baseCall := mockCallHost(t)
	ticker := &opsTicker{
		callHostFn: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthGet:
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "cn1", JSON: view.get()})
			case pluginabi.MethodHostAuthSave:
				t.Error("must not save without refresh_token")
			}
			return baseCall(method, payload)
		},
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				refreshCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"msg":"ok","data":{"accessToken":"at1","refreshToken":"rt1","expiresIn":3600}}`))
			}}}, nil
		},
		tickHook: func(id string, err error) {
			if err != nil {
				t.Errorf("keepalive hook err: %v", err)
			}
		},
	}
	ticker.runKeepalive(raceAcct())
	if n := refreshCalls.Load(); n != 0 {
		t.Fatalf("refresh calls=%d, want 0 (no refresh_token)", n)
	}
}

// B3 核心：并发保活每路必须用锁内重读的最新 refresh_token。
// 锁外读（旧行为）：多路并发 freshCred 都读到 rt_0 → 重复使用已轮换 RT → 上游作废风险。
// 锁内重读（正确行为）：序列 rt_0→rt_1→rt_2→rt_3 无重复。
func TestRunKeepalive_ConcurrentRefreshUsesFreshToken(t *testing.T) {
	var (
		recMu sync.Mutex
		rts   []string
	)
	view := &sharedAuthView{raw: rtStorage("rt_0")}
	baseCall := mockCallHost(t)
	var hookErrs atomic.Int32
	ticker := &opsTicker{
		callHostFn: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthGet:
				// 放大并发读重叠窗：让旧实现的多路读都落在首个写回之前。
				time.Sleep(30 * time.Millisecond)
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "cn1", JSON: view.get()})
			case pluginabi.MethodHostAuthSave:
				var req pluginapi.HostAuthSaveRequest
				raw, _ := json.Marshal(payload)
				if err := json.Unmarshal(raw, &req); err != nil {
					return nil, err
				}
				view.set(req.JSON)
				return json.Marshal(map[string]bool{"ok": true})
			}
			return baseCall(method, payload)
		},
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/v2/plugin/auth/token/refresh") {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				recMu.Lock()
				rts = append(rts, r.Header.Get("X-Refresh-Token"))
				n := len(rts)
				recMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"code":0,"msg":"ok","data":{"accessToken":"at_%d","refreshToken":"rt_%d","expiresIn":3600}}`, n, n)
			}}}, nil
		},
		tickHook: func(id string, err error) {
			if err != nil {
				hookErrs.Add(1)
			}
		},
	}

	const workers = 4
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker.runKeepalive(raceAcct())
		}()
	}
	wg.Wait()

	if len(rts) != workers {
		t.Fatalf("refresh calls=%d, want %d (rts=%v)", len(rts), workers, rts)
	}
	seen := map[string]int{}
	for _, rt := range rts {
		seen[rt]++
	}
	if len(seen) != workers {
		t.Fatalf("stale refresh_token reused across keepalives (read outside lock): %v", rts)
	}
	if rts[0] != "rt_0" {
		t.Fatalf("first refresh must use rt_0, got %v", rts)
	}
	if n := hookErrs.Load(); n != 0 {
		t.Fatalf("hook errors=%d, want 0 (fresh-token keepalives must all succeed)", n)
	}
}
