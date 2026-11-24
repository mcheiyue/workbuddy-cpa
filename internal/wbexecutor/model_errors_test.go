package wbexecutor_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

func TestClassify_ModelErrorsAreDistinct(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   wbexecutor.ErrKind
	}{
		{"numeric missing code", http.StatusBadRequest, `{"code":11102}`, wbexecutor.ErrModelMissing},
		{"string missing code", http.StatusNotFound, `{"error":{"code":"14401","message":"model not found"}}`, wbexecutor.ErrModelMissing},
		{"missing message", http.StatusBadRequest, `{"error":{"message":"service info not found"}}`, wbexecutor.ErrModelMissing},
		{"display message", http.StatusNotFound, `{"error":{"displayMsg":"service info not found"}}`, wbexecutor.ErrModelMissing},
		{"permission code wins", http.StatusForbidden, `{"code":11158,"msg":"model not found: not allowed for user type"}`, wbexecutor.ErrModelBlocked},
		{"permission message", http.StatusForbidden, `{"error":{"message":"This model is not allowed for user type"}}`, wbexecutor.ErrModelBlocked},
		{"permission code with unauthorized session", http.StatusUnauthorized, `{"code":11158,"msg":"12153 Offline user session not found"}`, wbexecutor.ErrSessionDead},
		{"permission-like success message", http.StatusOK, `{"msg":"model is not allowed for user type"}`, wbexecutor.ErrNone},
		{"unknown forbidden code", http.StatusForbidden, `{"code":99999,"msg":"permission denied"}`, wbexecutor.ErrClient},
		{"unknown missing code", http.StatusNotFound, `{"code":99999,"msg":"resource unavailable"}`, wbexecutor.ErrClient},
		{"permission code on rate limit", http.StatusTooManyRequests, `{"code":11158,"msg":"rate limit"}`, wbexecutor.ErrSoftRate},
		{"explicit missing code with literal service info", http.StatusBadRequest, `{"code":11158,"msg":"service info not found"}`, wbexecutor.ErrModelBlocked},
		{"request id is not code", http.StatusBadRequest, `{"requestId":"11102","msg":"bad request"}`, wbexecutor.ErrClient},
		{"unrelated code with missing text", http.StatusBadRequest, `{"code":99999,"msg":"model not found"}`, wbexecutor.ErrClient},
		{"outer code is not combined with nested model code", http.StatusBadRequest, `{"code":99999,"error":{"code":11102}}`, wbexecutor.ErrClient},
		{"outer code is not combined with nested model message", http.StatusBadRequest, `{"code":99999,"error":{"message":"service info not found"}}`, wbexecutor.ErrClient},
		{"outer code is not combined with nested permission code", http.StatusForbidden, `{"code":99999,"error":{"code":11158}}`, wbexecutor.ErrClient},
		{"rate limit code takes priority", http.StatusTooManyRequests, `{"code":11102,"msg":"rate limit"}`, wbexecutor.ErrSoftRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wbexecutor.Classify(tt.status, tt.body); got != tt.want {
				t.Fatalf("Classify(%d, %s) = %v, want %v", tt.status, tt.body, got, tt.want)
			}
		})
	}
}

func TestDegrade_ModelErrorsDoNotCoolOtherModels(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing", `{"code":11102,"msg":"service info not found"}`},
		{"unsupported", `{"code":11158,"msg":"not allowed for user type"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := wbexecutor.NewDegradeGate()
			cfg := wbexecutor.Config{Doer: errDoer(http.StatusBadRequest, tc.body), Degrade: gate}
			for range 6 {
				if _, err := wbexecutor.Execute(context.Background(), cfg, dummyReq("model-a")); err == nil {
					t.Fatal("expected model error")
				}
			}

			calls := 0
			healthy := wbexecutor.Config{Doer: countingDoer(&calls), Degrade: gate}
			if _, err := wbexecutor.Execute(context.Background(), healthy, dummyReq("model-b")); err != nil {
				t.Fatalf("other model was blocked: %v", err)
			}
			if calls != 1 {
				t.Fatalf("healthy model reached upstream %d times, want 1", calls)
			}
		})
	}
}
