package wbauth

import (
	"testing"
	"time"
)

// 锁：NextRefreshAfter 必须是「过期前 5 分钟」而不是过期时刻。
// 宿主 auto_refresh_loop 在 NextRefreshAfter 到点时才发 refresh；
// 若等于过期时刻，一次失败就等于 token 已死、账号立刻不可用。
// ProviderRefreshLead("workbuddy") 返回 nil（宿主只注册了
// codex/claude/antigravity/kimi/xai/devin/meta），所以只能由插件自己提前。
func TestAuthDataRefreshesBeforeExpiry(t *testing.T) {
	expiry := time.Now().Add(2 * time.Hour)
	cred := Credential{
		AccessToken: "a", RefreshToken: "r", Realm: RealmGlobal,
		ExpiresAt: expiry.Unix(),
	}
	data := cred.AuthData("")
	want := expiry.Add(-refreshLead)
	if d := data.NextRefreshAfter.Sub(want); d > time.Second || d < -time.Second {
		t.Errorf("NextRefreshAfter=%v, want ~%v (expiry-5m)", data.NextRefreshAfter, want)
	}
	if !data.NextRefreshAfter.Before(expiry) {
		t.Error("NextRefreshAfter must be strictly before expiry, else a failed refresh leaves a dead token")
	}
}

// 锁：已过期凭证不得算出「过去很久」的刷新时刻，否则宿主会立刻重刷并反复失败。
func TestAuthDataRefreshTimeNeverInPast(t *testing.T) {
	cred := Credential{
		AccessToken: "a", RefreshToken: "r", Realm: RealmGlobal,
		ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	}
	data := cred.AuthData("")
	if data.NextRefreshAfter.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("NextRefreshAfter=%v is far in the past; must clamp to now", data.NextRefreshAfter)
	}
}

// 锁：ExpiresAt 缺省（0）时不得算出远古时间，否则宿主会立即无限重刷。
func TestAuthDataZeroExpiryStaysFarFuture(t *testing.T) {
	cred := Credential{AccessToken: "a", RefreshToken: "r", Realm: RealmGlobal}
	data := cred.AuthData("")
	if data.NextRefreshAfter.Before(time.Now()) {
		t.Errorf("NextRefreshAfter=%v, want far future for zero expiry", data.NextRefreshAfter)
	}
}
