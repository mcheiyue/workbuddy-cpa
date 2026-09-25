package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
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

	// Auth 系列
	case pluginabi.MethodAuthIdentifier,
		pluginabi.MethodAuthParse,
		pluginabi.MethodAuthLoginStart,
		pluginabi.MethodAuthLoginPoll,
		pluginabi.MethodAuthRefresh:
		return handleAuthMethod(method, raw)

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

// handleAuthMethod 分发 auth 系列 RPC 到 wbauth 包。
func handleAuthMethod(method string, raw []byte) ([]byte, error) {
	ctx := context.Background()
	var result any
	var err error
	switch method {
	case pluginabi.MethodAuthIdentifier:
		result = struct {
			Identifier string `json:"identifier"`
		}{Identifier: wbauth.Provider}
	case pluginabi.MethodAuthParse:
		result, err = handleAuthParse(raw)
	case pluginabi.MethodAuthLoginStart:
		result, err = handleAuthLoginStart(ctx, raw)
	case pluginabi.MethodAuthLoginPoll:
		result, err = handleAuthLoginPoll(ctx, raw)
	case pluginabi.MethodAuthRefresh:
		result, err = handleAuthRefresh(ctx, raw)
	}
	if err != nil {
		return errorEnvelope("auth_error", err.Error()), nil
	}
	return okEnvelope(result)
}

// rpcAuthLoginStartRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthLoginStartRequest struct {
	pluginapi.AuthLoginStartRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// rpcAuthLoginPollRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthLoginPollRequest struct {
	pluginapi.AuthLoginPollRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// rpcAuthRefreshRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthRefreshRequest struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleAuthParse(raw []byte) (pluginapi.AuthParseResponse, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	// 非 workbuddy provider → 不处理。
	if req.Provider != "" && !strings.EqualFold(req.Provider, wbauth.Provider) {
		return pluginapi.AuthParseResponse{}, nil
	}
	cred, err := wbauth.Parse(req.RawJSON)
	if err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	return pluginapi.AuthParseResponse{
		Handled: true,
		Auth:    cred.AuthData(req.FileName),
	}, nil
}

func handleAuthLoginStart(ctx context.Context, raw []byte) (pluginapi.AuthLoginStartResponse, error) {
	var req rpcAuthLoginStartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	realm := wbauth.RealmCN
	if v, ok := req.Metadata["realm"].(string); ok {
		realm = wbauth.ResolveRealm(v, "")
	}
	start, err := wbauth.Start(ctx, client, wbauth.RealmBase(realm), wbauth.RealmOrigin(realm))
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	return pluginapi.AuthLoginStartResponse{
		Provider: wbauth.Provider,
		URL:      start.AuthURL,
		State:    start.State,
	}, nil
}

func handleAuthLoginPoll(ctx context.Context, raw []byte) (pluginapi.AuthLoginPollResponse, error) {
	var req rpcAuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	realm := wbauth.RealmCN
	if v, ok := req.Metadata["realm"].(string); ok {
		realm = wbauth.ResolveRealm(v, "")
	}
	poll, err := wbauth.Poll(ctx, client, wbauth.RealmBase(realm), wbauth.RealmOrigin(realm), realm, req.State)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	if poll.Pending {
		return pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for login",
		}, nil
	}
	return pluginapi.AuthLoginPollResponse{
		Status: pluginapi.AuthLoginStatusSuccess,
		Auth:   poll.Credential.AuthData(""),
	}, nil
}

func handleAuthRefresh(ctx context.Context, raw []byte) (pluginapi.AuthRefreshResponse, error) {
	var req rpcAuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	cred, err := wbauth.Parse(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	refreshed, err := wbauth.Refresh(ctx, client, wbauth.RealmBase(wbauth.ResolveRealm(cred.Realm, cred.Domain)), wbauth.RealmOrigin(wbauth.ResolveRealm(cred.Realm, cred.Domain)), cred)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	return pluginapi.AuthRefreshResponse{
		Auth:             refreshed.Credential.AuthData(""),
		NextRefreshAfter: refreshed.NextRefreshAfter,
	}, nil
}
