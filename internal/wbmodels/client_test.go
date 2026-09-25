package wbmodels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// --- FetchConfig 基本 ---

func TestFetchConfig_Success(t *testing.T) {
	resp := map[string]any{
		"code": 0,
		"data": map[string]any{
			"models": []map[string]any{
				{"id": "gpt-5.6", "name": "GPT 5.6"},
				{"id": "hy4-preview", "name": "HY4 Preview"},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config" {
			t.Errorf("path=%q, want /v3/config", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// 注入假 base URL：用 httptest 的 URL 替换 wbauth.RealmBase
	// 由于 wbauth.RealmBase 是硬编码，我们直接用 FetchAndParse 绕过 URL 构造
	models, err := fetchAndParseFromURL(srv.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models=%d, want 2", len(models))
	}
}

func TestFetchConfig_NonZeroCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":401,"msg":"unauthorized","data":null}`))
	}))
	defer srv.Close()

	_, err := fetchAndParseFromURL(srv.URL, "bad-token")
	if err == nil {
		t.Fatal("expected error for non-zero code")
	}
}

func TestFetchConfig_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	_, err := fetchAndParseFromURL(srv.URL, "token")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// --- FilterAndRegister 端到端 ---

func TestFetchAndRegister_EndToEnd(t *testing.T) {
	resp := map[string]any{
		"code": 0,
		"data": map[string]any{
			"models": []map[string]any{
				{"id": "model-a", "name": "Model A"},
				{"id": "model-a", "name": "duplicate"},
				{"id": "model-b", "name": "Model B"},
				{"id": "nes-embed", "name": "Embed"},
				{"id": "tiny", "name": "Tiny", "maxOutputTokens": 64},
				{"id": "", "name": ""},             // 空 id + 空 name → 过滤
				{"disabled": true, "name": "Disabled"},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	reg := NewRegistry()
	models, err := fetchAndRegisterFromURL(srv.URL, "token", "auth-test", reg)
	if err != nil {
		t.Fatal(err)
	}
	// model-a + model-b = 2 个有效模型
	if len(models) != 2 {
		t.Fatalf("models=%d, want 2", len(models))
	}
	// 验证映射注册
	got, err := reg.ResolveModel("auth-test", models[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "model-a" {
		t.Errorf("resolve=%q, want model-a", got)
	}
}

func TestFetchAndRegister_PerAuthIsolation(t *testing.T) {
	resp := map[string]any{
		"code": 0,
		"data": map[string]any{
			"models": []map[string]any{
				{"id": "shared-model", "name": "Shared"},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	reg := NewRegistry()
	models1, _ := fetchAndRegisterFromURL(srv.URL, "token", "auth-1", reg)
	models2, _ := fetchAndRegisterFromURL(srv.URL, "token", "auth-2", reg)

	// 两个 authID 各自独立映射
	got1, _ := reg.ResolveModel("auth-1", models1[0].ID)
	got2, _ := reg.ResolveModel("auth-2", models2[0].ID)
	if got1 != "shared-model" || got2 != "shared-model" {
		t.Errorf("isolation: auth-1=%q auth-2=%q", got1, got2)
	}
}

// --- ToPluginModels ---

func TestToPluginModels(t *testing.T) {
	models := []ModelInfo{
		{ID: "workbuddy/Model A", Name: "Model A", SupportsReason: true, MaxInputTokens: 131072},
		{ID: "workbuddy/Model B", Name: "Model B"},
	}
	out := ToPluginModels("workbuddy", models)
	if len(out) != 2 {
		t.Fatalf("out=%d, want 2", len(out))
	}
	if out[0].Thinking == nil || !out[0].Thinking.ZeroAllowed {
		t.Error("Model A should have thinking support")
	}
	if out[1].Thinking != nil {
		t.Error("Model B should not have thinking support")
	}
	if out[0].InputTokenLimit != 131072 {
		t.Errorf("InputTokenLimit=%d, want 131072", out[0].InputTokenLimit)
	}
}

// --- 并发请求 ---

func TestFetchConfig_Concurrent(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		resp := map[string]any{
			"code": 0,
			"data": map[string]any{"models": []map[string]any{{"id": "m1", "name": "M1"}}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetchAndRegisterFromURL(srv.URL, "token", "auth-concurrent", reg)
		}()
	}
	wg.Wait()
	if c := count.Load(); c != 20 {
		t.Errorf("requests=%d, want 20", c)
	}
}

// --- 辅助函数：绕过 wbauth.RealmBase 直接用 httptest URL ---

func fetchAndParseFromURL(baseURL, accessToken string) ([]ModelInfo, error) {
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v3/config", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	return ParseConfigModels(raw)
}

func fetchAndRegisterFromURL(baseURL, accessToken, authID string, reg *Registry) ([]ModelInfo, error) {
	models, err := fetchAndParseFromURL(baseURL, accessToken)
	if err != nil {
		return nil, err
	}
	mapping, entries := FilterAndBuild(models)
	reg.Store(authID, mapping)
	return entries, nil
}
