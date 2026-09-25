package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// idempotentCodes 幂等/不适用业务码（逐字对照 reference signin/main.go）。
var idempotentCodes = []string{"10001", "14001"}

// idempotentMarkers 幂等/不适用文案关键词。
var idempotentMarkers = []string{
	"今天已签到", "今日已签到", "已签到", "already",
	"未开启", "未开放", "已过期", "inactive",
}

// bareMarkers 裸错误回退匹配用的中文文案子集（英文短词不在裸错误上生效）。
var bareMarkers = []string{"今天已签到", "今日已签到", "已签到", "未开启", "未开放", "已过期"}

// checkinResult 签到结果。
type checkinResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	UID     string `json:"uid,omitempty"`
	Remain  *int64 `json:"remain,omitempty"`
}

// doCheckin 对单个账号执行签到。
// realm 由 caller 解析后传入，确保打对 base。
func doCheckin(client *http.Client, realm string, cred wbauth.Credential) error {
	return dailyCheckin(client, realm, cred)
}

// isAlreadyCheckin 报告错误是否表示「已签到/不适用」。
// 业务码只在带结构化信息的错误中判定；裸网络错误不误判。
func isAlreadyCheckin(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	// 先检查业务码。
	for _, code := range idempotentCodes {
		if strings.Contains(low, code) {
			return true
		}
	}
	// 结构化上游错误走全量文案匹配。
	if strings.Contains(low, "upstream") && (strings.Contains(low, "code=") || strings.Contains(low, "\"code\":")) {
		for _, m := range idempotentMarkers {
			if strings.Contains(low, strings.ToLower(m)) {
				return true
			}
		}
	}
	// 裸错误只认中文专属文案（英文短词太常见，如 address already in use）。
	for _, m := range bareMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// isSessionDead 报告错误是否为 12153 会话失效。
func isSessionDead(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "12153")
}

// checkinStatus 归一化签到状态。
func checkinStatus(err error) string {
	if err == nil {
		return "OK"
	}
	if isSessionDead(err) {
		return "AUTH_INVALID"
	}
	if isAlreadyCheckin(err) {
		return "ALREADY"
	}
	return "FAIL"
}

// checkinErrorMask 对签到错误脱敏。
func checkinErrorMask(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = tokenScrub(msg)
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// newHostHTTPClientForCheckin 为签到创建宿主 HTTP 客户端。
func newHostHTTPClientForCheckin(callbackID string) (*http.Client, error) {
	return newHostHTTPClient(callbackID)
}

// fmtCheckinResult 格式化签到结果消息。
func fmtCheckinResult(status string) string {
	switch status {
	case "OK":
		return "签到成功"
	case "ALREADY":
		return "今天已签到"
	case "AUTH_INVALID":
		return "会话已失效，需重新登录"
	default:
		return fmt.Sprintf("签到失败: %s", status)
	}
}
