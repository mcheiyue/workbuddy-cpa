package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// managementAccount 管理面板账号条目（脱敏：无 token/device_token）。
type managementAccount struct {
	AuthIndex   string `json:"auth_index"`
	UIDTail     string `json:"uid_tail"`
	Nickname    string `json:"nickname"`
	Realm       string `json:"realm"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	LastCheckin string `json:"last_checkin,omitempty"`
	QuotaError  string `json:"quota_error,omitempty"`
	// B1/挂起#4：12153 连续计次与禁用态（内存态，见 ops_disable.go）。
	Disabled       bool   `json:"disabled,omitempty"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	DeadCount      int    `json:"dead_count,omitempty"`
	// File 是宿主 auth 文件名（PATCH /auth-files/fields 的 name 定位键），空表示无法定位。
	File string `json:"file,omitempty"`
	// Priority 是宿主路由优先级（数值越大越优先，缺省 0）；不带 omitempty，0 必须回显。
	Priority int `json:"priority"`
}

// managementQuotaResp 配额刷新响应。
type managementQuotaResp struct {
	AuthIndex string `json:"auth_index"`
	Remain    int64  `json:"remain"`
	Used      int64  `json:"used"`
	Size      int64  `json:"size"`
	Packages  int    `json:"packages"`
	Error     string `json:"error,omitempty"`
}

// managementCheckinResp 签到响应。
type managementCheckinResp struct {
	UID     string `json:"uid,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Remain  *int64 `json:"remain,omitempty"`
}

// managementStreakResp 连登天数与领奖档位状态（C3 展示；脱敏无 token）。
type managementStreakResp struct {
	AuthIndex string `json:"auth_index"`
	Days      int    `json:"days"`
	Tier7d    string `json:"tier_7d,omitempty"`
	Tier14d   string `json:"tier_14d,omitempty"`
	Tier28d   string `json:"tier_28d,omitempty"`
	Error     string `json:"error,omitempty"`
}

// uidTail 返回 UID 尾号（最后 4 位），用于脱敏展示。
func uidTail(uid string) string {
	uid = strings.TrimSpace(uid)
	if len(uid) <= 4 {
		return uid
	}
	return "..." + uid[len(uid)-4:]
}

// fallbackLabel 判断标签是否为空或 provider 兜底名（保活写回路径的重复注册记录
// metadata 无 email，label 落 provider）——去重时用于把兜底名升级为真实昵称。
func fallbackLabel(label string) bool {
	label = strings.TrimSpace(label)
	return label == "" || strings.EqualFold(label, wbauth.Provider)
}

// jsonManagementResponse 构造 JSON 管理响应。
func jsonManagementResponse(status int, value any) (pluginapi.ManagementResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       body,
	}, nil
}

// jsonManagementError 构造错误管理响应。
func jsonManagementError(status int, message string) pluginapi.ManagementResponse {
	resp, _ := jsonManagementResponse(status, map[string]string{"error": message})
	return resp
}
