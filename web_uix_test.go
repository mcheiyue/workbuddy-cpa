package main

import (
	"strings"
	"testing"
)

// TestWebUIBootstrapOnSaveKey 锁：密钥保存后立即拉取统计/账号/模型，
// 而不是等用户手动点三次刷新（进页已自动加载，补保存后的缺口）。
func TestWebUIBootstrapOnSaveKey(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{
		"function bootstrap",
		"bootstrap()", // saveKey 与初始化两处调用
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
	// saveKey 里必须在写 localStorage 后触发 bootstrap
	i := strings.Index(html, "function saveKey")
	if i < 0 {
		t.Fatal("saveKey missing")
	}
	chunk := html[i : i+400]
	if !strings.Contains(chunk, "bootstrap()") {
		t.Error("saveKey must call bootstrap after saving key")
	}
}

// TestWebUIBusyGuards 锁：预设/签到/配额刷新期间禁用入口，
// 并给预设串行循环进度提示（延迟是设计代价，必须可见）。
func TestWebUIBusyGuards(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{
		"presetBusy",
		"正在写入优先级",
		"btn.disabled=true", // 预设三键与签到/配额按钮进入 busy 态
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
	i := strings.Index(html, "function applyPriorityPreset")
	if i < 0 {
		t.Fatal("applyPriorityPreset missing")
	}
	end := strings.Index(html[i:], "\nasync function toast")
	if end < 0 {
		end = 1200
	}
	chunk := html[i : i+end]
	if !strings.Contains(chunk, "presetBusy=true") {
		t.Error("applyPriorityPreset must set presetBusy")
	}
	if !strings.Contains(chunk, "presetBusy=false") {
		t.Error("applyPriorityPreset must clear presetBusy in finally")
	}
}

// TestWebUIErrorBody 锁：非 2xx 时优先展示上游错误体原文，
// 不截断在冒号/状态码上（对齐 manager 08e75eb 的错误提示口径）。
func TestWebUIErrorBody(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{
		"请求失败",
		"errorBody",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
}
