package wbexecutor_test

import (
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// ref workbuddy2api b262660b：11148 工具历史错配是请求级确定性错误
// （换号无法修复），必须与通用 ErrClient 分开，避免无意义轮换；
// 同时不进 degradeKnownKind（不罚号）。
func TestClassify_ToolCallMismatch(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   wbexecutor.ErrKind
	}{
		{400, `{"code":11148,"msg":"tool calls and tool results do not match"}`, wbexecutor.ErrToolCallMismatch},
		{400, `{"error":{"data":{"code":"11148","message":"invalid tool history"}}}`, wbexecutor.ErrToolCallMismatch},
		{400, `tool_call_sequence_broken`, wbexecutor.ErrToolCallMismatch},
		// requestId 里的 11148 不是业务 code，不得误判。
		{400, `{"requestId":"11148","msg":"bad request"}`, wbexecutor.ErrClient},
		// 仅 400 判请求级；5xx 仍是上游故障。
		{500, `{"code":11148,"msg":"tool calls and tool results do not match"}`, wbexecutor.ErrServer},
	}
	for _, tt := range tests {
		if got := wbexecutor.Classify(tt.status, tt.body); got != tt.want {
			t.Errorf("Classify(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
		}
	}
}
