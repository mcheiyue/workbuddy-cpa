package wbexecutor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// --- doer helpers: wrap httptest server as func(*http.Request) (*http.Response, error) ---

func newDoer(handler http.HandlerFunc) func(*http.Request) (*http.Response, error) {
	ts := httptest.NewServer(handler)
	return func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = ts.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	}
}

func sseDoer(chunks ...string) func(*http.Request) (*http.Response, error) {
	return newDoer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			io.WriteString(w, "data: "+c+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	})
}

func errDoer(status int, body string) func(*http.Request) (*http.Response, error) {
	return newDoer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

// rawDoer returns a doer that writes raw JSON (non-SSE) — for non-stream Execute tests.
func rawDoer(body string) func(*http.Request) (*http.Response, error) {
	return newDoer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	})
}

func dummyReq(model string) wbexecutor.ExecuteRequest {
	payload, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	return wbexecutor.ExecuteRequest{
		AuthID:        "test-auth",
		PublicModelID: model,
		Payload:       payload,
		StreamID:      "stream-1",
		ChatBaseURL:   "http://unused",
		Cred:          wbexecutor.Credential{AccessToken: "tok", UID: "u1", Realm: "cn"},
	}
}

// --- Execute/ExecuteStream tests ---

func TestExecute_NonStream(t *testing.T) {
	resp := `{"id":"c1","model":"internal","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	cfg := wbexecutor.Config{Doer: rawDoer(resp)}
	result, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("public-m"))
	if execErr != nil {
		t.Fatal(execErr)
	}
	var obj map[string]any
	json.Unmarshal(result, &obj)
	if obj["model"] != "public-m" {
		t.Fatalf("model not restored: %v", obj["model"])
	}
}

func TestExecute_Upstream400(t *testing.T) {
	cfg := wbexecutor.Config{Doer: errDoer(400, `{"code":11102}`)}
	_, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	if execErr == nil || execErr.Kind != wbexecutor.ErrModelBlocked {
		t.Fatalf("expected model_blocked, got %v", execErr)
	}
}

func TestExecute_UnknownModel(t *testing.T) {
	cfg := wbexecutor.Config{Doer: sseDoer("{}"), Resolver: failResolver{}}
	_, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("bad"))
	if execErr == nil || execErr.Kind != wbexecutor.ErrClient {
		t.Fatalf("expected client err, got %v", execErr)
	}
}

func TestExecuteStream_NoSeams(t *testing.T) {
	err := wbexecutor.ExecuteStream(context.Background(), wbexecutor.Config{}, dummyReq("m"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecuteStream_NoStreamID(t *testing.T) {
	cfg := wbexecutor.Config{
		Doer:        sseDoer("{}"),
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
	}
	req := dummyReq("m")
	req.StreamID = ""
	if err := wbexecutor.ExecuteStream(context.Background(), cfg, req); err == nil {
		t.Fatal("expected error")
	}
}

func TestPumpStream_NoDataPrefix(t *testing.T) {
	emitted := [][]byte{}
	cfg := wbexecutor.Config{
		Doer:        sseDoer(`{"choices":[{"delta":{"content":"hi"}}]}`),
		StreamEmit:  func(_ string, p []byte) error { emitted = append(emitted, p); return nil },
		StreamClose: func(string, string) {},
	}
	if err := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("m")); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(emitted))
	}
	if bytes.HasPrefix(emitted[0], []byte("data: ")) {
		t.Fatalf("dual-layer SSE bug: %s", emitted[0])
	}
	if !json.Valid(emitted[0]) {
		t.Fatalf("not valid JSON: %s", emitted[0])
	}
}

func TestExecuteStream_BusinessError(t *testing.T) {
	cfg := wbexecutor.Config{
		Doer:        errDoer(200, `{"code":12153,"msg":"session dead"}`),
		StreamEmit:  func(string, []byte) error { return nil },
		StreamClose: func(string, string) {},
	}
	err := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("m"))
	if err == nil || err.Kind != wbexecutor.ErrSessionDead {
		t.Fatalf("expected session_dead, got %v", err)
	}
}

func TestSanitizeNoToken(t *testing.T) {
	cfg := wbexecutor.Config{Doer: errDoer(500, "Authorization: Bearer secret_tok_abc err")}
	_, execErr := wbexecutor.Execute(context.Background(), cfg, dummyReq("m"))
	if execErr == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(execErr.Msg, "secret_tok") {
		t.Fatalf("token leaked: %s", execErr.Msg)
	}
}

func TestCountTokens(t *testing.T) {
	if wbexecutor.CountTokens(nil) != 0 || wbexecutor.CountTokens([]byte("x")) != 1 || wbexecutor.CountTokens([]byte("12345678")) != 2 {
		t.Fatal("CountTokens wrong")
	}
}

type failResolver struct{}

func (failResolver) ResolveModel(_, _ string) (string, error) {
	return "", errors.New("unknown model bad")
}
