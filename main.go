package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const abiVersion = pluginabi.ABIVersion

func main() {}

// handleMethod 分发宿主 RPC 调用到对应 handler。
func handleMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	// 插件生命周期
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(registration())
	case pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		return notImplemented(method)

	// Management API
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegister())
	case pluginabi.MethodManagementHandle:
		return managementHandle(raw)

	// Auth 系列（P0 占位）
	case pluginabi.MethodAuthIdentifier,
		pluginabi.MethodAuthParse,
		pluginabi.MethodAuthLoginStart,
		pluginabi.MethodAuthLoginPoll,
		pluginabi.MethodAuthRefresh:
		return notImplemented(method)

	// Model 系列（P0 占位）
	case pluginabi.MethodModelStatic,
		pluginabi.MethodModelForAuth:
		return notImplemented(method)

	// Executor 系列（P0 占位）
	case pluginabi.MethodExecutorIdentifier,
		pluginabi.MethodExecutorExecute,
		pluginabi.MethodExecutorExecuteStream,
		pluginabi.MethodExecutorCountTokens,
		pluginabi.MethodExecutorHTTPRequest:
		return notImplemented(method)

	// Quota 系列（P0 占位）
	case pluginabi.MethodQuotaIdentifier,
		pluginabi.MethodQuotaDescribe,
		pluginabi.MethodQuotaFetch,
		pluginabi.MethodQuotaReset:
		return notImplemented(method)

	// Usage（P0 占位）
	case pluginabi.MethodUsageHandle:
		return notImplemented(method)

	// Request lifecycle（P0 占位）
	case pluginabi.MethodRequestComplete:
		return notImplemented(method)

	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func notImplemented(method string) ([]byte, error) {
	return errorEnvelope("not_implemented", method+" not yet implemented"), nil
}

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

// callHostJSON 是可测试的宿主回调 seam，生产环境由 cabi.go 注入。
var hostJSONCall func(method string, payload any) (json.RawMessage, error)

func callHostJSON(method string, payload any) (json.RawMessage, error) {
	if hostJSONCall != nil {
		return hostJSONCall(method, payload)
	}
	return nil, simpleErr("host not connected")
}
