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
				w.Write([]byte(`{"code":0,"data":{"models":[{"id":"deepseek-v4.1-flash","name":"DeepSeek-V4.1-Flash","credits":"x0.00"}]}}`))
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
			ID         string `json:"id"`
			InternalID string `json:"internal_id"`
			Realm      string `json:"realm"`
			Credits    string `json:"credits"`
		} `json:"models"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("want 2 per-realm rows, got %d: %s", len(payload.Models), resp.Body)
	}
	gotID := map[string]string{}
	gotCredits := map[string]string{}
	for _, m := range payload.Models {
		gotID[m.Realm] = m.ID
		gotCredits[m.Realm] = m.Credits
	}
	if gotCredits["cn"] != "x0.11" {
		t.Errorf("cn credits=%q want x0.11 (suffix stripped)", gotCredits["cn"])
	}
	if gotCredits["global"] != "x0.00" {
		t.Errorf("global credits=%q want x0.00", gotCredits["global"])
	}
	for _, m := range payload.Models {
		if m.ID != "workbuddy/deepseek-v4.1-flash" {
			t.Errorf("%s callable id=%q want workbuddy/deepseek-v4.1-flash", m.Realm, m.ID)
		}
		if m.InternalID != "deepseek-v4.1-flash" {
			t.Errorf("%s internal_id=%q want deepseek-v4.1-flash", m.Realm, m.InternalID)
		}
	}
	if _, err := defaultRegistry.ResolveModel("cn1", "workbuddy/Deepseek-V4.1-Flash"); err == nil {
		t.Fatal("display-name-derived ID still resolves after the callable-ID migration")
	}
}
