package wbexecutor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 锁：传输层失败不得累积账号连败——12 次连接失败后仍须每次都真的打上游。
// 对照（必要）：同样次数的 HTTP 500（ErrServer）必须仍在 degradeThreshold 次后
// fail-fast；否则「降权门根本没接上」也会让主断言通过（calls 恒等于 runs）。
func TestTransportFailureDoesNotTripAccountDegrade(t *testing.T) {
	const runs = 12
	req := ExecuteRequest{AuthID: "a1", PublicModelID: "m",
		Payload: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)}

	transportCalls := 0
	transportCfg := Config{Degrade: NewDegradeGate(),
		Doer: func(*http.Request) (*http.Response, error) {
			transportCalls++
			return nil, errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
		}}
	for i := 0; i < runs; i++ {
		if _, err := Execute(context.Background(), transportCfg, req); err == nil {
			t.Fatal("transport failure must surface as an error")
		}
	}
	if transportCalls != runs {
		t.Fatalf("transport calls=%d want %d: the account must not degrade on transport failures",
			transportCalls, runs)
	}

	serverCalls := 0
	serverCfg := Config{Degrade: NewDegradeGate(),
		Doer: func(*http.Request) (*http.Response, error) {
			serverCalls++
			return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{},
				Body: io.NopCloser(strings.NewReader(`{"code":500,"msg":"boom"}`))}, nil
		}}
	for i := 0; i < runs; i++ {
		if _, err := Execute(context.Background(), serverCfg, req); err == nil {
			t.Fatal("5xx must surface as an error")
		}
	}
	if serverCalls != degradeThreshold {
		t.Fatalf("5xx calls=%d want %d (control: the degrade gate must still trip)",
			serverCalls, degradeThreshold)
	}
}
