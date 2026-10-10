package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// TestAccountsHandler_ExposesFileAndPriority 账号行必须带宿主文件名与
// priority，供 WebUI 直接 PATCH /v0/management/auth-files/fields 定位
// （name 必填，GetByID 或 FileName 匹配）并回显当前优先级。
// 语义：宿主 conductor 取最高 priority 层（数值越大越优先）。
func TestAccountsHandler_ExposesFileAndPriority(t *testing.T) {
	filesJSON := json.RawMessage(`{"files":[
		{"auth_index":"p1","provider":"workbuddy","label":"global1","name":"workbuddy-p1.json","id":"workbuddy-p1.json","priority":10},
		{"auth_index":"p2","provider":"workbuddy","label":"cn1","name":"workbuddy-p2.json","id":"workbuddy-p2.json","priority":0},
		{"auth_index":"q1","provider":"qoder","label":"q","name":"qoder-x.json","id":"qoder-x","priority":5}]}`)
	svc := &managementService{
		hostCall: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthList:
				return filesJSON, nil
			case pluginabi.MethodHostAuthGet:
				req, ok := payload.(pluginapi.HostAuthGetRequest)
				if !ok {
					t.Fatalf("auth.get payload type=%T", payload)
				}
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: req.AuthIndex, JSON: controlTestCredential()})
			default:
				return nil, errUnexpectedMethod
			}
		},
	}
	resp, err := svc.accountsHandler()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Accounts []managementAccount `json:"accounts"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("accounts=%d want 2: %s", len(out.Accounts), resp.Body)
	}
	byIdx := map[string]managementAccount{}
	for _, a := range out.Accounts {
		byIdx[a.AuthIndex] = a
	}
	p1, ok := byIdx["p1"]
	if !ok {
		t.Fatal("p1 missing")
	}
	if p1.File != "workbuddy-p1.json" {
		t.Errorf("p1.File=%q want workbuddy-p1.json (PATCH name target)", p1.File)
	}
	if p1.Priority != 10 {
		t.Errorf("p1.Priority=%d want 10", p1.Priority)
	}
	p2 := byIdx["p2"]
	if p2.File != "workbuddy-p2.json" {
		t.Errorf("p2.File=%q want workbuddy-p2.json", p2.File)
	}
	// priority 缺席（omitempty→0）：显式 0 与未设置同层，必须能回显 0。
	if p2.Priority != 0 {
		t.Errorf("p2.Priority=%d want 0", p2.Priority)
	}
	// 0 值不能被 omitempty 整字段吞掉（WebUI 需要区分“0”与“字段缺失”）：
	// wire 上 p2 行必须带 "priority":0，否则前端读不到当前值。
	if !strings.Contains(string(resp.Body), `"priority":0`) {
		t.Errorf("wire missing priority:0 (omitempty would swallow zero): %s", resp.Body)
	}
}

// TestAccountsHandler_DuplicateRowKeepsPriority 去重塌缩后幸存行保留
// 自身的 file/priority（重复项若先见无 priority 记录不得回填脏值）。
func TestAccountsHandler_DuplicateRowKeepsPriority(t *testing.T) {
	filesJSON := json.RawMessage(`{"files":[
		{"auth_index":"dup","provider":"workbuddy","label":"workbuddy","name":"workbuddy-d.json","id":"workbuddy-d.json"},
		{"auth_index":"dup","provider":"workbuddy","label":"别名","name":"workbuddy-d.json","id":"workbuddy-d.json","priority":3}]}`)
	svc := &managementService{
		hostCall: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthList:
				return filesJSON, nil
			case pluginabi.MethodHostAuthGet:
				req := payload.(pluginapi.HostAuthGetRequest)
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: req.AuthIndex, JSON: controlTestCredential()})
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
	if len(out.Accounts) != 1 {
		t.Fatalf("accounts=%d want 1: %s", len(out.Accounts), resp.Body)
	}
	got := out.Accounts[0]
	if got.File != "workbuddy-d.json" {
		t.Errorf("File=%q want workbuddy-d.json", got.File)
	}
	// 首见行未设 priority：保留首见行原值 0，不去抄重复行的 3
	// （塌缩只按 auth_index 合并展示，不合并字段——与 Nickname 兜底升级同理，
	//  但 priority 是路由语义，抄错会改选路，故按首见原样）。
	if got.Priority != 0 {
		t.Errorf("Priority=%d want 0 (first-seen row kept as-is)", got.Priority)
	}
}
