package main

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
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
		StopOpsTicker()
		return okEnvelope(nil)

	// Management API
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegister())
	case pluginabi.MethodManagementHandle:
		return managementHandle(raw)

	// Auth 系列
	case pluginabi.MethodAuthIdentifier,
		pluginabi.MethodAuthParse,
		pluginabi.MethodAuthLoginStart,
		pluginabi.MethodAuthLoginPoll,
		pluginabi.MethodAuthRefresh:
		return handleAuthMethod(method, raw)

	// Model 系列
	case pluginabi.MethodModelStatic,
		pluginabi.MethodModelForAuth:
		return handleModelMethod(method, raw)

	// Executor 系列
	case pluginabi.MethodExecutorIdentifier,
		pluginabi.MethodExecutorExecute,
		pluginabi.MethodExecutorExecuteStream,
		pluginabi.MethodExecutorCountTokens,
		pluginabi.MethodExecutorHTTPRequest:
		return handleExecutorMethod(method, raw)

	// Quota 系列
	case pluginabi.MethodQuotaIdentifier,
		pluginabi.MethodQuotaDescribe,
		pluginabi.MethodQuotaFetch,
		pluginabi.MethodQuotaReset:
		return handleQuotaMethod(method, raw)

	// Scheduler 系列
	case pluginabi.MethodSchedulerPick:
		return handleSchedulerMethod(method, raw)

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

// callHostJSON 是可测试的宿主回调 seam，生产环境由 cabi.go 注入。
var hostJSONCall func(method string, payload any) (json.RawMessage, error)

func callHostJSON(method string, payload any) (json.RawMessage, error) {
	if hostJSONCall != nil {
		return hostJSONCall(method, payload)
	}
	return nil, simpleErr("host not connected")
}
