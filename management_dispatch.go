package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// managementRegister 返回管理路由声明（/workbuddy/* 前缀）。
func managementRegister() map[string]any {
	return map[string]any{
		"routes": []map[string]string{
			{"method": http.MethodGet, "path": "/workbuddy/accounts"},
			{"method": http.MethodGet, "path": "/workbuddy/models"},
			{"method": http.MethodGet, "path": "/workbuddy/quota"},
		},
		"resources": []map[string]string{
			{"path": "/index.html", "menu": "WorkBuddy"},
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
	case path == "/workbuddy/accounts":
		return notImplemented("management.accounts")
	case path == "/workbuddy/models":
		return notImplemented("management.models")
	case path == "/workbuddy/quota":
		return notImplemented("management.quota")
	case path == "/index.html" || strings.HasSuffix(path, "/workbuddy/index.html"):
		return notImplemented("management.web")
	default:
		return errorEnvelopeStatus("not_found", "management route not found: "+path, 404), nil
	}
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
