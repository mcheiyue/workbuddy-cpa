package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/mcheiyue/workbuddy-cpa/internal/wbmodels"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestExecutorRestrictionReachesHostStorage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, status := range []int{200, 500} {
			t.Run(fmt.Sprintf("stream=%t/status=%d", stream, status), func(t *testing.T) {
				oldCall, oldRegistry, oldGate := hostJSONCall, defaultRegistry, sharedDegradeGate
				t.Cleanup(func() { hostJSONCall, defaultRegistry, sharedDegradeGate = oldCall, oldRegistry, oldGate })
				defaultRegistry = wbmodels.NewRegistry()
				defaultRegistry.Store("selected", map[string]string{"workbuddy/Auto": "auto"})
				sharedDegradeGate = wbexecutor.NewDegradeGate()
				stored := json.RawMessage(`{"type":"workbuddy","auth":{"accessToken":"test-token","realm":"cn"},"account":{"uid":"test-user"},"priority":10}`)
				restricted := []byte(`{"code":11140,"displayMsg":"Your account has been restricted due to unusual activity"}`)
				finished := make(chan string, 1)
				hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
					switch method {
					case pluginabi.MethodHostHTTPDo:
						return json.Marshal(hostHTTPResponse{StatusCode: status, Body: restricted})
					case pluginabi.MethodHostHTTPDoStream:
						return json.Marshal(hostHTTPStreamResponse{StatusCode: status, StreamID: "upstream"})
					case pluginabi.MethodHostHTTPStreamRead:
						return json.Marshal(hostHTTPStreamChunk{Payload: restricted, Done: true})
					case pluginabi.MethodHostHTTPStreamClose:
						return json.RawMessage(`{}`), nil
					case pluginabi.MethodHostStreamClose:
						finished <- payload.(map[string]any)["error"].(string)
						return json.RawMessage(`{}`), nil
					case pluginabi.MethodHostAuthList:
						return json.RawMessage(`{"files":[{"id":"selected","provider":"workbuddy","auth_index":"index"}]}`), nil
					case pluginabi.MethodHostAuthGet:
						return json.Marshal(pluginapi.HostAuthGetResponse{Name: "selected.json", JSON: stored})
					case pluginabi.MethodHostAuthSave:
						stored = append(json.RawMessage(nil), payload.(pluginapi.HostAuthSaveRequest).JSON...)
						return json.RawMessage(`{}`), nil
					default:
						return nil, fmt.Errorf("unexpected host method %s", method)
					}
				}
				req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
					AuthID: "selected", AuthProvider: "workbuddy", Model: "workbuddy/Auto", Format: "chat-completions",
					StorageJSON: append([]byte(nil), stored...), Payload: []byte(`{"model":"workbuddy/Auto","messages":[{"role":"user","content":"hello"}]}`),
				}, HostCallbackID: "callback", StreamID: "downstream"}
				raw, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				method := pluginabi.MethodExecutorExecute
				if stream {
					method = pluginabi.MethodExecutorExecuteStream
				}
				response, err := handleExecutorMethod(method, raw)
				if err != nil {
					t.Fatal(err)
				}
				if stream {
					select {
					case message := <-finished:
						if !strings.Contains(message, "11140") {
							t.Fatalf("stream error=%s", message)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("stream did not close")
					}
				} else if !strings.Contains(string(response), `"ok":false`) {
					t.Fatalf("response=%s", response)
				}
				var saved struct {
					Disabled bool
					Priority int
					Note     string
				}
				if err := json.Unmarshal(stored, &saved); err != nil {
					t.Fatal(err)
				}
				if !saved.Disabled || saved.Priority != 10 || !strings.Contains(saved.Note, "11140") {
					t.Fatalf("saved state=%+v", saved)
				}
			})
		}
	}
}

func TestExecutorGlobalQueueStreamCloseCarriesRequestScopedError(t *testing.T) {
	oldCall, oldRegistry, oldGate := hostJSONCall, defaultRegistry, sharedDegradeGate
	t.Cleanup(func() { hostJSONCall, defaultRegistry, sharedDegradeGate = oldCall, oldRegistry, oldGate })
	defaultRegistry = wbmodels.NewRegistry()
	defaultRegistry.Store("selected", map[string]string{"workbuddy/Auto": "auto"})
	sharedDegradeGate = wbexecutor.NewDegradeGate()

	closed := make(chan []byte, 1)
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPDoStream:
			return json.Marshal(hostHTTPStreamResponse{
				StatusCode: http.StatusTooManyRequests,
				Headers:    http.Header{"Retry-After": []string{"45"}},
				StreamID:   "upstream",
			})
		case pluginabi.MethodHostHTTPStreamRead:
			return json.Marshal(hostHTTPStreamChunk{
				Payload: []byte(`{"code":6020,"msg":"queue.waiting.title"}`),
				Done:    true,
			})
		case pluginabi.MethodHostHTTPStreamClose:
			return json.RawMessage(`{}`), nil
		case pluginabi.MethodHostStreamClose:
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			closed <- raw
			return json.RawMessage(`{}`), nil
		default:
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
	}

	request := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		AuthID: "selected", AuthProvider: "workbuddy", Model: "workbuddy/Auto", Format: "chat-completions",
		StorageJSON: []byte(`{"type":"workbuddy","auth":{"accessToken":"test-token","realm":"cn"},"account":{"uid":"test-user"}}`),
		Payload:     []byte(`{"model":"workbuddy/Auto","messages":[{"role":"user","content":"hello"}]}`),
	}, HostCallbackID: "callback", StreamID: "downstream"}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handleExecutorMethod(pluginabi.MethodExecutorExecuteStream, rawRequest); err != nil {
		t.Fatal(err)
	}

	select {
	case rawClose := <-closed:
		var closeRequest struct {
			Error       string           `json:"error"`
			ErrorDetail *pluginabi.Error `json:"error_detail"`
		}
		if err := json.Unmarshal(rawClose, &closeRequest); err != nil {
			t.Fatalf("decode stream close request: %v", err)
		}
		if !strings.HasPrefix(closeRequest.Error, "upstream global_queue (http 429): ") || !strings.Contains(closeRequest.Error, "queue.waiting") {
			t.Fatalf("error = %q, want original upstream error format", closeRequest.Error)
		}
		if closeRequest.ErrorDetail == nil || !closeRequest.ErrorDetail.IsRequestScoped() {
			t.Fatalf("error_detail = %+v, want request-scoped error", closeRequest.ErrorDetail)
		}
		if closeRequest.ErrorDetail.Message != closeRequest.Error {
			t.Errorf("error_detail.message = %q, want stream error %q", closeRequest.ErrorDetail.Message, closeRequest.Error)
		}
		if closeRequest.ErrorDetail.HTTPStatus != http.StatusTooManyRequests {
			t.Errorf("http_status = %d, want 429", closeRequest.ErrorDetail.HTTPStatus)
		}
		if closeRequest.ErrorDetail.RetryAfterSeconds != 45 {
			t.Errorf("retry_after_seconds = %d, want 45", closeRequest.ErrorDetail.RetryAfterSeconds)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close")
	}
}
