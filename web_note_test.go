package main

import (
	"encoding/json"
	"net/http"
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
