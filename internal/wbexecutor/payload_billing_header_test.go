package wbexecutor_test

import (
	"encoding/json"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

func preparePayloadMessages(t *testing.T, src string) []json.RawMessage {
	t.Helper()
	var payload struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(wbexecutor.PreparePayload([]byte(src)), &payload); err != nil {
		t.Fatalf("decode prepared payload: %v", err)
	}
	return payload.Messages
}

func TestPreparePayload_RemovesBillingHeaderFromIdentityText(t *testing.T) {
	src := `{"messages":[
		{"role":"system","content":"  x-anthropic-billing-header: cc_version=2.1.321.abc; cc_entrypoint=cli; cch=00000;\nKeep system."},
		{"role":"assistant","content":[{"type":"text","text":"X-ANTHROPIC-BILLING-HDR: cc_version=2.1.321.abc; cch=00000;\nKeep assistant.","cache_control":{"type":"ephemeral"}}]},
		{"role":"user","content":"x-anthropic-billing-header: preserve user text"}
	]}`
	messages := preparePayloadMessages(t, src)
	if len(messages) != 3 {
		t.Fatalf("messages=%d, want 3", len(messages))
	}
	var systemText, userText string
	var assistantBlocks []struct {
		Type         string            `json:"type"`
		Text         string            `json:"text"`
		CacheControl map[string]string `json:"cache_control"`
	}
	for _, raw := range messages {
		var message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		switch message.Role {
		case "system":
			if err := json.Unmarshal(message.Content, &systemText); err != nil {
				t.Fatal(err)
			}
		case "assistant":
			if err := json.Unmarshal(message.Content, &assistantBlocks); err != nil {
				t.Fatal(err)
			}
		case "user":
			if err := json.Unmarshal(message.Content, &userText); err != nil {
				t.Fatal(err)
			}
		}
	}
	if systemText != "Keep system." {
		t.Fatalf("system text=%q", systemText)
	}
	if len(assistantBlocks) != 1 || assistantBlocks[0].Text != "Keep assistant." {
		t.Fatalf("assistant blocks=%+v", assistantBlocks)
	}
	if assistantBlocks[0].CacheControl["type"] != "ephemeral" {
		t.Fatalf("assistant cache_control=%v", assistantBlocks[0].CacheControl)
	}
	if userText != "x-anthropic-billing-header: preserve user text" {
		t.Fatalf("user text=%q", userText)
	}
}

func TestPreparePayload_DropsBillingHeaderOnlySystemText(t *testing.T) {
	src := `{"messages":[
		{"role":"system","content":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.321.abc; cch=00000;"}]},
		{"role":"user","content":"hello"}
	]}`
	messages := preparePayloadMessages(t, src)
	if len(messages) != 2 {
		t.Fatalf("messages=%d, want fallback system plus user", len(messages))
	}
	var first struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(messages[0], &first); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := json.Unmarshal(first.Content, &content); err != nil {
		t.Fatal(err)
	}
	if first.Role != "system" || content != "You are a helpful assistant." {
		t.Fatalf("leading system role=%q content=%q", first.Role, content)
	}
}

func TestPreparePayload_RemovesEmbeddedBillingHeaderFromIdentityText(t *testing.T) {
	src := `{"messages":[{"role":"system","content":"Keep before x-anthropic-billing-header: value; keep after"}]}`
	messages := preparePayloadMessages(t, src)
	var first struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(messages[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Content != "Keep before keep after" {
		t.Fatalf("system text=%q", first.Content)
	}
}

func TestPreparePayload_PreservesEmptySystemWithoutBillingHeader(t *testing.T) {
	src := `{"messages":[{"role":"system","content":""},{"role":"user","content":"hello"}]}`
	messages := preparePayloadMessages(t, src)
	if len(messages) != 2 {
		t.Fatalf("messages=%d, want 2", len(messages))
	}
	var first struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(messages[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Role != "system" || first.Content != "" {
		t.Fatalf("leading system=%+v", first)
	}
}
