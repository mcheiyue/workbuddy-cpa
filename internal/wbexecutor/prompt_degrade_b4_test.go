package wbexecutor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRewriteSystemNeutralDropsSystemAndDeveloper(t *testing.T) {
	in := []byte(`{"model":"m","messages":[` +
		`{"role":"system","content":"You are an AI agent powered by DeepSeek Harness."},` +
		`{"role":"developer","content":"dev"},` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":"yo"}]}`)
	out := rewriteSystemNeutral(in)
	var obj struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal rewritten payload: %v", err)
	}
	if len(obj.Messages) != 3 {
		t.Fatalf("messages=%d want 3 (system+developer replaced by one neutral)", len(obj.Messages))
	}
	if obj.Messages[0].Role != "system" || obj.Messages[0].Content != neutralSystemPrompt {
		t.Fatalf("first message=%+v want the neutral system prompt", obj.Messages[0])
	}
	if obj.Messages[1].Content != "hi" || obj.Messages[2].Content != "yo" {
		t.Fatalf("non-system messages must survive verbatim, got %+v", obj.Messages[1:])
	}
	if !strings.Contains(string(out), `"model":"m"`) {
		t.Fatalf("other top-level keys must be preserved, got %s", out)
	}
}

func TestRewriteSystemNeutralLeavesUnusablePayloadAlone(t *testing.T) {
	if got := string(rewriteSystemNeutral([]byte("not json"))); got != "not json" {
		t.Fatalf("unparsable payload must be returned as-is, got %q", got)
	}
	noMessages := []byte(`{"model":"m"}`)
	if got := string(rewriteSystemNeutral(noMessages)); got != string(noMessages) {
		t.Fatalf("payload without messages must be returned as-is, got %q", got)
	}
}

func TestNextMidnightCSTIsNextDay(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 10, 27, 23, 59, 0, 0, loc)
	want := time.Date(2026, 10, 28, 0, 0, 0, 0, loc)
	if got := nextMidnightCST(now); !got.Equal(want) {
		t.Fatalf("nextMidnightCST=%v want %v", got, want)
	}
}

// 锁：只有内容/渠道拦截才触发提示词层降级。账号级故障与额度过错换号有用，
// 改提示词只会掩盖真实原因（现网 11128 是前者，14018/12153 是后者）。
func TestDegradeTriggerKindIsContentBlockedOnly(t *testing.T) {
	if !degradeTriggerKind(ErrContentBlocked) {
		t.Fatal("content blocked must trigger the prompt-layer degrade")
	}
	for _, k := range []ErrKind{ErrAccountFault, ErrBadParams, ErrHardCredit, ErrTransport, ErrNone} {
		if degradeTriggerKind(k) {
			t.Fatalf("%v must not trigger the prompt-layer degrade", k)
		}
	}
}
