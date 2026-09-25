package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// managementRegister 返回管理路由声明（/workbuddy/* 前缀 + checkin POST）。
func managementRegister() map[string]any {
	return map[string]any{
		"routes": []map[string]string{
			{"method": http.MethodGet, "path": "/workbuddy/accounts"},
			{"method": http.MethodGet, "path": "/workbuddy/models"},
			{"method": http.MethodPost, "path": "/workbuddy/quota"},
			{"method": http.MethodPost, "path": "/workbuddy/checkin"},
			{"method": http.MethodGet, "path": "/workbuddy/last-checkin"},
			{"method": http.MethodGet, "path": "/workbuddy/credits-ledger"},
		},
		"resources": []map[string]string{
			{"path": "/index.html", "menu": "WorkBuddy", "description": "WorkBuddy 管理面板"},
		},
	}
}

// managementHandle 路由管理请求到对应 handler。
func managementHandle(raw []byte) ([]byte, error) {
	var request pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return errorEnvelopeStatus("invalid_request", "invalid management request", 400), nil
	}
	path := managementPath(request.Path)
	switch {
	case path == "/workbuddy/accounts" && request.Method == http.MethodGet:
		resp, err := defaultManagementService.accountsHandler()
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/models" && request.Method == http.MethodGet:
		resp, err := defaultManagementService.modelsHandler()
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/quota" && request.Method == http.MethodPost:
		resp, err := defaultManagementService.quotaRefreshHandler(request.Body)
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/checkin" && request.Method == http.MethodPost:
		resp, err := defaultManagementService.checkinHandler(request.Body)
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/last-checkin" && request.Method == http.MethodGet:
		resp, err := defaultOpsManagementService.lastCheckinHandler()
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/credits-ledger" && request.Method == http.MethodGet:
		resp, err := defaultOpsManagementService.creditsLedgerHandler()
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/index.html" || strings.HasSuffix(path, "/workbuddy/index.html"):
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": {"text/html; charset=utf-8"}},
			Body:       workbuddyWebUI,
		})
	default:
		return errorEnvelopeStatus("not_found", "management route not found: "+path, 404), nil
	}
}

// managementResponseEnvelope 将 ManagementResponse 包装为 RPC Envelope。
// 必须直接序列化结构体本身：Body 是 []byte，宿主 rpc_client.callPlugin 用
// json.Unmarshal 解回 ManagementResponse 时对 []byte 按 base64 解码；
// 手工 "body": string(...) 传明文会触发 illegal base64 → 宿主 502。
func managementResponseEnvelope(resp pluginapi.ManagementResponse) ([]byte, error) {
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	return okEnvelope(resp)
}

// managementPath 归一化管理请求路径，去除 CPA 前缀。
func managementPath(path string) string {
	path = strings.TrimSpace(path)
	for _, prefix := range []string{
		"/v0/management",
		"/v0/resource/plugins/" + pluginID,
	} {
		if strings.HasPrefix(path, prefix+"/") {
			return strings.TrimPrefix(path, prefix)
		}
	}
	return path
}
