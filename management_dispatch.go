package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// managementRegister 返回管理路由声明（/workbuddy/* 前缀 + checkin POST）。
func managementRegister() map[string]any {
	resources := []map[string]string{{
		"path":        "/index.html",
		"menu":        "WorkBuddy",
		"description": "WorkBuddy 管理面板",
	}}
	names := make([]string, 0, len(workbuddyWebAssets)-1)
	for name := range workbuddyWebAssets {
		if name != "index.html" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		resources = append(resources, map[string]string{"path": webResourcePrefix + name})
	}
	return map[string]any{
		"routes": []map[string]string{
			{"method": http.MethodGet, "path": "/workbuddy/accounts"},
			{"method": http.MethodGet, "path": "/workbuddy/models"},
			{"method": http.MethodPost, "path": "/workbuddy/quota"},
			{"method": http.MethodPost, "path": "/workbuddy/checkin"},
			{"method": http.MethodGet, "path": "/workbuddy/last-checkin"},
			{"method": http.MethodGet, "path": "/workbuddy/credits-ledger"},
			{"method": http.MethodPost, "path": "/workbuddy/streak"},
			{"method": http.MethodPost, "path": "/workbuddy/benefits"},
			{"method": http.MethodPost, "path": "/workbuddy/benefits/claim"},
			{"method": http.MethodGet, "path": "/workbuddy/tasks"},
		},
		"resources": resources,
	}
}

// managementHandle 路由管理请求到对应 handler。
func managementHandle(raw []byte) ([]byte, error) {
	var request pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return errorEnvelopeStatus("invalid_request", "invalid management request", 400), nil
	}
	if asset, ok := webAssetFor(strings.TrimSpace(request.Path)); ok && request.Method == http.MethodGet {
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":  {asset.contentType},
				"Cache-Control": {"no-store"},
			},
			Body: asset.body,
		})
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
	case path == "/workbuddy/tasks" && request.Method == http.MethodGet:
		resp, err := defaultOpsManagementService.tasksHandler()
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/streak" && request.Method == http.MethodPost:
		resp, err := defaultManagementService.streakHandler(request.Body)
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/benefits" && request.Method == http.MethodPost:
		resp, err := defaultManagementService.benefitsHandler(request.Body)
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	case path == "/workbuddy/benefits/claim" && request.Method == http.MethodPost:
		resp, err := defaultManagementService.claimBenefitsHandler(request.Body)
		if err != nil {
			return errorEnvelopeStatus("internal_error", err.Error(), 500), nil
		}
		return managementResponseEnvelope(resp)
	default:
		if asset, ok := webAssetFor(path); ok && request.Method == http.MethodGet {
			return okEnvelope(pluginapi.ManagementResponse{
				StatusCode: http.StatusOK,
				Headers: http.Header{
					"Content-Type":  {asset.contentType},
					"Cache-Control": {"no-store"},
				},
				Body: asset.body,
			})
		}
		return errorEnvelopeStatus("not_found", "management route not found: "+path, 404), nil
	}
}

const webResourcePrefix = "/v0/resource/plugins/" + pluginID + "/"

// webAssetFor 只接受白名单内的精确资源名；未知、嵌套、.. 与外来前缀一律 404。
func webAssetFor(requestPath string) (webAsset, bool) {
	var name string
	switch {
	case requestPath == "/index.html":
		name = "index.html"
	case strings.HasPrefix(requestPath, webResourcePrefix):
		name = strings.TrimPrefix(requestPath, webResourcePrefix)
	default:
		return webAsset{}, false
	}
	asset, ok := workbuddyWebAssets[name]
	return asset, ok
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
