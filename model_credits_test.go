package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// credRealm 构造指定 realm 的凭证 JSON（仅模型目录测试用）。
func credRealm(realm, domain, token string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"auth": map[string]any{
			"accessToken":  token,
			"refreshToken": "rt",
			"expiresAt":    time.Now().Add(time.Hour).Unix(),
			"domain":       domain,
			"realm":        realm,
		},
		"account": map[string]any{"uid": "u-" + realm, "nickname": realm},
	})
	return raw
}

// TestModelsHandler_PerRealmCredits 模型目录必须按 realm 区分倍率：
// 同一模型在两 realm 倍率不同（deepseek-v4.1-flash：global x0.00 / cn x0.11），
// 按 realm+id 去重后应出现两条，各带自己的 credits 与区域标签。
func TestModelsHandler_PerRealmCredits(t *testing.T) {
	files, _ := json.Marshal(map[string]any{
		"files": []any{
			map[string]any{"auth_index": "cn1", "provider": "workbuddy", "name": "cn1"},
			map[string]any{"auth_index": "gl1", "provider": "workbuddy", "name": "gl1"},
		},
	})
	hostCall := func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthList:
			return files, nil
		case pluginabi.MethodHostAuthGet:
			req, ok := payload.(pluginapi.HostAuthGetRequest)
			if !ok {
				t.Fatalf("auth.get payload type=%T", payload)
			}
			if req.AuthIndex == "cn1" {
				return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "cn1", JSON: credRealm("cn", "www.workbuddy.cn", "cn-token")})
			}
			return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: "gl1", JSON: credRealm("global", "www.workbuddy.ai", "gl-token")})
		default:
			return nil, errUnexpectedMethod
		}
	}
	svc := &managementService{
		hostCall: hostCall,
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if tok == "cn-token" {
					w.Write([]byte(`{"code":0,"data":{"models":[{"id":"deepseek-v4.1-flash","name":"Deepseek-V4.1-Flash","credits":"x0.11 credits"}]}}`))
					return
				}
				w.Write([]byte(`{"code":0,"data":{"models":[{"id":"deepseek-v4.1-flash","name":"Deepseek-V4.1-Flash","credits":"x0.00"}]}}`))
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
	var payload struct {
		Models []struct {
			ID      string `json:"id"`
			Realm   string `json:"realm"`
			Credits string `json:"credits"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("want 2 per-realm rows, got %d: %s", len(payload.Models), resp.Body)
	}
	got := map[string]string{}
	for _, m := range payload.Models {
		got[m.Realm] = m.Credits
	}
	if got["cn"] != "x0.11" {
		t.Errorf("cn credits=%q want x0.11 (suffix stripped)", got["cn"])
	}
	if got["global"] != "x0.00" {
		t.Errorf("global credits=%q want x0.00", got["global"])
	}
}

// TestWebUIModelsCreditsColumn 模型目录前端契约：倍率列 + 免费高亮 + 区域标签。
func TestWebUIModelsCreditsColumn(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{"creditsCell", "免费", "realmTag", "倍率"} {
		if !strings.Contains(html, want) {
			t.Errorf("model directory rendering missing %q", want)
		}
	}
	// 页脚文案须反映 realm+id 去重（同一模型两区域各一行）与实时倍率，
	// 否则沿用旧「已按调用 ID 去重」会误导（该键已废弃）。
	for _, want := range []string{"区域+调用 ID", "实时拉上游"} {
		if !strings.Contains(html, want) {
			t.Errorf("model directory footer missing updated copy %q", want)
		}
	}
}
