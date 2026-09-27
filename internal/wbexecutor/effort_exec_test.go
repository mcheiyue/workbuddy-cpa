package wbexecutor_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

func effortRequest(model string) wbexecutor.ExecuteRequest {
	payload := []byte(`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`)
	return wbexecutor.ExecuteRequest{
		AuthID: "auth-effort", PublicModelID: model, Payload: payload,
		StreamID: "stream-effort", ChatBaseURL: "https://example.test",
		Cred: wbexecutor.Credential{AccessToken: "token", UID: "uid", Realm: "cn"},
	}
}

func TestExecute_NormalizesEffortAndKeepsValidJSON(t *testing.T) {
	var captured []byte
	cfg := wbexecutor.Config{
		Resolver: fixedResolver{internal: "long-internal-model"},
		Efforts:  func(string, string) []string { return []string{"high"} },
		Doer: func(req *http.Request) (*http.Response, error) {
			var err error
			captured, err = io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
				`{"id":"id","model":"long-internal-model","choices":[]}`,
			))}, nil
		},
	}
	if _, execErr := wbexecutor.Execute(context.Background(), cfg, effortRequest("public-model")); execErr != nil {
		t.Fatal(execErr)
	}
	var body map[string]any
	if err := json.Unmarshal(captured, &body); err != nil {
		t.Fatalf("upstream body is invalid JSON: %v; body=%s", err, captured)
	}
	if body["model"] != "long-internal-model" || body["reasoning_effort"] != "high" {
		t.Fatalf("normalized body=%s", captured)
	}
}

func TestExecuteStream_NormalizesEffort(t *testing.T) {
	var captured []byte
	cfg := wbexecutor.Config{
		Resolver: fixedResolver{internal: "internal-model"},
		Efforts:  func(string, string) []string { return []string{"high"} },
		StreamDoer: func(context.Context, string, string, http.Header, []byte) (wbexecutor.StreamHandle, error) {
			return wbexecutor.StreamHandle{
				StatusCode: 200,
				Read: func() ([]byte, bool, error) {
					return []byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"), true, nil
				},
				Close: func() {},
			}, nil
		},
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
	}
	originalStreamDoer := cfg.StreamDoer
	cfg.StreamDoer = func(ctx context.Context, method, url string, header http.Header, body []byte) (wbexecutor.StreamHandle, error) {
		captured = append([]byte(nil), body...)
		return originalStreamDoer(ctx, method, url, header, body)
	}
	if err := wbexecutor.ExecuteStream(context.Background(), cfg, effortRequest("public-model")); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(captured, &body); err != nil {
		t.Fatal(err)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v, want high", body["reasoning_effort"])
	}
}

type fixedResolver struct{ internal string }

func (r fixedResolver) ResolveModel(string, string) (string, error) { return r.internal, nil }
