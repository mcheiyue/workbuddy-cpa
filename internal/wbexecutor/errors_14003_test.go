package wbexecutor

import (
	"net/http"
	"testing"
)

// 锁：14003 是模型级繁忙，必须归 ErrModelRateLimit 而非 ErrSoftRate。
//
// 判定依据来自现网 24h 46 次实测：
//   - 同一模型（Deepseek-V4.1-Flash）在 89ac14d9 / 8cd98657 / ecdd9a86 三个
//     不同账号上都返回 14003 —— 不是账号级；
//   - 上游 displayMsg 明写「Model busy. Please switch models or retry later」
//     （中文「模型繁忙，请换模型或稍后重试」）—— 上游自己要求换模型，不是换号。
//
// 归 ErrSoftRate 会让 degradeGate 记账号级连败并开冷却窗，把好账号一起罚掉；
// 归 ErrModelRateLimit 才是模型级冷却（executor_dispatch 的 setCooldown(auth, model)）。
//
// 注意：14003 的 body 含 "too many requests"，会被通用文案兜底命中，
// 所以判定必须放在该兜底之前。
func TestClassify14003IsModelLevelNotSoftRate(t *testing.T) {
	body := `{"code":14003,"msg":"too many requests","requestId":"5e65e5440cc5b5094289b96851f26ef6","displayMsg":{"en":"Model busy. Please switch models or retry later","zh":"模型繁忙，请换模型或稍后重试"}}`
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		if got := Classify(status, body); got != ErrModelRateLimit {
			t.Errorf("Classify(%d, 14003)=%v, want ErrModelRateLimit", status, got)
		}
	}
}

// 锁：14003 的判定不得放宽成「body 含 14003 就命中」——requestId 等字段里
// 出现同样数字时不能误判（与 11148 同款边界）。
func TestClassify14003NotTriggeredByRequestID(t *testing.T) {
	body := `{"code":5001,"msg":"some other failure","requestId":"abc14003def"}`
	if got := Classify(http.StatusInternalServerError, body); got == ErrModelRateLimit {
		t.Error("requestId containing 14003 must not classify as ErrModelRateLimit")
	}
}

// 锁：普通 429 仍归 ErrSoftRate——加 14003 不能改变既有软限流语义。
func TestClassify429StillSoftRate(t *testing.T) {
	body := `{"code":429,"msg":"rate limit exceeded"}`
	if got := Classify(http.StatusTooManyRequests, body); got != ErrSoftRate {
		t.Errorf("Classify(429)=%v, want ErrSoftRate", got)
	}
}
