package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// executorRequestHelper 构造 rpcExecutorRequest。
func executorRequestHelper(authProvider, authID, model string, storageJSON, payload []byte, streamID string) rpcExecutorRequest {
	return rpcExecutorRequest{
		ExecutorRequest: pluginapi.ExecutorRequest{
			AuthProvider: authProvider,
			AuthID:       authID,
			Model:        model,
			StorageJSON:  storageJSON,
			Payload:      payload,
			Headers:      http.Header{},
		},
		StreamID:       streamID,
		HostCallbackID: "test-callback",
	}
}

func executorStreamRequestHelper(authProvider, authID, model string, storageJSON, payload []byte, streamID string) rpcExecutorRequest {
	return executorRequestHelper(authProvider, authID, model, storageJSON, payload, streamID)
}

// TestExecutorExecuteRPCTrack 验证 executor.execute 经 handleMethod 的 RPC 往返。
func TestExecutorExecuteRPCTrack(t *testing.T) {
	// 预注册模型映射：公开 ID → 内部 key。
	defaultRegistry.Store("auth-u-test", map[string]string{
		"workbuddy/test-model": "test-model",
	})

	// httptest upstream 模拟 /v2/chat/completions（返回合法 SSE 聚合响应）。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/chat/completions" {
			http.NotFound(w, r)
			return
		}
		// 返回 SSE 流（executor.Execute 会本地聚合）。
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	orig := hostJSONCall
	defer func() { hostJSONCall = orig }()
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		return mockHostHTTPDo(method, payload, upstream)
	}

	storageJSON := []byte(`{"auth":{"accessToken":"test-token","refreshToken":"rt","expiresAt":9999999999,"domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-test","nickname":"tester"}}`)
	payload := []byte(`{"model":"workbuddy/test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	rawReq, _ := json.Marshal(executorRequestHelper("workbuddy", "auth-u-test", "workbuddy/test-model", storageJSON, payload, ""))

	raw, err := handleMethod(pluginabi.MethodExecutorExecute, rawReq)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, envelope: %s", raw)
	}

	var resp struct {
		Payload []byte      `json:"Payload"`
		Headers http.Header `json:"Headers"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	// 验证返回的是合法 chat.completion JSON。
	var completion struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(resp.Payload, &completion); err != nil {
		t.Fatalf("invalid completion JSON: %v", err)
	}
	if completion.ID != "cmpl-1" {
		t.Fatalf("id=%q, want cmpl-1", completion.ID)
	}
	if completion.Object != "chat.completion" {
		t.Fatalf("object=%q, want chat.completion", completion.Object)
	}
	// model 应替换为公开 ID。
	if completion.Model != "workbuddy/test-model" {
		t.Fatalf("model=%q, want workbuddy/test-model", completion.Model)
	}
	if len(completion.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	if completion.Choices[0].Message.Content != "hello" {
		t.Fatalf("content=%q, want hello", completion.Choices[0].Message.Content)
	}
}

// TestExecutorExecuteStreamRPCTrack 验证 executor.execute_stream 经 handleMethod 的 RPC 往返。
// 重点：StreamEmit 收到的是裸 JSON chunk（无 "data: " 前缀）。
func TestExecutorExecuteStreamRPCTrack(t *testing.T) {
	// 预注册模型映射。
	defaultRegistry.Store("auth-u-test", map[string]string{
		"workbuddy/stream-model": "stream-model",
	})

	// httptest upstream 返回 SSE 流。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"world\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"model\":\"stream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	// 收集 StreamEmit 调用的 payload。
	var emittedChunks []string
	var streamBody []byte
	var streamStatus int
	var streamHeaders http.Header
	var streamDone bool
	orig := hostJSONCall
	defer func() { hostJSONCall = orig }()
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		// 拦截 stream emit 和 stream close。
		switch method {
		case pluginabi.MethodHostStreamEmit:
			p, ok := payload.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid payload type for stream emit")
			}
			chunkBytes, _ := p["payload"].([]byte)
			emittedChunks = append(emittedChunks, string(chunkBytes))
			return json.Marshal(struct{}{})
		case pluginabi.MethodHostStreamClose:
			return json.Marshal(struct{}{})
		case pluginabi.MethodHostHTTPDo:
			return mockHostHTTPDoRaw(method, payload, upstream)
		case pluginabi.MethodHostHTTPDoStream:
			// 复用 do 的上游打点，转成流式 wire（status_code/stream_id）。
			raw, err := mockHostHTTPDoRaw(pluginabi.MethodHostHTTPDo, payload, upstream)
			if err != nil {
				return nil, err
			}
			var hr hostHTTPResponse
			if err := json.Unmarshal(raw, &hr); err != nil {
				return nil, err
			}
			streamBody, streamStatus, streamHeaders, streamDone = hr.Body, hr.StatusCode, hr.Headers, false
			return json.Marshal(map[string]any{
				"status_code": streamStatus,
				"headers":     streamHeaders,
				"stream_id":   "us-1",
			})
		case pluginabi.MethodHostHTTPStreamRead:
			if !streamDone {
				streamDone = true
				return json.Marshal(map[string]any{"payload": streamBody, "done": true})
			}
			return json.Marshal(map[string]any{"done": true})
		case pluginabi.MethodHostHTTPStreamClose:
			return json.Marshal(struct{}{})
		}
		return nil, fmt.Errorf("unexpected method: %s", method)
	}

	storageJSON := []byte(`{"auth":{"accessToken":"test-token","refreshToken":"rt","expiresAt":9999999999,"domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-test","nickname":"tester"}}`)
	payload := []byte(`{"model":"workbuddy/stream-model","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	rawReq, _ := json.Marshal(executorStreamRequestHelper("workbuddy", "auth-u-test", "workbuddy/stream-model", storageJSON, payload, "stream-001"))

	raw, err := handleMethod(pluginabi.MethodExecutorExecuteStream, rawReq)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, envelope: %s", raw)
	}

	var resp struct {
		Headers http.Header `json:"Headers"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if ct := resp.Headers.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type=%q, want text/event-stream", ct)
	}

	// 验证 StreamEmit 收到裸 JSON chunk（无 "data: " 前缀）。
	if len(emittedChunks) == 0 {
		t.Fatal("expected at least one emitted chunk")
	}
	for i, chunk := range emittedChunks {
		if len(chunk) == 0 {
			t.Fatalf("chunk[%d] is empty", i)
		}
		// 裸 JSON：应以 '{' 开头，不包含 "data: " 前缀。
		if chunk[0] != '{' {
			t.Fatalf("chunk[%d] does not start with '{': %q", i, chunk)
		}
		// 验证是合法 JSON。
		if !json.Valid([]byte(chunk)) {
			t.Fatalf("chunk[%d] is not valid JSON: %q", i, chunk)
		}
	}

	// 最后一个 chunk 的 model 应为公开 ID。
	lastChunk := emittedChunks[len(emittedChunks)-1]
	var parsed struct {
		Model string `json:"model"`
	}
	json.Unmarshal([]byte(lastChunk), &parsed)
	if parsed.Model != "workbuddy/stream-model" {
		t.Fatalf("last chunk model=%q, want workbuddy/stream-model", parsed.Model)
	}
}
