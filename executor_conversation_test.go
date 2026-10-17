package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// TestDeriveConversationID 锁：出站必须永远带非空 X-Conversation-ID。
// 2026-10-17 现网探针实证：缺该头时上游 100% 返回 11128 request illegal
// （displayMsg「请求被拦截，请重新发送」），带任意非空值即 200。
// 因此这里是「绝不返回空串」的契约，而非「尽量取到头」的偏好。
func TestDeriveConversationID(t *testing.T) {
	t.Run("X-Conversation-ID wins", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Conversation-ID", "conv-1")
		h.Set("X-Session-ID", "sess-1")
		if got := deriveConversationID(h, nil); got != "conv-1" {
			t.Fatalf("got %q, want conv-1", got)
		}
	})

	t.Run("falls back to X-Session-ID", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Session-ID", "sess-1")
		if got := deriveConversationID(h, nil); got != "sess-1" {
			t.Fatalf("got %q, want sess-1", got)
		}
	})

	t.Run("falls back to lowercase x-session-id", func(t *testing.T) {
		h := http.Header{}
		h.Set("x-session-id", "sess-2")
		if got := deriveConversationID(h, nil); got != "sess-2" {
			t.Fatalf("got %q, want sess-2", got)
		}
	})

	t.Run("falls back to Metadata conversation_id", func(t *testing.T) {
		if got := deriveConversationID(http.Header{}, map[string]any{"conversation_id": "conv-md"}); got != "conv-md" {
			t.Fatalf("got %q, want conv-md", got)
		}
	})

	t.Run("generates when nothing available", func(t *testing.T) {
		first := deriveConversationID(http.Header{}, nil)
		second := deriveConversationID(http.Header{}, nil)
		if first == "" || second == "" {
			t.Fatalf("must never be empty: first=%q second=%q", first, second)
		}
		if first == second {
			t.Fatalf("per-request generated ids must differ, both %q", first)
		}
		if len(first) != 36 {
			t.Fatalf("generated id must be 36-char UUID form, got %q (%d)", first, len(first))
		}
	})

	t.Run("empty header values are ignored", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Conversation-ID", "")
		h.Set("X-Session-ID", "sess-3")
		if got := deriveConversationID(h, nil); got != "sess-3" {
			t.Fatalf("got %q, want sess-3", got)
		}
	})
}

// TestExecutorWireConversationID 锁：executor 出站线上必须出现非空
// X-Conversation-ID，且当客户端只给 X-Session-ID 时以它为值。
// 这条锁住的是「宿主到插件再到上游」的整条转发链——单测 deriveConversationID
// 通过但 buildExecuteRequest 忘记接线时，必须红。
func TestExecutorWireConversationID(t *testing.T) {
	cases := []struct {
		name        string
		headers     http.Header
		metadata    map[string]any
		wantExact   string
		wantAnyBool bool
	}{
		{
			name:      "client sends X-Conversation-ID",
			headers:   http.Header{"X-Conversation-Id": []string{"conv-wire"}},
			wantExact: "conv-wire",
		},
		{
			name:      "client sends only X-Session-ID",
			headers:   http.Header{"X-Session-Id": []string{"ses_wire_1"}},
			wantExact: "ses_wire_1",
		},
		{
			name:        "client sends nothing",
			headers:     http.Header{},
			wantAnyBool: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defaultRegistry.Store("auth-conv", map[string]string{
				"workbuddy/conv-model": "conv-model",
			})

			var seen string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Get("X-Conversation-ID")
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion\",\"model\":\"conv-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"cmpl-1\",\"object\":\"chat.completion\",\"model\":\"conv-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer upstream.Close()

			orig := hostJSONCall
			defer func() { hostJSONCall = orig }()
			hostJSONCall = func(method string, payload any) (json.RawMessage, error) {
				return mockHostHTTPDo(method, payload, upstream)
			}

			storageJSON := []byte(`{"auth":{"accessToken":"test-token","refreshToken":"rt","expiresAt":9999999999,"domain":"www.codebuddy.cn","realm":"cn"},"account":{"uid":"u-conv","nickname":"tester"}}`)
			payload := []byte(`{"model":"workbuddy/conv-model","messages":[{"role":"user","content":"hi"}],"stream":false}`)
			rpc := rpcExecutorRequest{
				ExecutorRequest: pluginapi.ExecutorRequest{
					AuthProvider: "workbuddy",
					AuthID:       "auth-conv",
					Model:        "workbuddy/conv-model",
					StorageJSON:  storageJSON,
					Payload:      payload,
					Headers:      tc.headers,
					Metadata:     tc.metadata,
				},
				HostCallbackID: "test-callback",
			}
			rawReq, _ := json.Marshal(rpc)

			raw, err := handleMethod(pluginabi.MethodExecutorExecute, rawReq)
			if err != nil {
				t.Fatal(err)
			}
			var env struct {
				OK bool `json:"ok"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatal(err)
			}
			if !env.OK {
				t.Fatalf("expected ok=true, envelope: %s", raw)
			}
			if tc.wantExact != "" && seen != tc.wantExact {
				t.Fatalf("upstream saw X-Conversation-ID=%q, want %q", seen, tc.wantExact)
			}
			if tc.wantAnyBool && seen == "" {
				t.Fatalf("upstream saw empty X-Conversation-ID; must never be omitted")
			}
		})
	}
}
