package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
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

// uidTail 返回 UID 尾号（最后 4 位），用于脱敏展示。
func uidTail(uid string) string {
	uid = strings.TrimSpace(uid)
	if len(uid) <= 4 {
		return uid
	}
	return "..." + uid[len(uid)-4:]
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
