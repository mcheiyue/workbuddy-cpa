package main

import (
	"strings"
	"testing"
)

// --- WebUI：credits 阶梯可视化 + 配额全部账号 + 日志页三卡联动（2026-10-14） ---
// 背景：本批给 global 补每日余额快照，WebUI 流水表必须能直接读出每日 +30 阶梯。

// TestWebUILedgerDeltaNewestFirst 锁：Credits 流水含「变化」delta 列（着色）且最新在前。
func TestWebUILedgerDeltaNewestFirst(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{"变化", "entries.slice().reverse()", "dcls"} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
}

// TestWebUIRefreshQuotaAllAccounts 锁：配额下拉的「全部账号」必须真可用——
// 串行逐号查询并渲染汇总表（此前选全部直接 toast 报错，选项是死的）。
func TestWebUIRefreshQuotaAllAccounts(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{"正在查询配额", "配额查询完成"} {
		if !strings.Contains(html, want) {
			t.Errorf("web/index.html missing %q", want)
		}
	}
	i := strings.Index(html, "async function refreshQuota")
	if i < 0 {
		t.Fatal("refreshQuota missing")
	}
	end := strings.Index(html[i:], "\nasync function doCheckin")
	if end < 0 {
		end = 1600
	}
	chunk := html[i : i+end]
	if !strings.Contains(chunk, "if(!idx){") {
		t.Error("refreshQuota must handle 全部账号 branch")
	}
}

// TestWebUILogsTabAutoLoad 锁：切到日志页时三张卡一起加载（此前只 loadTasks，
// 签到记录/流水要点各自刷新）。
func TestWebUILogsTabAutoLoad(t *testing.T) {
	html := string(workbuddyWebUI)
	i := strings.Index(html, "function setView")
	if i < 0 {
		t.Fatal("setView missing")
	}
	chunk := html[i : i+500]
	for _, want := range []string{"loadTasks()", "loadLastCheckin()", "loadCreditsLedger()"} {
		if !strings.Contains(chunk, want) {
			t.Errorf("setView logs branch missing %q", want)
		}
	}
}

// TestWebUICheckinStatCNOnly 锁：总览「今日已签到」只统计 CN 账号——
// last_checkin 字段实为 ledger 观测时间（非签到），global 无签到体系，
// 口径含 global 会永久显示假不满。
func TestWebUICheckinStatCNOnly(t *testing.T) {
	html := string(workbuddyWebUI)
	i := strings.Index(html, "function loadStats")
	if i < 0 {
		t.Fatal("loadStats missing")
	}
	end := strings.Index(html[i:], "\nasync function")
	if end < 0 {
		end = 2200
	}
	chunk := html[i : i+end]
	for _, want := range []string{"今日已签到(CN)", "realm==='cn'"} {
		if !strings.Contains(chunk, want) {
			t.Errorf("loadStats missing %q", want)
		}
	}
}

// TestWebUIQuotaEmptyHint 锁：配额空态文案跟上「全部账号」已可用。
func TestWebUIQuotaEmptyHint(t *testing.T) {
	html := string(workbuddyWebUI)
	if !strings.Contains(html, "选择账号或全部账号") {
		t.Error("quota empty hint must mention 全部账号 option")
	}
}
