package wbauth

import "strings"

// Realm 枚举常量。
const (
	RealmCN     = "cn"
	RealmGlobal = "global"
)

// 端点基础 URL。国内统一走 www.workbuddy.cn（2026-09-26 切换：state/chat/billing 同域
// 实测可用；旧入口 copilot.tencent.com + www.codebuddy.cn 为回退点）。
const (
	BaseCN     = "https://www.workbuddy.cn"
	BaseGlobal = "https://www.workbuddy.ai"
)

// Origin/Referer 头值。
const (
	OriginCN     = "https://www.workbuddy.cn"
	OriginGlobal = "https://www.workbuddy.ai"
)

// Billing base URL（按 realm 选；CN 与 chat 同域）。
const (
	BaseBillingCN     = "https://www.workbuddy.cn"
	BaseBillingGlobal = "https://www.workbuddy.ai"
)

// ResolveRealm 归一化 realm：显式非空优先，否则按 domain 推断。
func ResolveRealm(explicit, domain string) string {
	if r := strings.TrimSpace(explicit); r == RealmCN || r == RealmGlobal {
		return r
	}
	if IsGlobalDomain(domain) {
		return RealmGlobal
	}
	return RealmCN
}

// IsGlobalDomain 判定 domain 是否指向 workbuddy.ai 家族。
func IsGlobalDomain(d string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	return d == "workbuddy.ai" || strings.HasSuffix(d, ".workbuddy.ai")
}

// RealmBase 按 realm 返回上游 base URL。
func RealmBase(realm string) string {
	if realm == RealmGlobal {
		return BaseGlobal
	}
	return BaseCN
}

// RealmOrigin 按 realm 返回 Origin/Referer 头值。
func RealmOrigin(realm string) string {
	if realm == RealmGlobal {
		return OriginGlobal
	}
	return OriginCN
}

// BillingBase 返回计费域 base URL（billing 与 chat 不同域）。
func BillingBase(realm string) string {
	if realm == RealmGlobal {
		return BaseBillingGlobal
	}
	return BaseBillingCN
}
