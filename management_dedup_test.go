package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// TestAccountsHandler_DedupsDuplicateAuthIndex 同一 auth 文件被宿主注册成两条记录
// （扫描 AuthParse 的 ID=workbuddy-<uid> 与保活 host.auth.save 建的 ID=<file>.json，
// 二者 auth_index 相同）时，账号页必须按 auth_index 去重，且保留真实昵称标签
// （非 provider 兜底名），重复条目不再触发多余的 auth.get。
func TestAccountsHandler_DedupsDuplicateAuthIndex(t *testing.T) {
	filesJSON := json.RawMessage(`{"files":[
		{"auth_index":"dup1","provider":"workbuddy","label":"workbuddy","name":"workbuddy-d1.json","id":"workbuddy-d1.json"},
		{"auth_index":"dup1","provider":"workbuddy","label":"黑月","name":"workbuddy-d1.json","id":"workbuddy-d1"},
		{"auth_index":"uniq1","provider":"workbuddy","label":"月黑","name":"workbuddy-d2.json","id":"workbuddy-d2"},
		{"auth_index":"q1","provider":"qoder","label":"q","name":"qoder-x.json","id":"qoder-x"}]}`)
	getCalls := 0
	svc := &managementService{
		hostCall: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthList:
				return filesJSON, nil
			case pluginabi.MethodHostAuthGet:
				getCalls++
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
	var out struct {
		Accounts []managementAccount `json:"accounts"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 2 {
		t.Fatalf("accounts=%d want 2 (dup pair collapsed, qoder filtered): %s", len(out.Accounts), resp.Body)
	}
	byIdx := map[string]managementAccount{}
	for _, a := range out.Accounts {
		byIdx[a.AuthIndex] = a
	}
	dup, ok := byIdx["dup1"]
	if !ok {
		t.Fatal("dup1 missing")
	}
	if dup.Nickname != "黑月" {
		t.Fatalf("dup1 nickname=%q want 真实昵称 黑月 (fallback label upgraded)", dup.Nickname)
	}
	if _, ok := byIdx["uniq1"]; !ok {
		t.Fatal("uniq1 missing")
	}
	if getCalls != 2 {
		t.Fatalf("auth.get calls=%d want 2 (duplicate entry must skip auth.get)", getCalls)
	}
}

// TestModelsHandler_DedupsDuplicateAuthIndex 模型目录对重复注册的同一 auth_index
// 只拉取一次上游（重复条目跳过 auth.get 与 FetchAndRegister）。
func TestModelsHandler_DedupsDuplicateAuthIndex(t *testing.T) {
	filesJSON := json.RawMessage(`{"files":[
		{"auth_index":"dup1","provider":"workbuddy","label":"workbuddy","name":"workbuddy-d1.json","id":"workbuddy-d1.json"},
		{"auth_index":"dup1","provider":"workbuddy","label":"黑月","name":"workbuddy-d1.json","id":"workbuddy-d1"}]}`)
	getCalls := 0
	svc := &managementService{
		hostCall: func(method string, payload any) (json.RawMessage, error) {
			switch method {
			case pluginabi.MethodHostAuthList:
				return filesJSON, nil
			case pluginabi.MethodHostAuthGet:
				getCalls++
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "dup1", JSON: controlTestCredential()})
			default:
				return nil, errUnexpectedMethod
			}
		},
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"code":0,"data":{"models":[{"id":"m-ctl-a","name":"Model A","maxInputTokens":8192}]}}`))
			}}}, nil
		},
	}
	resp, err := svc.modelsHandler()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if getCalls != 1 {
		t.Fatalf("auth.get calls=%d want 1 (duplicate auth_index must be fetched once)", getCalls)
	}
}
