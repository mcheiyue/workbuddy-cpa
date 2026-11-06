package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestAuthParseKeepsRestrictionThroughHostStorage(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		name := "disabled"
		if !disabled {
			name = "manually-enabled"
		}
		t.Run(name, func(t *testing.T) {
			original := map[string]any{"type": "workbuddy", "auth": map[string]string{"accessToken": "test", "realm": "global"}, "account": map[string]string{"uid": "u"}, "disabled": disabled, "note": restrictedAccountReason, "priority": 10, "user_field": "keep"}
			stored, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(pluginapi.AuthParseRequest{Provider: "workbuddy", FileName: "account.json", RawJSON: stored})
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := handleAuthParse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Auth.Disabled != disabled {
				t.Fatalf("parsed disabled=%v want=%v", parsed.Auth.Disabled, disabled)
			}
			if parsed.Auth.Metadata["note"] != restrictedAccountReason {
				t.Fatal("host metadata lost restriction reason after file reload")
			}
			var result map[string]any
			if err := json.Unmarshal(parsed.Auth.StorageJSON, &result); err != nil {
				t.Fatal(err)
			}
			if result["note"] != restrictedAccountReason || result["disabled"] != disabled || result["user_field"] != "keep" || result["priority"] != float64(10) {
				t.Fatalf("storage metadata changed after parse: %v", result)
			}
		})
	}
}

func TestAuthRefreshKeepsRestrictionWithRotatedTokens(t *testing.T) {
	oldCall, oldOps := hostJSONCall, ops
	ops = &opsTicker{now: time.Now, listAccountsFn: func() []accountInfo { return nil }}
	t.Cleanup(func() { ops.stop(); hostJSONCall, ops = oldCall, oldOps })
	hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostHTTPDo {
			return nil, fmt.Errorf("unexpected method %s", method)
		}
		return json.Marshal(hostHTTPResponse{StatusCode: 200, Body: []byte(`{"code":0,"data":{"accessToken":"rotated-access","refreshToken":"rotated-refresh","expiresIn":3600}}`)})
	}
	stored := []byte(`{"type":"workbuddy","auth":{"accessToken":"old-access","refreshToken":"old-refresh","realm":"global"},"account":{"uid":"u"},"disabled":true,"note":"restriction reason","priority":10}`)
	raw, err := json.Marshal(rpcAuthRefreshRequest{AuthRefreshRequest: pluginapi.AuthRefreshRequest{AuthID: "u", StorageJSON: stored}, HostCallbackID: "callback"})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := handleAuthRefresh(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Disabled bool
		Note     string
		Priority int
		Auth     struct {
			AccessToken  string
			RefreshToken string
		}
	}
	if err := json.Unmarshal(refreshed.Auth.StorageJSON, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Disabled || result.Note != "restriction reason" || result.Priority != 10 || result.Auth.AccessToken != "rotated-access" || result.Auth.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh dropped state or tokens: %+v", result)
	}
}
