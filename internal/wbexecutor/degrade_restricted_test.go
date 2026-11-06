package wbexecutor

import (
	"net/http"
	"testing"
	"time"
)

const restrictedEN = `{"code":11140,"msg":"request illegal","displayMsg":{"en":"Your account has been restricted due to unusual activity","zh":"账号存在异常行为，已被限制使用"}}`

// 锁：只有「账号受限」文案算受限。普通 request illegal 与 11140 模型限流文案
// 都不能触发持久停用，否则健康账号会被误停。
func TestIsAccountRestricted(t *testing.T) {
	if !isAccountRestricted(restrictedEN) {
		t.Fatal("restricted EN wording must be detected")
	}
	if !isAccountRestricted(`{"code":11140,"displayMsg":"账号存在异常行为，已被限制使用"}`) {
		t.Fatal("restricted zh wording must be detected")
	}
	if isAccountRestricted(`{"code":11140,"msg":"request illegal","requestId":"x"}`) {
		t.Fatal("plain request illegal must not be restricted")
	}
	if isAccountRestricted(`{"code":11140,"msg":"The model provider is rate-limiting requests."}`) {
		t.Fatal("model rate-limit wording must not be restricted")
	}
	if isAccountRestricted(`{"code":0,"msg":"restricted due to unusual activity"}`) {
		t.Fatal("wording without restriction code must not disable an account")
	}
}

// 明确受限交由宿主持久停用；人工启用后不能仍被进程内旧冷却拦截。
func TestDegradeGate_RestrictedDoesNotLeaveTemporaryWindow(t *testing.T) {
	g := NewDegradeGate()
	now := time.Unix(1790000000, 0)
	g.Now = func() time.Time { return now }
	hit := &ExecError{Kind: ErrAccountFault, Status: http.StatusInternalServerError, Restricted: true}

	for range 6 {
		g.note("auth-a", hit)
	}
	if e := g.enter("auth-a"); e != nil {
		t.Fatal("persistent restriction must not create temporary cooldown")
	}
}

// 锁：普通账号级故障仍按原有 5 连败阈值，不能因为新增受限分支被提前触发。
func TestDegradeGate_PlainAccountFaultStillNeedsThreshold(t *testing.T) {
	g := NewDegradeGate()
	now := time.Unix(1790000000, 0)
	g.Now = func() time.Time { return now }

	g.note("auth-b", &ExecError{Kind: ErrAccountFault, Status: http.StatusInternalServerError})
	if got := g.enter("auth-b"); got != nil {
		t.Fatal("one plain account fault must not open the window")
	}
}
