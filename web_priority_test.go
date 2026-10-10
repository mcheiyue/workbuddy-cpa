package main

import (
	"strings"
	"testing"
)

// TestWebUIAccountsPriorityEditor 锁 WebUI 优先级编辑接线：
// 账号表必须有优先级列与保存入口，且写路径走宿主
// PATCH /v0/management/auth-files/fields（name+priority），不新造插件写端点。
// 按行保存，携带该行 file（宿主文件名）作为 PATCH 定位键。
func TestWebUIAccountsPriorityEditor(t *testing.T) {
	html := string(workbuddyWebUI)
	if html == "" {
		t.Fatal("workbuddyWebUI empty")
	}
	for _, want := range []string{
		"优先级",               // 优先级列头
		"auth-files/fields", // 宿主字段 PATCH 端点
		"savePriority",      // 保存入口
		"method:'PATCH'",    // PATCH 动词（authApi 仅 POST，需独立 helper）
		"数字越大越优先",           // 方向提示（宿主取最高层）
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
}

// TestWebUIAccountsPriorityPresets 锁三键预设：国际优先/国内优先/全部清零。
// 预设必须复用 authPatch 串行 PATCH（不并发写 auth 目录），并对齐
// wb-priority.ps1 语义：global=10 / cn=0 或反之 / 清零=全 0。
func TestWebUIAccountsPriorityPresets(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{
		"applyPriorityPreset",
		"国际优先",
		"国内优先",
		"全部清零",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
}
