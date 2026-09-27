package wbexecutor

import "testing"

func TestNormalizeReasoningEffort(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		key       string
		supported []string
		want      string
	}{
		{name: "downgrade", value: "xhigh", key: "reasoning_effort", supported: []string{"low", "high"}, want: "high"},
		{name: "floor", value: "low", key: "reasoning_effort", supported: []string{"high", "xhigh"}, want: "high"},
		{name: "camel_case", value: "xhigh", key: "reasoningEffort", supported: []string{"high"}, want: "high"},
		{name: "supported", value: "high", key: "reasoning_effort", supported: []string{"low", "high"}, want: "high"},
		{name: "unknown_value", value: "ultra", key: "reasoning_effort", supported: []string{"low", "high"}, want: "ultra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := map[string]any{"model": "model-a", tt.key: tt.value}
			normalizeReasoningEffort(obj, tt.supported)
			if got := obj[tt.key]; got != tt.want {
				t.Fatalf("effort=%v, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeReasoningEffort_Passthrough(t *testing.T) {
	obj := map[string]any{"model": "model-a", "reasoning_effort": "high"}
	normalizeReasoningEffort(obj, nil)
	if got := obj["reasoning_effort"]; got != "high" {
		t.Fatalf("effort=%v, want high", got)
	}

	obj = map[string]any{"model": "model-a"}
	normalizeReasoningEffort(obj, []string{"high"})
	if _, ok := obj["reasoning_effort"]; ok {
		t.Fatal("normalizer injected a missing effort field")
	}
}
