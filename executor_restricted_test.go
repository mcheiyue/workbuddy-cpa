package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestRestrictedFailurePersistsOnlyMatchingAccount(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain rejection", true: "explicit restriction"}[restricted], func(t *testing.T) {
			stored := json.RawMessage(`{"type":"workbuddy","auth":{"access_token":"fresh-token"},"priority":10,"note":"user note"}`)
			old := hostJSONCall
			t.Cleanup(func() { hostJSONCall = old })
			hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
				switch method {
				case pluginabi.MethodHostAuthList:
					return json.RawMessage(`{"files":[{"id":"other","provider":"qoder","auth_index":"other"},{"id":"selected","provider":"workbuddy","auth_index":"index"}]}`), nil
				case pluginabi.MethodHostAuthGet:
					if payload.(pluginapi.HostAuthGetRequest).AuthIndex != "index" {
						t.Fatal("wrong account")
					}
					return json.Marshal(pluginapi.HostAuthGetResponse{Name: "selected.json", JSON: stored})
				case pluginabi.MethodHostAuthSave:
					req := payload.(pluginapi.HostAuthSaveRequest)
					if req.Name != "selected.json" {
						t.Fatal("wrong file")
					}
					stored = append(json.RawMessage(nil), req.JSON...)
					return json.RawMessage(`{}`), nil
				default:
					t.Fatalf("unexpected method %s", method)
					return nil, nil
				}
			}
			hit := &wbexecutor.ExecError{Kind: wbexecutor.ErrAccountFault, Restricted: restricted, Msg: "upstream restriction"}
			req := rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{AuthID: "selected", StorageJSON: []byte(`{"auth":{"access_token":"stale-token"}}`)}}
			if err := recordExecutorFailure(req, hit); !errors.Is(err, hit) {
				t.Fatalf("lost upstream error: %v", err)
			}
			var state struct {
				Disabled bool
				Priority int
				Note     string
				Auth     struct {
					Token string `json:"access_token"`
				}
			}
			if err := json.Unmarshal(stored, &state); err != nil {
				t.Fatal(err)
			}
			if state.Disabled != restricted || state.Priority != 10 || state.Auth.Token != "fresh-token" {
				t.Fatalf("state=%+v", state)
			}
			if restricted && state.Note == "user note" {
				t.Fatal("restriction reason missing")
			}
			if !restricted && state.Note != "user note" {
				t.Fatal("plain rejection changed note")
			}
			// 保活仍走同一合并函数，不能抹掉停用态或用户字段。
			merged, err := mergeForeignAuthMetadata(stored, []byte(`{"type":"workbuddy","auth":{"access_token":"renewed-token"}}`))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(merged, &state); err != nil {
				t.Fatal(err)
			}
			if state.Disabled != restricted || state.Auth.Token != "renewed-token" {
				t.Fatalf("refresh state=%+v", state)
			}
		})
	}
}

func TestRestrictedFailureReportsPersistenceFailure(t *testing.T) {
	old := hostJSONCall
	t.Cleanup(func() { hostJSONCall = old })
	hostJSONCall = func(string, any) (json.RawMessage, error) { return nil, errors.New("host offline") }
	hit := &wbexecutor.ExecError{Restricted: true, Msg: "upstream restriction"}
	err := recordExecutorFailure(rpcExecutorRequest{ExecutorRequest: pluginapi.ExecutorRequest{AuthID: "selected"}}, hit)
	if err == nil || err == hit {
		t.Fatal("persistence failure must be reported")
	}
}
