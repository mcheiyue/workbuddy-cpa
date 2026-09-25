package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// handleQuotaMethod 分发 quota 系列 RPC。
func handleQuotaMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodQuotaIdentifier:
		return okEnvelope(struct {
			Identifier string `json:"identifier"`
		}{Identifier: wbauth.Provider})
	case pluginabi.MethodQuotaDescribe:
		return okEnvelope(pluginapi.QuotaDescribeResponse{
			SupportedProviders: []string{wbauth.Provider},
			DisplayName:        "WorkBuddy",
			SupportsReset:      false,
		})
	case pluginabi.MethodQuotaFetch:
		return handleQuotaFetch(raw)
	case pluginabi.MethodQuotaReset:
		return okEnvelope(pluginapi.QuotaResetResponse{
			Success: false,
			Message: "workbuddy does not support quota reset",
		})
	default:
		return errorEnvelope("unknown_method", "unknown quota method: "+method), nil
	}
}

// rpcQuotaFetchRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcQuotaFetchRequest struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleQuotaFetch(raw []byte) ([]byte, error) {
	var req rpcQuotaFetchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorEnvelopeStatus("invalid_request", "invalid quota fetch request", http.StatusBadRequest), nil
	}
	if req.Provider != "" && !strings.EqualFold(req.Provider, wbauth.Provider) {
		return errorEnvelope("unsupported_provider", "unsupported provider: "+req.Provider), nil
	}
	cred, err := wbauth.Parse(req.StorageJSON)
	if err != nil {
		return errorEnvelope("auth_error", "invalid workbuddy credential"), nil
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return errorEnvelope("host_unavailable", err.Error()), nil
	}
	realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
	remain, used, size, packs, fetchErr := resourceSummary(client, realm, cred.AccessToken)
	if fetchErr != nil {
		msg := quotaErrorMask(fetchErr)
		return okEnvelope(pluginapi.QuotaFetchResponse{
			Summary: []pluginapi.QuotaMetric{{
				Key: "error", Label: "配额查询失败", Value: 0, Unit: msg,
			}},
		})
	}
	fraction := 0.0
	if size > 0 {
		fraction = float64(remain) / float64(size)
	}
	return okEnvelope(pluginapi.QuotaFetchResponse{
		Summary: []pluginapi.QuotaMetric{
			{Key: "remain", Label: "剩余额度", Value: float64(remain), Unit: "积分"},
			{Key: "used", Label: "已用额度", Value: float64(used), Unit: "积分"},
			{Key: "size", Label: "总量", Value: float64(size), Unit: "积分"},
			{Key: "packages", Label: "套餐数", Value: float64(packs)},
			{Key: "remainFraction", Label: "剩余额度比例", Value: fraction},
		},
	})
}

// quotaErrorMask 对上游错误脱敏：仅暴露 status/code，不泄露 token。
func quotaErrorMask(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = tokenScrub(msg)
	// 提取 status/code 信息，截断敏感内容。
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// tokenScrub 移除消息中可能泄露的 token 类凭据。
func tokenScrub(msg string) string {
	msg = bearerPat.ReplaceAllString(msg, "[REDACTED]")
	msg = tokenFieldPat.ReplaceAllString(msg, "${1}[REDACTED]")
	return msg
}

var (
	bearerPat    = regexp.MustCompile(`Bearer\s+\S+`)
	tokenFieldPat = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|device[_-]?token)["':=\s]+\S+`)
)
