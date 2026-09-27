package wbexecutor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// E2 A3b（流路径）：pumpStream 把 tool_calls 帧缓存到 finish/[DONE] 再决策——
// finish_reason=length 时剔除半截 arguments（ref truncation 语义），其余场景完整放行。
// 断言用「客户端式聚合」（按 index 拼接 arguments），与帧形状（原始分片/合并单帧）无关。

type pumpResult struct {
	emitted      [][]byte
	closeReasons []string
}

func (r *pumpResult) run(t *testing.T, sse string) *wbexecutor.ExecError {
	t.Helper()
	cfg := wbexecutor.Config{
		StreamDoer: streamDoerOf(200, sse),
		StreamEmit: func(_ string, p []byte) error {
			r.emitted = append(r.emitted, p)
			return nil
		},
		StreamClose: func(_ string, reason string) {
			r.closeReasons = append(r.closeReasons, reason)
		},
	}
	return wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("m"))
}

// aggregatePump 按客户端视角聚合所有已发帧：index→arguments 拼接、finish、首帧位置。
// 返回 (args, finishReason, 首个 tool_calls 帧下标, 首个非空 finish 帧下标)。
func aggregatePump(t *testing.T, emitted [][]byte) (map[int]string, string, int, int) {
	t.Helper()
	args := map[int]string{}
	fr := ""
	tcPos := -1
	finishPos := -1
	for i, ch := range emitted {
		var obj struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Index    int `json:"index"`
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason any `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(ch, &obj); err != nil {
			t.Fatalf("emitted[%d] not JSON: %v: %s", i, err, ch)
		}
		for _, c := range obj.Choices {
			if s, ok := c.FinishReason.(string); ok && s != "" {
				fr = s
				if finishPos < 0 {
					finishPos = i
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				if tcPos < 0 {
					tcPos = i
				}
				args[tc.Index] += tc.Function.Arguments
			}
		}
	}
	return args, fr, tcPos, finishPos
}

// 正常结束：完整 arguments 聚合正确，且 tool_calls 帧先于 finish 帧（决策后 flush）。
func TestPumpStream_StopKeepsMergedToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"get","arguments":""}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"beijing\"}"}},{"index":1,"id":"call2","type":"function","function":{"name":"ping","arguments":"{}"}}]}, "finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	res := &pumpResult{}
	if execErr := res.run(t, sse); execErr != nil {
		t.Fatalf("unexpected error: %+v", execErr)
	}
	if len(res.closeReasons) != 1 || res.closeReasons[0] != "" {
		t.Fatalf("want clean close, got %v", res.closeReasons)
	}
	args, fr, tcPos, finishPos := aggregatePump(t, res.emitted)
	if fr != "stop" {
		t.Fatalf("finish=%q, want stop", fr)
	}
	if args[0] != `{"city":"beijing"}` {
		t.Fatalf("index0 args=%q, want merged full", args[0])
	}
	if args[1] != `{}` {
		t.Fatalf("index1 args=%q, want {}", args[1])
	}
	if tcPos < 0 || finishPos < 0 || tcPos >= finishPos {
		t.Fatalf("tool_calls must be emitted before finish: tc=%d finish=%d", tcPos, finishPos)
	}
}

// length 且半截 arguments：整条剔除——所有已发帧不含任何 tool_calls，finish=length 仍在。
func TestPumpStream_LengthDropsTruncatedToolCall(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"f","arguments":"{\"q\":"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"half"}}]}, "finish_reason":"length"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	res := &pumpResult{}
	if execErr := res.run(t, sse); execErr != nil {
		t.Fatalf("unexpected error: %+v", execErr)
	}
	args, fr, _, _ := aggregatePump(t, res.emitted)
	if fr != "length" {
		t.Fatalf("finish=%q, want length", fr)
	}
	if len(args) != 0 {
		t.Fatalf("truncated tool_call must not be emitted, got %v", args)
	}
}

// length 但 arguments 完整：保留（只剔截断的）。
func TestPumpStream_LengthKeepsValidToolCall(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{}, "finish_reason":"length"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	res := &pumpResult{}
	if execErr := res.run(t, sse); execErr != nil {
		t.Fatalf("unexpected error: %+v", execErr)
	}
	args, fr, _, _ := aggregatePump(t, res.emitted)
	if fr != "length" {
		t.Fatalf("finish=%q, want length", fr)
	}
	if args[0] != `{}` {
		t.Fatalf("valid tool_call must survive length, got %v", args)
	}
}

// 没有 finish 帧、直接 [DONE]：缓存的 tool_calls 放行。
func TestPumpStream_DoneWithoutFinishFlushesToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]}}]}` + "\n\n" +
		"data: [DONE]\n\n"
	res := &pumpResult{}
	if execErr := res.run(t, sse); execErr != nil {
		t.Fatalf("unexpected error: %+v", execErr)
	}
	args, fr, _, _ := aggregatePump(t, res.emitted)
	if fr != "" {
		t.Fatalf("finish=%q, want none", fr)
	}
	if args[0] != `{"a":1}` {
		t.Fatalf("held tool_calls must flush on [DONE], got %v", args)
	}
}

// 无 [DONE] 断流：沿用既有错误关闭，且缓存的（可能截断的）tool_calls 不外发。
func TestPumpStream_EofWithoutDoneDiscardsHeldToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"f","arguments":"{\"q\":"}}]}}]}` + "\n\n"
	res := &pumpResult{}
	if execErr := res.run(t, sse); execErr == nil {
		t.Fatal("expected terminal-missing error")
	}
	if len(res.closeReasons) != 1 || res.closeReasons[0] == "" {
		t.Fatalf("want error close reason, got %v", res.closeReasons)
	}
	args, _, _, _ := aggregatePump(t, res.emitted)
	if len(args) != 0 {
		t.Fatalf("held tool_calls must be discarded on broken stream, got %v", args)
	}
}
