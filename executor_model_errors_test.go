package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/mcheiyue/workbuddy-cpa/internal/wbmodels"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestExecutorModelErrorsKeepRPCSemantics(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{"missing", `{"code":11102,"msg":"service info not found"}`, pluginabi.ErrorCodeRequestScoped},
		{"unsupported", `{"code":11158,"msg":"not allowed for user type"}`, pluginabi.ErrorCodeModelNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldCall, oldRegistry, oldGate := hostJSONCall, defaultRegistry, sharedDegradeGate
			t.Cleanup(func() { hostJSONCall, defaultRegistry, sharedDegradeGate = oldCall, oldRegistry, oldGate })
			defaultRegistry = wbmodels.NewRegistry()
			defaultRegistry.Store("selected", map[string]string{"workbuddy/Auto": "auto"})
			sharedDegradeGate = wbexecutor.NewDegradeGate()
			saved := []byte(`{"type":"workbuddy","auth":{"accessToken":"token","realm":"cn"},"account":{"uid":"user"}}`)
			var saveCalls atomic.Int32
			hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostHTTPDo:
					return json.Marshal(hostHTTPResponse{StatusCode: http.StatusBadRequest, Body: []byte(tc.body)})
				case pluginabi.MethodHostAuthList:
					return json.RawMessage(`{"files":[{"id":"selected","provider":"workbuddy","auth_index":"index"}]}`), nil
				case pluginabi.MethodHostAuthGet:
					return json.Marshal(pluginapi.HostAuthGetResponse{Name: "selected.json", JSON: saved})
				case pluginabi.MethodHostAuthSave:
					saveCalls.Add(1)
					return nil, errors.New("unexpected auth save")
				default:
					return nil, nil
				}
			}

			request := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				AuthID: "selected", AuthProvider: "workbuddy", Model: "workbuddy/Auto", Format: "chat-completions",
				StorageJSON: saved, Payload: []byte(`{"model":"workbuddy/Auto","messages":[{"role":"user","content":"hello"}]}`),
			}, HostCallbackID: "callback"}
			rawRequest, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			response, err := handleExecutorMethod(pluginabi.MethodExecutorExecute, rawRequest)
			if err != nil {
				t.Fatal(err)
			}
			var envelope pluginabi.Envelope
			if err := json.Unmarshal(response, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil || envelope.Error.Code != tc.code {
				t.Fatalf("error = %+v, want code %q", envelope.Error, tc.code)
			}
			if envelope.Error.HTTPStatus != http.StatusBadRequest {
				t.Errorf("http_status = %d, want %d", envelope.Error.HTTPStatus, http.StatusBadRequest)
			}
			if saveCalls.Load() != 0 {
				t.Fatalf("auth save calls = %d, want 0", saveCalls.Load())
			}
		})
	}
}

func TestExecutorModelErrorStreamIncludesStructuredDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"missing", http.StatusNotFound, `{"code":14401,"msg":"model not found"}`, pluginabi.ErrorCodeRequestScoped},
		{"unsupported", http.StatusForbidden, `{"error":{"code":"11158","message":"not allowed for user type"}}`, pluginabi.ErrorCodeModelNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldCall, oldRegistry, oldGate := hostJSONCall, defaultRegistry, sharedDegradeGate
			t.Cleanup(func() { hostJSONCall, defaultRegistry, sharedDegradeGate = oldCall, oldRegistry, oldGate })
			defaultRegistry = wbmodels.NewRegistry()
			defaultRegistry.Store("selected", map[string]string{"workbuddy/Auto": "auto"})
			sharedDegradeGate = wbexecutor.NewDegradeGate()

			type streamCloseResult struct {
				payload []byte
				err     error
			}
			closed := make(chan streamCloseResult, 1)
			var saveCalls atomic.Int32
			hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostHTTPDoStream:
					return json.Marshal(hostHTTPStreamResponse{StatusCode: tc.status, StreamID: "upstream"})
				case pluginabi.MethodHostHTTPStreamRead:
					return json.Marshal(hostHTTPStreamChunk{Payload: []byte(tc.body), Done: true})
				case pluginabi.MethodHostHTTPStreamClose:
					return json.RawMessage(`{}`), nil
				case pluginabi.MethodHostStreamClose:
					raw, err := json.Marshal(payload)
					closed <- streamCloseResult{payload: append([]byte(nil), raw...), err: err}
					if err != nil {
						return nil, err
					}
					return json.RawMessage(`{}`), nil
				case pluginabi.MethodHostAuthSave:
					saveCalls.Add(1)
					return nil, errors.New("unexpected auth save")
				default:
					return nil, nil
				}
			}

			request := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				AuthID: "selected", AuthProvider: "workbuddy", Model: "workbuddy/Auto", Format: "chat-completions",
				StorageJSON: []byte(`{"type":"workbuddy","auth":{"accessToken":"token","realm":"cn"},"account":{"uid":"user"}}`),
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
			case closeResult := <-closed:
				if closeResult.err != nil {
					t.Fatalf("encode stream close payload: %v", closeResult.err)
				}
				var closeRequest struct {
					Error       string           `json:"error"`
					ErrorDetail *pluginabi.Error `json:"error_detail"`
				}
				if err := json.Unmarshal(closeResult.payload, &closeRequest); err != nil {
					t.Fatal(err)
				}
				if closeRequest.ErrorDetail == nil || closeRequest.ErrorDetail.Code != tc.code {
					t.Fatalf("error_detail = %+v, want %q", closeRequest.ErrorDetail, tc.code)
				}
				if closeRequest.ErrorDetail.HTTPStatus != tc.status {
					t.Errorf("http_status = %d, want %d", closeRequest.ErrorDetail.HTTPStatus, tc.status)
				}
				if saveCalls.Load() != 0 {
					t.Errorf("auth save calls = %d, want 0", saveCalls.Load())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not close")
			}
		})
	}
}
