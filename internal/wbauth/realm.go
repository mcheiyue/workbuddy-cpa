package wbauth

import "strings"

// Realm 枚举常量。
const (
	RealmCN     = "cn"
	RealmGlobal = "global"
)

// 端点基础 URL。
const (
	BaseCN     = "https://copilot.tencent.com"
	BaseGlobal = "https://www.workbuddy.ai"
)

// Origin/Referer 头值。
const (
	OriginCN     = "https://www.codebuddy.cn"
	OriginGlobal = "https://www.workbuddy.ai"
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
