package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// swapDeadCounter 换入全新 deadSessions（包级全局；同包测试顺序执行，Cleanup 还原）。
func swapDeadCounter(t *testing.T) *deadCounter {
	t.Helper()
	old := deadSessions
	d := newDeadCounter()
	deadSessions = d
	t.Cleanup(func() { deadSessions = old })
	return d
}

// dead12153Client 固定回 12153 业务码的直连 client（不打真网；忽略 callbackID）。
func dead12153Client(_ string) (*http.Client, error) {
	return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":12153,"msg":"session dead","data":null}`))
	}}}, nil
}

// TestSessionDeadDisablesAtThirdConsecutive 连续 3 次 12153 才禁用（ref sessionDeadThreshold 语义）。
func TestSessionDeadDisablesAtThirdConsecutive(t *testing.T) {
	d := swapDeadCounter(t)
	if d.note("a") {
		t.Fatal("1st 12153 must not disable")
	}
	if d.isDisabled("a") {
		t.Fatal("disabled after 1st")
	}
	if d.note("a") {
		t.Fatal("2nd 12153 must not disable")
	}
	if d.isDisabled("a") || d.count("a") != 2 {
		t.Fatalf("after 2nd: disabled=%v count=%d, want false/2", d.isDisabled("a"), d.count("a"))
	}
	if !d.note("a") {
		t.Fatal("3rd 12153 must disable")
	}
	if !d.isDisabled("a") || d.reason("a") != sessionDeadReason {
		t.Fatalf("disabled=%v reason=%q, want true/%q", d.isDisabled("a"), d.reason("a"), sessionDeadReason)
	}
	if d.count("a") != 0 {
		t.Fatalf("count after disable=%d, want reset 0", d.count("a"))
	}
}

// TestSessionDeadSuccessClearsCount 成功清计次（ref ClearSessionDead：误判有复活路径）。
func TestSessionDeadSuccessClearsCount(t *testing.T) {
	d := swapDeadCounter(t)
	d.note("a")
	d.note("a")
	d.clear("a")
	if d.note("a") || d.note("a") {
		t.Fatal("count must reset after clear: not yet 3")
	}
	if !d.note("a") {
		t.Fatal("fresh consecutive 3rd must disable")
	}
}

// TestSessionDeadDisabledSurvivesClear 已禁用态不被 clear 消除（解禁只走重登换新 authIndex）。
func TestSessionDeadDisabledSurvivesClear(t *testing.T) {
	d := swapDeadCounter(t)
	d.note("a")
	d.note("a")
	d.note("a")
	d.clear("a")
	if !d.isDisabled("a") {
		t.Fatal("clear must not revive a disabled account")
	}
}

// TestRunCheckinSessionDeadDisablesAfterThree 挂起#4 主链路：签到 12153×3 → 禁用。
func TestRunCheckinSessionDeadDisablesAfterThree(t *testing.T) {
	swapDeadCounter(t)
	ticker := &opsTicker{now: time.Now, hostHTTPFn: dead12153Client}
	acct := accountInfo{authIndex: "dn1", callbackID: "dn1", realm: wbauth.RealmCN}
	ticker.runCheckin(acct)
	ticker.runCheckin(acct)
	if deadSessions.isDisabled("dn1") {
		t.Fatal("disabled before 3rd checkin")
	}
	ticker.runCheckin(acct)
	if !deadSessions.isDisabled("dn1") {
		t.Fatal("3rd consecutive 12153 checkin must disable account")
	}
}

// TestRunKeepaliveSessionDeadDisablesAfterThree 保活（refresh）12153×3 → 禁用；失败不写回。
func TestRunKeepaliveSessionDeadDisablesAfterThree(t *testing.T) {
	swapDeadCounter(t)
	var savedNames []string
	ticker := &opsTicker{
		now:        time.Now,
		hostHTTPFn: dead12153Client,
		callHostFn: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthGet:
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "dn2", JSON: controlTestCredential()})
			case pluginabi.MethodHostAuthSave:
				savedNames = append(savedNames, "unexpected-save")
				return nil, errUnexpectedMethod
			default:
				return nil, errUnexpectedMethod
			}
		},
	}
	acct := accountInfo{
		authIndex: "dn2", callbackID: "dn2", realm: wbauth.RealmCN,
		fileName: "dn2.json", authID: "a2",
		cred: wbauth.Credential{AccessToken: "at", RefreshToken: "rt", UID: "u2"},
	}
	ticker.runKeepalive(acct)
	ticker.runKeepalive(acct)
	if deadSessions.isDisabled("dn2") {
		t.Fatal("disabled before 3rd keepalive")
	}
	ticker.runKeepalive(acct)
	if !deadSessions.isDisabled("dn2") {
		t.Fatal("3rd consecutive 12153 keepalive must disable account")
	}
	if len(savedNames) != 0 {
		t.Fatalf("failed refresh must not save, got %v", savedNames)
	}
}

// TestSpawnTickersSkipsDisabled 禁用账号不重建运营 loop。
func TestSpawnTickersSkipsDisabled(t *testing.T) {
	d := swapDeadCounter(t)
	d.note("dn3")
	d.note("dn3")
	d.note("dn3")
	ticker := &opsTicker{
		spawned: map[string]bool{},
		listCNAccountsFn: func() []accountInfo {
			return []accountInfo{{authIndex: "dn3", callbackID: "dn3", realm: wbauth.RealmCN}}
		},
	}
	ticker.spawnTickers()
	if ticker.spawned["dn3"] {
		t.Fatal("disabled account must not spawn")
	}
}

// TestAccountsHandlerReportsDisabledState 管理面 accounts 暴露 disabled/dead_count。
func TestAccountsHandlerReportsDisabledState(t *testing.T) {
	d := swapDeadCounter(t)
	d.note("dn1")
	d.note("dn1")
	d.note("dn1") // dn1 禁用
	d.note("dn2") // dn2 计次 1
	filesJSON := json.RawMessage(`{"files":[
		{"auth_index":"dn1","provider":"workbuddy","label":"黑月","name":"workbuddy-1.json"},
		{"auth_index":"dn2","provider":"workbuddy","label":"月黑","name":"workbuddy-2.json"}]}`)
	svc := &managementService{
		hostCall: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthList:
				return filesJSON, nil
			case pluginabi.MethodHostAuthGet:
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "dn1", JSON: controlTestCredential()})
			default:
				return nil, errUnexpectedMethod
			}
		},
	}
	resp, err := svc.accountsHandler()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Accounts []managementAccount `json:"accounts"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	byIdx := map[string]managementAccount{}
	for _, a := range out.Accounts {
		byIdx[a.AuthIndex] = a
	}
	a1, ok := byIdx["dn1"]
	if !ok {
		t.Fatal("dn1 missing in accounts")
	}
	if !a1.Disabled || a1.DisabledReason != sessionDeadReason {
		t.Fatalf("dn1 disabled=%v reason=%q, want true/%q", a1.Disabled, a1.DisabledReason, sessionDeadReason)
	}
	a2, ok := byIdx["dn2"]
	if !ok {
		t.Fatal("dn2 missing in accounts")
	}
	if a2.Disabled {
		t.Fatal("dn2 must not be disabled at count 1")
	}
	if a2.DeadCount != 1 {
		t.Fatalf("dn2 dead_count=%d, want 1", a2.DeadCount)
	}
}
