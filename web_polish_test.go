package main

import (
	"strings"
	"testing"
)

// --- WebUI 四项抛光（2026-10-15） ---

// TestWebUILedgerUidTail 锁：流水表账号列映射 uid 尾号（对齐账号表），
// 不再裸显 36 字符 UUID；auth_index 无映射时兜底截断。
func TestWebUILedgerUidTail(t *testing.T) {
	html := string(workbuddyWebUI)
	i := strings.Index(html, "async function loadCreditsLedger")
	if i < 0 {
		t.Fatal("loadCreditsLedger missing")
	}
	end := strings.Index(html[i:], "\n// ---")
	if end < 0 {
		end = 2000
	}
	chunk := html[i : i+end]
	for _, want := range []string{"uid_tail", "authShort", "api('/accounts')"} {
		if !strings.Contains(chunk, want) {
			t.Errorf("loadCreditsLedger missing %q", want)
		}
	}
}

// TestWebUIModelFilter 锁：模型目录带前端过滤框（44+ 行可按 ID/展示名过滤）。
func TestWebUIModelFilter(t *testing.T) {
	html := string(workbuddyWebUI)
	if !strings.Contains(html, `id="modelFilter"`) {
		t.Error("models card missing filter input")
	}
	i := strings.Index(html, "async function loadModels")
	if i < 0 {
		t.Fatal("loadModels missing")
	}
	chunk := html[i : i+900]
	if !strings.Contains(chunk, "filterModels()") {
		t.Error("loadModels must route through filterModels()")
	}
	if !strings.Contains(html, "function filterModels") {
		t.Error("filterModels function missing")
	}
}

// TestWebUIGlobalCheckinDash 锁：账号表「上次签到」列 global 行显示「—」——
// 该字段实为 ledger 观测时间，global 无签到体系，显示时间戳语义误导。
func TestWebUIGlobalCheckinDash(t *testing.T) {
	html := string(workbuddyWebUI)
	i := strings.Index(html, "async function loadAccounts")
	if i < 0 {
		t.Fatal("loadAccounts missing")
	}
	end := strings.Index(html[i:], "\nfunction populateSelects")
	if end < 0 {
		end = 1600
	}
	chunk := html[i : i+end]
	if !strings.Contains(chunk, `x.realm==='global'?'—'`) {
		t.Error("loadAccounts must show — for global last_checkin")
	}
}

// TestWebUILoginRefreshModels 锁：登录成功后除账号外还刷新模型目录。
func TestWebUILoginRefreshModels(t *testing.T) {
	html := string(workbuddyWebUI)
	i := strings.Index(html, "async function startLogin")
	if i < 0 {
		t.Fatal("startLogin missing")
	}
	end := strings.Index(html[i:], "\nasync function loadAccounts")
	if end < 0 {
		end = 2400
	}
	chunk := html[i : i+end]
	if !strings.Contains(chunk, "登录成功") {
		t.Fatal("login success branch missing")
	}
	if !strings.Contains(chunk, "loadModels()") {
		t.Error("login success must also loadModels()")
	}
}
