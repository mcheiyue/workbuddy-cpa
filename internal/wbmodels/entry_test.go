package wbmodels

import (
	"encoding/json"
	"testing"
)

// --- NonChatModel 过滤测试 ---

func TestNonChatModel_PrefixFilter(t *testing.T) {
	for _, id := range []string{"nes-embedding", "completion-foo", "codewise-bar"} {
		if !NonChatModel(id, 0, nil) {
			t.Errorf("NonChatModel(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"gpt-5.6", "hy4-preview", "deepseek-v4.1"} {
		if NonChatModel(id, 0, nil) {
			t.Errorf("NonChatModel(%q) = true, want false", id)
		}
	}
}

func TestNonChatModel_MaxOutputFilter(t *testing.T) {
	if !NonChatModel("some-model", 128, nil) {
		t.Error("maxOutputTokens=128 should be filtered")
	}
	if NonChatModel("some-model", 257, nil) {
		t.Error("maxOutputTokens=257 should not be filtered")
	}
	if NonChatModel("some-model", 0, nil) {
		t.Error("maxOutputTokens=0 (unknown) should not be filtered")
	}
}

func TestNonChatModel_TagFilter(t *testing.T) {
	if !NonChatModel("img-gen", 0, []string{"text-to-image"}) {
		t.Error("text-to-image tag should be filtered")
	}
	if NonChatModel("img-gen", 0, []string{"chat", "reasoning"}) {
		t.Error("other tags should not be filtered")
	}
}

// --- DisplayNameForModel ---

func TestDisplayNameForModel(t *testing.T) {
	tests := []struct {
		id, name, want string
	}{
		{"model-a", "Model A", "Model A"},
		{"model-a", "", "model-a"},
		{"model-a", "  ", "model-a"},
		{"new-model", "New Model", "New Model"},
	}
	for _, tt := range tests {
		if got := DisplayNameForModel(tt.id, tt.name); got != tt.want {
			t.Errorf("DisplayNameForModel(%q, %q) = %q, want %q", tt.id, tt.name, got, tt.want)
		}
	}
}

// --- PublicModelID ---

func TestPublicModelID(t *testing.T) {
	if got := PublicModelID("Model A"); got != "workbuddy/Model A" {
		t.Errorf("PublicModelID = %q", got)
	}
	if got := PublicModelID(""); got != "" {
		t.Errorf("PublicModelID('') = %q, want empty", got)
	}
}

// --- UniquePublicModelID 冲突回退 ---

func TestUniquePublicModelID_NoCollision(t *testing.T) {
	mapping := map[string]string{}
	got := UniquePublicModelID("Model A", "model-a", mapping)
	if got != "workbuddy/Model A" {
		t.Errorf("got %q, want workbuddy/Model A", got)
	}
}

func TestUniquePublicModelID_DisplayNameCollision(t *testing.T) {
	mapping := map[string]string{"workbuddy/Same Name": "model-a"}
	// 两个模型共享展示名 "Same Name"，第二个应回退到内部 key
	got := UniquePublicModelID("Same Name", "model-b", mapping)
	if got != "workbuddy/model-b" {
		t.Errorf("got %q, want workbuddy/model-b", got)
	}
}

func TestUniquePublicModelID_BothCollision(t *testing.T) {
	// 同一个 internalID 映射到 "workbuddy/model-b"，无冲突
	mapping := map[string]string{
		"workbuddy/Same Name": "model-a",
		"workbuddy/model-b":   "model-b",
	}
	got := UniquePublicModelID("Same Name", "model-b", mapping)
	if got != "workbuddy/model-b" {
		t.Errorf("got %q, want workbuddy/model-b (same internalID, no collision)", got)
	}

	// 不同 internalID 且 fallback 也冲突 → 递增后缀
	mapping2 := map[string]string{
		"workbuddy/Same Name": "model-a",
		"workbuddy/model-b":   "model-x",
	}
	got2 := UniquePublicModelID("Same Name", "model-b", mapping2)
	if got2 != "workbuddy/model-b-2" {
		t.Errorf("got %q, want workbuddy/model-b-2", got2)
	}
}

func TestUniquePublicModelID_EmptyName(t *testing.T) {
	got := UniquePublicModelID("", "model-a", map[string]string{})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- FilterAndBuild ---

func TestFilterAndBuild_Dedup(t *testing.T) {
	models := []ModelInfo{
		{ID: "model-a", Name: "Model A"},
		{ID: "model-a", Name: "duplicate"}, // 重复内部 key
		{ID: "model-b", Name: "Model B"},
	}
	mapping, entries := FilterAndBuild(models)
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(entries))
	}
	if mapping["workbuddy/Model A"] != "model-a" {
		t.Errorf("mapping wrong: %v", mapping)
	}
}

func TestFilterAndBuild_DisabledFiltered(t *testing.T) {
	// Disabled 过滤由 parseConfigModels 完成，FilterAndBuild 只负责去重
	models := []ModelInfo{
		{ID: "a"},
		{ID: "b"},
	}
	_, entries := FilterAndBuild(models)
	if len(entries) != 2 || entries[0].ID != "workbuddy/a" || entries[1].ID != "workbuddy/b" {
		t.Fatalf("entries=%v", entries)
	}
}

func TestFilterAndBuild_EmptyIDFallback(t *testing.T) {
	// id 为空但 name 非空时应回退到 name 作为内部 key
	models := []ModelInfo{{Name: "fallback-name"}}
	_, entries := FilterAndBuild(models)
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
	if entries[0].ID != "workbuddy/fallback-name" {
		t.Errorf("ID=%q, want workbuddy/fallback-name", entries[0].ID)
	}
}

func TestFilterAndBuild_OrderIndependent(t *testing.T) {
	models := []ModelInfo{
		{ID: "c", Name: "C"},
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
	}
	m1, _ := FilterAndBuild(models)
	// 打乱顺序
	shuffled := []ModelInfo{models[2], models[0], models[1]}
	m2, _ := FilterAndBuild(shuffled)
	// 映射内容一致（顺序不保证，逐 key 对比）
	for k, v := range m1 {
		if m2[k] != v {
			t.Errorf("mapping[%q] = %q vs %q", k, m1[k], m2[k])
		}
	}
}

// --- parseConfigModels ---

func TestParseConfigModels(t *testing.T) {
	resp := map[string]any{
		"code": 0,
		"data": map[string]any{
			"models": []map[string]any{
				{"id": "gpt-5.6", "name": "GPT 5.6", "disabled": false},
				{"id": "nes-embedding", "name": "NES Embed", "disabled": false},
				{"id": "fast", "name": "Fast", "disabled": true},
				{"id": "", "name": ""},             // 空 id + 空 name → 过滤
				{"id": "tiny", "name": "Tiny", "maxOutputTokens": 64},
			},
		},
	}
	raw, _ := json.Marshal(resp)
	models, err := ParseConfigModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 过滤后只剩 gpt-5.6（nes- 被前缀过滤、fast 被 disabled、空 id/name 被过滤、tiny 被 maxOutput 过滤）
	if len(models) != 1 || models[0].ID != "gpt-5.6" {
		t.Fatalf("models=%v", models)
	}
}

func TestParseConfigModels_NonZeroCode(t *testing.T) {
	raw := []byte(`{"code":401,"data":null}`)
	_, err := ParseConfigModels(raw)
	if err == nil {
		t.Fatal("expected error for non-zero code")
	}
}

func TestParseConfigModels_BadJSON(t *testing.T) {
	_, err := ParseConfigModels([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}
