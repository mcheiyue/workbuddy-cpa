package main

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// benefitsAutoRecorder 记录 mock 收到的 (method, path) 序列，用于断言「只领可领项」。
type benefitsAutoRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *benefitsAutoRecorder) add(method, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, method+" "+path)
}

func (r *benefitsAutoRecorder) count(suffix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if len(c) >= len(suffix) && c[len(c)-len(suffix):] == suffix {
			n++
		}
	}
	return n
}

// traits 按路径返回状态体；claim 路径一律返回 code:0。
func benefitsAutoClient(rec *benefitsAutoRecorder, giftActive, giftClaimed, compActive, compClaimed bool) *http.Client {
	statusBody := func(active, claimed bool) string {
		if claimed {
			return `{"code":0,"data":{"claimed":true,"active":true,"credit_num":1500}}`
		}
		if active {
			return `{"code":0,"data":{"claimed":false,"active":true,"credit_num":500,"end_time":"2026-10-22 23:59:59"}}`
		}
		return `{"code":0,"data":{"claimed":false,"active":false}}`
	}
	return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case benefitGiftCheckPath:
			w.Write([]byte(statusBody(giftActive, giftClaimed)))
		case benefitCompStatusPath:
			w.Write([]byte(statusBody(compActive, compClaimed)))
		default:
			w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
		}
	}}}
}

// 锁：自动领取必须只对「可领」项发写请求。
// 依据：claimable() = active && !claimed。global 号两侧窗口 4 月已关（active=false），
// 若不过滤就会对每个 global 账号每天发 2 次空写请求；已领项再领会被上游拒（409）。
// 这条断言把「查询 2 次 + 领取恰好 1 次」钉死，防止后人把 claimable 过滤删成无条件领。
func TestClaimAllBenefits_OnlyClaimable(t *testing.T) {
	rec := &benefitsAutoRecorder{}
	client := benefitsAutoClient(rec, true, false, true, true)
	cred := benefitTestCred(t, wbauth.RealmCN, "www.workbuddy.cn")

	summary, err := claimAllBenefits(client, wbauth.RealmCN, cred)
	if err != nil {
		t.Fatalf("claimAllBenefits: %v", err)
	}
	if got := rec.count("POST " + benefitGiftCheckPath); got != 1 {
		t.Fatalf("gift 状态查询 %d 次，want 1", got)
	}
	if got := rec.count("POST " + benefitCompStatusPath); got != 1 {
		t.Fatalf("compensation 状态查询 %d 次，want 1", got)
	}
	if got := rec.count("POST " + benefitGiftClaimPath); got != 1 {
		t.Fatalf("gift 领取 %d 次，want 1（active 且未领）", got)
	}
	if got := rec.count("POST " + benefitCompClaimPath); got != 0 {
		t.Fatalf("compensation 已领仍发领取 %d 次，want 0", got)
	}
	if summary == "" {
		t.Fatal("summary 不得为空（任务日志需要可读结果）")
	}
}

// 锁：全部不可领时零写请求（global 窗口已关的常态）。
// 不写这条，后人会以为「查询到就领」是安全的，导致每天对 5 个 global 号各发 2 次空写。
func TestClaimAllBenefits_InactiveSkipsAllWrites(t *testing.T) {
	rec := &benefitsAutoRecorder{}
	client := benefitsAutoClient(rec, false, false, false, false)
	cred := benefitTestCred(t, wbauth.RealmGlobal, "www.workbuddy.ai")

	if _, err := claimAllBenefits(client, wbauth.RealmGlobal, cred); err != nil {
		t.Fatalf("claimAllBenefits: %v", err)
	}
	if got := rec.count("POST " + benefitGiftClaimPath); got != 0 {
		t.Fatalf("gift active=false 仍发领取 %d 次，want 0", got)
	}
	if got := rec.count("POST " + benefitCompClaimPath); got != 0 {
		t.Fatalf("comp active=false 仍发领取 %d 次，want 0", got)
	}
	if got := rec.count("POST " + benefitGiftCheckPath); got != 1 {
		t.Fatalf("gift 状态查询 %d 次，want 1", got)
	}
	if got := rec.count("POST " + benefitCompStatusPath); got != 1 {
		t.Fatalf("comp 状态查询 %d 次，want 1", got)
	}
}

// 锁：任务表必须挂上 benefits 槽，且小时为 11（跟随既有 claim 槽，早于 10-22 23:59 的补偿窗口）。
// buildWakes 的 scope 过滤是结构性门控：scope 写错会让整类账号零唤醒（静默不领）。
func TestOpsDailyTasks_HasBenefitsSlot(t *testing.T) {
	var found *opsDailyTask
	for i := range opsDailyTasks {
		if opsDailyTasks[i].name == "benefits" {
			found = &opsDailyTasks[i]
			break
		}
	}
	if found == nil {
		t.Fatal("opsDailyTasks 缺 benefits 槽")
	}
	if found.hour != 11 {
		t.Fatalf("benefits hour=%d, want 11", found.hour)
	}
	if found.scope != "" {
		t.Fatalf("benefits scope=%q, want \"\"（CN 有补偿、global 窗口将来可能重开，都要唤醒）", found.scope)
	}
	for _, realm := range []string{wbauth.RealmCN, wbauth.RealmGlobal} {
		wakes := buildWakes(time.Now(), realm)
		hit := false
		for _, w := range wakes {
			if w.task.name == "benefits" {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("realm=%s 的唤醒表缺 benefits", realm)
		}
	}
}
