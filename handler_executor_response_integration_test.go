package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestExecutorExecuteRPCSupportsDirectJSON(t *testing.T) {
	defaultRegistry.Store("auth-u-json", map[string]string{
		"workbuddy/json-model": "json-model",
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"json-1","object":"chat.completion","model":"json-model","choices":[{"index":0,"message":{"role":"assistant","content":"direct"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	orig := hostJSONCall
	t.Cleanup(func() { hostJSONCall = orig })
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		return mockHostHTTPDo(method, payload, upstream)
	}

	storageJSON := []byte(`{"auth":{"accessToken":"test-token","domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-json"}}`)
	payload := []byte(`{"model":"workbuddy/json-model","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	rawReq, err := json.Marshal(executorRequestHelper("workbuddy", "auth-u-json", "workbuddy/json-model", storageJSON, payload, ""))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := handleMethod(pluginabi.MethodExecutorExecute, rawReq)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("direct JSON must succeed, envelope: %s", raw)
	}
	var response pluginapi.ExecutorResponse
	if err := json.Unmarshal(env.Result, &response); err != nil {
		t.Fatal(err)
	}
	var completion struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Payload, &completion); err != nil {
		t.Fatal(err)
	}
	if completion.Model != "workbuddy/json-model" || len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "direct" {
		t.Fatalf("completion=%+v, want public model and direct content", completion)
	}
}

func TestExecutorExecuteStreamRPCClosesIncompleteSSEOnce(t *testing.T) {
	defaultRegistry.Store("auth-u-stream-incomplete", map[string]string{
		"workbuddy/stream-incomplete-model": "stream-incomplete-model",
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"stream-incomplete-1","object":"chat.completion.chunk","model":"stream-incomplete-model","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}

`)
	}))
	defer upstream.Close()

	var streamBody []byte
	var streamOffset int
	var downstreamCloseCount atomic.Int32
	var upstreamCloseCount atomic.Int32
	var downstreamClosed sync.Once
	var upstreamClosed sync.Once
	var downstreamClosePayload json.RawMessage
	downstreamFinished := make(chan struct{})
	upstreamFinished := make(chan struct{})
	oldCall := hostJSONCall
	t.Cleanup(func() { hostJSONCall = oldCall })
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostStreamEmit:
			return json.Marshal(struct{}{})
		case pluginabi.MethodHostStreamClose:
			downstreamCloseCount.Add(1)
			downstreamClosePayload, _ = json.Marshal(payload)
			downstreamClosed.Do(func() { close(downstreamFinished) })
			return json.Marshal(struct{}{})
		case pluginabi.MethodHostHTTPDoStream:
			raw, err := mockHostHTTPDoRaw(pluginabi.MethodHostHTTPDo, payload, upstream)
			if err != nil {
				return nil, err
			}
			var response hostHTTPResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				return nil, err
			}
			streamBody, streamOffset = response.Body, 0
			return json.Marshal(map[string]any{
				"status_code": response.StatusCode,
				"headers":     response.Headers,
				"stream_id":   "incomplete-upstream",
			})
		case pluginabi.MethodHostHTTPStreamRead:
			if streamOffset < len(streamBody) {
				next := bytes.Index(streamBody[streamOffset:], []byte("\n\n"))
				if next < 0 {
					next = len(streamBody) - streamOffset
				} else {
					next += 2
				}
				end := streamOffset + next
				payload := append([]byte(nil), streamBody[streamOffset:end]...)
				streamOffset = end
				return json.Marshal(map[string]any{"payload": payload})
			}
			return json.Marshal(map[string]any{"done": true})
		case pluginabi.MethodHostHTTPStreamClose:
			upstreamCloseCount.Add(1)
			upstreamClosed.Do(func() { close(upstreamFinished) })
			return json.Marshal(struct{}{})
		default:
			return nil, fmt.Errorf("unexpected host method: %s", method)
		}
	}

	storageJSON := []byte(`{"auth":{"accessToken":"test-token","domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-stream-incomplete"}}`)
	payload := []byte(`{"model":"workbuddy/stream-incomplete-model","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	rawReq, err := json.Marshal(executorStreamRequestHelper("workbuddy", "auth-u-stream-incomplete", "workbuddy/stream-incomplete-model", storageJSON, payload, "stream-incomplete"))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := handleMethod(pluginabi.MethodExecutorExecuteStream, rawReq)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("stream start must succeed before asynchronous failure, envelope: %s", raw)
	}
	select {
	case <-downstreamFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("incomplete stream did not close downstream")
	}
	select {
	case <-upstreamFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("incomplete stream did not close upstream")
	}
	if got := downstreamCloseCount.Load(); got != 1 {
		t.Fatalf("downstream close calls=%d, want 1", got)
	}
	if got := upstreamCloseCount.Load(); got != 1 {
		t.Fatalf("upstream close calls=%d, want 1", got)
	}
	var closeRequest struct {
		Error       string           `json:"error"`
		ErrorDetail *pluginabi.Error `json:"error_detail"`
	}
	if err := json.Unmarshal(downstreamClosePayload, &closeRequest); err != nil {
		t.Fatal(err)
	}
	if closeRequest.Error != "upstream stream_truncated (http 502): stream ended without terminal event" {
		t.Fatalf("close error=%q, want structured stream failure", closeRequest.Error)
	}
	if closeRequest.ErrorDetail == nil {
		t.Fatal("close error detail is missing")
	}
	if closeRequest.ErrorDetail.Code != pluginabi.ErrorCodeStreamTruncated {
		t.Fatalf("close error code=%q, want %q", closeRequest.ErrorDetail.Code, pluginabi.ErrorCodeStreamTruncated)
	}
	if closeRequest.ErrorDetail.Message != closeRequest.Error {
		t.Fatalf("close error detail message=%q, want close error", closeRequest.ErrorDetail.Message)
	}
	if closeRequest.ErrorDetail.HTTPStatus != http.StatusBadGateway {
		t.Fatalf("close error status=%d, want HTTP 502", closeRequest.ErrorDetail.HTTPStatus)
	}
}

func TestExecutorExecuteRPCRejectsIncompleteSSE(t *testing.T) {
	defaultRegistry.Store("auth-u-incomplete", map[string]string{
		"workbuddy/incomplete-model": "incomplete-model",
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"incomplete-1\",\"object\":\"chat.completion\",\"model\":\"incomplete-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
	}))
	defer upstream.Close()
	var authSaveCalls atomic.Int32
	orig := hostJSONCall
	t.Cleanup(func() { hostJSONCall = orig })
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		if method == pluginabi.MethodHostAuthSave {
			authSaveCalls.Add(1)
			return json.Marshal(struct{}{})
		}
		return mockHostHTTPDo(method, payload, upstream)
	}

	storageJSON := []byte(`{"auth":{"accessToken":"test-token","refreshToken":"rt","expiresAt":9999999999,"domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-incomplete","nickname":"tester"}}`)
	payload := []byte(`{"model":"workbuddy/incomplete-model","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	rawReq, err := json.Marshal(executorRequestHelper("workbuddy", "auth-u-incomplete", "workbuddy/incomplete-model", storageJSON, payload, ""))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := handleMethod(pluginabi.MethodExecutorExecute, rawReq)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatalf("incomplete SSE must fail, got success envelope: %s", raw)
	}
	if env.Result != nil {
		t.Fatalf("failed RPC must not include result payload: %s", raw)
	}
	if env.Error == nil || env.Error.HTTPStatus != http.StatusBadGateway {
		t.Fatalf("error=%+v, want HTTP 502", env.Error)
	}
	if env.Error.Code != pluginabi.ErrorCodeStreamTruncated {
		t.Fatalf("error code=%q, want %q", env.Error.Code, pluginabi.ErrorCodeStreamTruncated)
	}
	if got := authSaveCalls.Load(); got != 0 {
		t.Fatalf("auth save calls=%d, want 0", got)
	}
}
