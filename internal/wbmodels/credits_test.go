package wbmodels

import "testing"

// TestNormalizeCredits 归一上游 credits 倍率串：上游格式不统一
// （裸 "x0.00" / 带后缀 "x0.34 credits" / 空串），统一剥掉可选 " credits" 后缀。
func TestNormalizeCredits(t *testing.T) {
	cases := []struct{ in, want string }{
		{"x0.00", "x0.00"},
		{"x0.00 credits", "x0.00"},
		{"x0.34 credits", "x0.34"},
		{"x3.31 credits", "x3.31"},
		{"x0.11 credits", "x0.11"},
		{"x0.79", "x0.79"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := NormalizeCredits(c.in); got != c.want {
			t.Errorf("NormalizeCredits(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

// TestFilterAndBuild_CarriesCredits Credits 必须随 FilterAndBuild 透传——模型目录出倍率靠它。
func TestFilterAndBuild_CarriesCredits(t *testing.T) {
	_, entries := FilterAndBuild([]ModelInfo{
		{ID: "deepseek-v4.1-flash", Name: "Deepseek-V4.1-Flash", Credits: "x0.00"},
	})
	if len(entries) != 1 {
		t.Fatalf("entries=%d want 1", len(entries))
	}
	if entries[0].Credits != "x0.00" {
		t.Errorf("Credits lost in FilterAndBuild: %q", entries[0].Credits)
	}
}

// TestParseConfigModels_Credits /v3/config 的 credits 字段必须被解析进 ModelInfo。
func TestParseConfigModels_Credits(t *testing.T) {
	raw := []byte(`{"code":0,"data":{"models":[{"id":"m1","name":"M1","credits":"x0.34 credits"}]}}`)
	models, err := parseConfigModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Credits != "x0.34 credits" {
		t.Fatalf("credits not parsed: %+v", models)
	}
}
