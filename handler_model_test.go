package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestModelForAuthEndToEnd 验证 model.for_auth 端到端：
// httptest 假 /v3/config → FetchAndRegister → Registry 注册 → ResolveModel。
func TestModelForAuthEndToEnd(t *testing.T) {
	// httptest upstream 模拟 /v3/config。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config" {
			http.NotFound(w, r)
			return
		}
		resp := map[string]any{
			"code": 0,
			"data": map[string]any{
				"models": []map[string]any{
					{"id": "model-a", "name": "Model A", "maxInputTokens": 8192},
					{"id": "model-b", "name": "Model B", "maxInputTokens": 4096},
					{"id": "nes-embedding", "name": "Embedding", "maxOutputTokens": 128},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	// Mock host callback: 拦截 MethodHostHTTPDo，将请求转发到 httptest upstream。
	orig := hostJSONCall
	defer func() { hostJSONCall = orig }()
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		return mockHostHTTPDo(method, payload, upstream)
	}

	// 构造 model.for_auth RPC 请求。
	storageJSON := []byte(`{"auth":{"accessToken":"test-token","refreshToken":"rt","expiresAt":9999999999,"domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-test","nickname":"tester"}}`)
	rawReq, _ := json.Marshal(rpcAuthModelRequest{
		HostCallbackID: "test-callback",
		AuthModelRequest: authModelRequestHelper("workbuddy", "auth-u-test", storageJSON),
	})

	// 调用 handleMethod。
	raw, err := handleMethod(pluginabi.MethodModelForAuth, rawReq)
	if err != nil {
		t.Fatal(err)
	}

	// 解析成功响应。
	var successEnv struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct{}       `json:"error"`
	}
	if err := json.Unmarshal(raw, &successEnv); err != nil {
		t.Fatal(err)
	}
	if !successEnv.OK {
		t.Fatalf("expected ok=true, got envelope: %s", raw)
	}

	var resp struct {
		Provider string `json:"Provider"`
		Models   []struct {
			ID          string `json:"ID"`
			Name        string `json:"Name"`
			DisplayName string `json:"DisplayName"`
			Object      string `json:"Object"`
			OwnedBy     string `json:"OwnedBy"`
		} `json:"Models"`
	}
	if err := json.Unmarshal(successEnv.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Provider != "workbuddy" {
		t.Fatalf("provider=%q, want workbuddy", resp.Provider)
	}
	// nes-embedding 应被过滤（maxOutputTokens ≤ 256）
	if len(resp.Models) != 2 {
		t.Fatalf("models count=%d, want 2 (nes-embedding filtered out)", len(resp.Models))
	}

	// 验证 Registry 注册：ResolveModel 应能找到 model-a。
	publicID := resp.Models[0].ID
	internalID, err := defaultRegistry.ResolveModel("auth-u-test", publicID)
	if err != nil {
		t.Fatalf("ResolveModel(%q) error: %v", publicID, err)
	}
	if internalID != "model-a" {
		t.Fatalf("internalID=%q, want model-a", internalID)
	}

	// 验证 DisplayName 语义：DisplayName 来自上游 Name 字段。
	if resp.Models[0].DisplayName != "Model A" {
		t.Fatalf("DisplayName=%q, want Model A", resp.Models[0].DisplayName)
	}
}

// authModelRequestHelper 构造 pluginapi.AuthModelRequest（绕过 JSON tag 大小写问题）。
func authModelRequestHelper(authProvider, authID string, storageJSON []byte) pluginapi.AuthModelRequest {
	return pluginapi.AuthModelRequest{
		AuthProvider: authProvider,
		AuthID:       authID,
		StorageJSON:  storageJSON,
	}
}

// mockHostHTTPDo 通用 host HTTP mock：拦截 MethodHostHTTPDo，转发到 httptest upstream。
func mockHostHTTPDo(method string, payload any, upstream *httptest.Server) (json.RawMessage, error) {
	return mockHostHTTPDoRaw(method, payload, upstream)
}

func mockHostHTTPDoRaw(method string, payload any, upstream *httptest.Server) (json.RawMessage, error) {
	if method != pluginabi.MethodHostHTTPDo {
		return nil, fmt.Errorf("unexpected method: %s", method)
	}
	p, _ := json.Marshal(payload)
	var wrapper struct {
		Request struct {
			Method  string              `json:"Method"`
			URL     string              `json:"URL"`
			Headers map[string][]string `json:"Headers"`
			Body    []byte              `json:"Body"`
		} `json:"request"`
	}
	if err := json.Unmarshal(p, &wrapper); err != nil {
		return nil, fmt.Errorf("decode host request: %w", err)
	}
	// 将上游 URL 替换为 httptest 地址。
	reqURL, err := url.Parse(wrapper.Request.URL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	reqURL.Host = upstream.Listener.Addr().String()
	reqURL.Scheme = "http"

	httpReq, err := http.NewRequestWithContext(context.Background(), wrapper.Request.Method, reqURL.String(), bytes.NewReader(wrapper.Request.Body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, vs := range wrapper.Request.Headers {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	result := hostHTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
	}
	return json.Marshal(result)
}
