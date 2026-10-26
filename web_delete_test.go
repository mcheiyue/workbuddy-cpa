package main

import (
	"strings"
	"testing"
)

// TestWebUIAccountsDeleteButton 锁 WebUI 删除账号接线：账号表必须有删除按钮，
// 且删除走宿主自带 DELETE /v0/management/auth-files（插件 SDK 的 auth.get/list/save
// 没有 delete，只能直连宿主管理面），删除后必须重载账号表。
func TestWebUIAccountsDeleteButton(t *testing.T) {
	html := string(workbuddyWebUI)
	if html == "" {
		t.Fatal("workbuddyWebUI empty")
	}
	for _, want := range []string{
		"authDelete",        // 删除 helper
		"deleteAccount",     // 行内按钮入口
		"method:'DELETE'",   // DELETE 动词（authApi/authPatch 都不覆盖）
		"/auth-files?name=", // 宿主删除端点 + 定位键
		">删除</button>",      // 按钮文案
		"需重新登录才能恢复",         // 二次确认必须说明不可逆
	} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
}
