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
		{"workbuddy/model-a", "", "model-a"},
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
	if got := PublicModelID("model-a"); got != "workbuddy/model-a" {
		t.Errorf("PublicModelID = %q", got)
	}
	if got := PublicModelID(""); got != "" {
		t.Errorf("PublicModelID('') = %q, want empty", got)
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
	if mapping["workbuddy/model-a"] != "model-a" {
		t.Errorf("mapping wrong: %v", mapping)
	}
	// 旧展示名派生的公开 ID 不得再被注册（本次为破坏性变更，无兼容别名）。
	if _, ok := mapping["workbuddy/Model A"]; ok {
		t.Errorf("display-name-derived ID still registered: %v", mapping)
	}
}

// 公开 ID 由内部 ID 派生：同一展示名、不同内部 ID 的两个模型必须各自可调用。
func TestFilterAndBuild_SameNameDifferentInternalIDsStayDistinct(t *testing.T) {
	models := []ModelInfo{
		{ID: "deepseek-v4.1-flash", Name: "Deepseek-V4.1-Flash"},
		{ID: "deepseek-v4.1-flash-sg", Name: "Deepseek-V4.1-Flash"},
	}
	mapping, entries := FilterAndBuild(models)
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2 (same display name must not collapse)", len(entries))
	}
	wantIDs := []string{"workbuddy/deepseek-v4.1-flash", "workbuddy/deepseek-v4.1-flash-sg"}
	for i, want := range wantIDs {
		if entries[i].ID != want {
			t.Errorf("entries[%d].ID=%q, want %q", i, entries[i].ID, want)
		}
		if mapping[want] != models[i].ID {
			t.Errorf("mapping[%q]=%q, want %q", want, mapping[want], models[i].ID)
		}
	}
	for i := range entries {
		if entries[i].Name != "Deepseek-V4.1-Flash" {
			t.Errorf("entries[%d].Name=%q, want upstream display name preserved", i, entries[i].Name)
		}
	}
}

// 公开 ID 与展示名解耦：上游改名不改变已注册的可调用 ID。
func TestFilterAndBuild_StableAcrossRenamedDisplayNames(t *testing.T) {
	before, _ := FilterAndBuild([]ModelInfo{{ID: "model-a", Name: "Model A"}})
	after, entries := FilterAndBuild([]ModelInfo{{ID: "model-a", Name: "Model A Renamed"}})
	if before["workbuddy/model-a"] != "model-a" || after["workbuddy/model-a"] != "model-a" {
		t.Fatalf("public ID moved with display name: before=%v after=%v", before, after)
	}
	if len(entries) != 1 || entries[0].ID != "workbuddy/model-a" {
		t.Fatalf("entries=%v, want single workbuddy/model-a", entries)
	}
	if entries[0].Name != "Model A Renamed" {
		t.Errorf("Name=%q, want renamed upstream display name", entries[0].Name)
	}
}

// 同一内部 ID 出现在两个 realm 的目录里时得到同一个可调用 ID。
func TestFilterAndBuild_SameInternalIDAcrossRealms(t *testing.T) {
	cn, _ := FilterAndBuild([]ModelInfo{{ID: "deepseek-v4.1-flash", Name: "Deepseek-V4.1-Flash"}})
	global, _ := FilterAndBuild([]ModelInfo{{ID: "deepseek-v4.1-flash", Name: "DeepSeek-V4.1-Flash"}})
	if _, ok := cn["workbuddy/deepseek-v4.1-flash"]; !ok {
		t.Fatalf("cn mapping=%v", cn)
	}
	if _, ok := global["workbuddy/deepseek-v4.1-flash"]; !ok {
		t.Fatalf("global mapping=%v (case-differing display name must not change ID)", global)
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
				{"id": "", "name": ""}, // 空 id + 空 name → 过滤
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
