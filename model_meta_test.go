package main

import (
	"reflect"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbmodels"
)

func TestToPluginModelInfo_ReasoningAndTokenMetadata(t *testing.T) {
	model := wbmodels.ModelInfo{
		ID:              "public-model",
		Name:            "Model",
		MaxInputTokens:  200000,
		MaxOutputTokens: 8192,
		SupportsReason:  true,
	}
	model.Reasoning.SupportedEfforts = []string{"low", "medium", "high"}
	got := toPluginModelInfo([]wbmodels.ModelInfo{model})
	if len(got) != 1 {
		t.Fatalf("models=%d, want 1", len(got))
	}
	info := got[0]
	if info.InputTokenLimit != 200000 || info.ContextLength != 200000 ||
		info.OutputTokenLimit != 8192 || info.MaxCompletionTokens != 8192 {
		t.Fatalf("token metadata=%+v", info)
	}
	if info.Thinking == nil || !info.Thinking.ZeroAllowed ||
		!reflect.DeepEqual(info.Thinking.Levels, []string{"low", "medium", "high"}) {
		t.Fatalf("thinking=%+v", info.Thinking)
	}
}

func TestToPluginModelInfo_SingleEffortAndNoReasoning(t *testing.T) {
	model := wbmodels.ModelInfo{ID: "single", SupportsReason: true}
	model.Reasoning.Effort = "high"
	got := toPluginModelInfo([]wbmodels.ModelInfo{model, {ID: "plain"}})
	if !reflect.DeepEqual(got[0].Thinking.Levels, []string{"high"}) {
		t.Fatalf("single effort=%v", got[0].Thinking.Levels)
	}
	if got[1].Thinking != nil {
		t.Fatalf("plain model thinking=%+v, want nil", got[1].Thinking)
	}
}
