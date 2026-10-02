package wbexecutor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// E4：usage 扩展元数据（billable / cacheable_tokens / firstTokenDuration /
// totalDuration / serverDuration）的接收/缺失回归——观察性批次，生产零改动。
//
// 字段出处说明：该 5 字段清单源自 Orchids 698e8c63（fix(qoder)，qoder 通道
// usage/finish 帧），workbuddy 上游无实证发送（ref parseSSELine 与现网抓帧均无）。
// 本批次锁两条边界：
//   1. 接收回归——上游若发送这些字段，解析不得破坏 credit 记账（未知字段容忍），
//      NormalizeChunk 须原样透传（观测可见，不吞）；
//   2. 缺失回归——只有这些元数据、无 credit 时绝不记账（缺失≠0，不伪造成本），
//      且插件从不凭空产出时延（OnUsage 契约只有 credit/tokens，无时延通道）。

// usageExtendedPayload 模拟上游/网关附加元数据的非流式响应。
const usageExtendedPayload = `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
	`"usage":{"credit":1.5,"total_tokens":900,"prompt_tokens":600,"completion_tokens":300,` +
	`"billable":true,"cacheable_tokens":128,"firstTokenDuration":420,"totalDuration":3500,"serverDuration":3100}}`

// usageMetadataOnlyPayload 只带扩展元数据、无 credit 的响应。
const usageMetadataOnlyPayload = `{"choices":[],` +
	`"usage":{"total_tokens":900,"prompt_tokens":600,"completion_tokens":300,` +
	`"billable":false,"cacheable_tokens":128,"firstTokenDuration":420,"totalDuration":3500,"serverDuration":3100}}`

// 接收回归：扩展元数据在场时 credit/tokens 记账逐字节不受影响。
func TestExecute_NonStreamUsageExtendedMetadataNoted(t *testing.T) {
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{Doer: rawDoer(usageExtendedPayload), OnUsage: onUsage}
	if _, err := wbexecutor.Execute(context.Background(), cfg, dummyReq("public-m4")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls=%d want 1 (unknown metadata must not break noting)", len(*calls))
	}
	got := (*calls)[0]
	if got.credit != 1.5 || got.tokens != 900 || got.model != "public-m4" {
		t.Fatalf("got %+v want {credit 1.5 tokens 900}", got)
	}
}

// 缺失回归：只有扩展元数据、无 credit → 不记账（不伪造成本）。
func TestExecute_NonStreamUsageMetadataOnlyNotNoted(t *testing.T) {
	calls, onUsage := captureUsage()
	cfg := wbexecutor.Config{Doer: rawDoer(usageMetadataOnlyPayload), OnUsage: onUsage}
	if _, err := wbexecutor.Execute(context.Background(), cfg, dummyReq("m4")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("metadata without credit must not be noted, got %+v", *calls)
	}
}

// 接收回归：NormalizeChunk 对带扩展元数据的末帧 usage 原样透传（观测可见），
// 仅注入公开 model ID；绝不伪造/删除上游真实字段。
func TestNormalizeChunk_UsageExtendedPassthrough(t *testing.T) {
	raw := `{"choices":[{"delta":{},"finish_reason":"stop","index":0}],"id":"c4","model":"internal-key",` +
		`"object":"chat.completion.chunk","usage":{"credit":1.5,"total_tokens":900,` +
		`"billable":true,"cacheable_tokens":128,"firstTokenDuration":420,"totalDuration":3500,"serverDuration":3100}}`
	out := wbexecutor.NormalizeChunk([]byte(raw), "public-4")
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("normalize output invalid: %v", err)
	}
	if obj["model"] != "public-4" {
		t.Fatalf("model=%v want public-4", obj["model"])
	}
	usage, ok := obj["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage dropped: %s", out)
	}
	want := map[string]any{
		"credit":             1.5,
		"total_tokens":       float64(900),
		"billable":           true,
		"cacheable_tokens":   float64(128),
		"firstTokenDuration": float64(420),
		"totalDuration":      float64(3500),
		"serverDuration":     float64(3100),
	}
	for k, w := range want {
		if usage[k] != w {
			t.Errorf("usage[%s]=%v want %v (must pass through verbatim)", k, usage[k], w)
		}
	}
}

// 流式接收回归：末帧带扩展元数据时 OnUsage 记账不受影响，且帧透传到客户端。
func TestExecuteStream_UsageExtendedMetadataNoted(t *testing.T) {
	body := `data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"credit":2.0,"total_tokens":500,` +
		`"billable":true,"firstTokenDuration":100,"totalDuration":900,"serverDuration":800}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	calls, onUsage := captureUsage()
	var emitted []string
	cfg := wbexecutor.Config{
		StreamDoer:  streamDoerOf(200, body),
		StreamEmit:  func(_ string, b []byte) error { emitted = append(emitted, string(b)); return nil },
		StreamClose: func(string, string) {},
		OnUsage:     onUsage,
	}
	if err := wbexecutor.ExecuteStream(context.Background(), cfg, dummyReq("public-s4")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].credit != 2.0 || (*calls)[0].tokens != 500 {
		t.Fatalf("calls=%+v want one {2.0 500}", *calls)
	}
	found := false
	for _, frame := range emitted {
		if len(frame) > 0 && json.Valid([]byte(frame)) {
			var obj map[string]any
			if json.Unmarshal([]byte(frame), &obj) == nil {
				if u, ok := obj["usage"].(map[string]any); ok {
					if v, ok := u["firstTokenDuration"]; ok && v == float64(100) {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Errorf("extended usage metadata frame not passed through to client, frames=%d", len(emitted))
	}
}
