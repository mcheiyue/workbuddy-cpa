package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// TestAccountsHandler_ExposesNote 账号行必须透出宿主 note 字段：
// 宿主 auth_files list 已从 Attributes/Metadata 读 note（auth_files.go:417），
// WebUI 备注名编辑靠 PATCH /auth-files/fields {name, note}（auth_files.go:278 支持），
// 回显链路缺 note 则前端不知道当前备注值。
func TestAccountsHandler_ExposesNote(t *testing.T) {
	filesJSON := json.RawMessage(`{"files":[
		{"auth_index":"p1","provider":"workbuddy","label":"19974904853","name":"workbuddy-p1.json","id":"workbuddy-p1.json","priority":0,"note":"主力手机号"},
		{"auth_index":"p2","provider":"workbuddy","label":"moqutu","name":"workbuddy-p2.json","id":"workbuddy-p2.json","priority":10}]}`)
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
	byIdx := map[string]managementAccount{}
	for _, a := range out.Accounts {
		byIdx[a.AuthIndex] = a
	}
	p1, ok := byIdx["p1"]
	if !ok {
		t.Fatal("p1 missing")
	}
	if p1.Note != "主力手机号" {
		t.Errorf("p1.Note=%q want 主力手机号", p1.Note)
	}
	// note 缺席：空串，wire 不带键（omitempty）或带空串均可，前端容忍。
	p2 := byIdx["p2"]
	if p2.Note != "" {
		t.Errorf("p2.Note=%q want empty", p2.Note)
	}
}

// TestWebUILoginMethodInference WebUI 组合档契约：
// ① 登录方式按昵称格式推断（上游不提供 login_method，两独立实现均只有 uid/nickname）；
// ② 显示名备注优先（note > nickname）；
// ③ 备注编辑走 PATCH auth-files/fields {name, note}。
func TestWebUILoginMethodInference(t *testing.T) {
	html := string(workbuddyWebUI)

	// ① 推断函数与三分类
	if !strings.Contains(html, "function loginMethod") {
		t.Fatal("loginMethod inference function missing")
	}
	for _, want := range []string{"手机(推断)", "邮箱(推断)", "自定义(推断)"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing badge %q", want)
		}
	}
	// 推断输入是原始昵称而非备注（用户备注不改变登录方式事实）
	i := strings.Index(html, "function loginMethod")
	if i < 0 {
		t.Fatal("loginMethod missing")
	}
	chunk := html[i : i+400]
	if !strings.Contains(chunk, "nickname") {
		t.Error("loginMethod must infer from nickname (upstream identity), not note")
	}

	// ② 显示名 note 优先
	if !strings.Contains(html, "x.note||x.nickname") && !strings.Contains(html, "x.note || x.nickname") {
		t.Error("display name must prefer note over nickname")
	}

	// ③ 备注编辑 PATCH
	if !strings.Contains(html, "saveNote") {
		t.Error("saveNote editor missing")
	}
	j := strings.Index(html, "async function saveNote")
	if j < 0 {
		t.Fatal("saveNote missing")
	}
	chunk2 := html[j : j+500]
	if !strings.Contains(chunk2, "note:") && !strings.Contains(chunk2, "note :") {
		t.Error("saveNote must PATCH {name, note}")
	}
	if !strings.Contains(chunk2, "auth-files/fields") {
		t.Error("saveNote must target auth-files/fields")
	}
}
