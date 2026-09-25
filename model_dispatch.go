package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/mcheiyue/workbuddy-cpa/internal/wbmodels"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// defaultRegistry 是全局唯一模型注册表（包级实例），并发安全。
var defaultRegistry = wbmodels.NewRegistry()

func handleModelMethod(method string, raw []byte) ([]byte, error) {
	var result pluginapi.ModelResponse
	var err error
	switch method {
	case pluginabi.MethodModelStatic:
		result = staticModels()
	case pluginabi.MethodModelForAuth:
		result, err = modelsForAuth(context.Background(), raw)
	}
	if err != nil {
		return errorEnvelope("model_error", err.Error()), nil
	}
	return okEnvelope(result)
}

// staticModels 返回静态模型列表（WorkBuddy 无静态基底，返回空列表）。
func staticModels() pluginapi.ModelResponse {
	return pluginapi.ModelResponse{Provider: wbauth.Provider}
}

// rpcAuthModelRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthModelRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// modelsForAuth 解析请求中的凭据，经宿主 HTTP GET /v3/config 获取模型目录，
// 注册到 per-Auth Registry 并返回 pluginapi.ModelResponse。
func modelsForAuth(ctx context.Context, raw []byte) (pluginapi.ModelResponse, error) {
	var req rpcAuthModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.ModelResponse{}, err
	}
	if req.AuthProvider != "" && !strings.EqualFold(req.AuthProvider, wbauth.Provider) {
		return pluginapi.ModelResponse{}, fmt.Errorf("unsupported auth provider %q", req.AuthProvider)
	}
	cred, err := wbauth.Parse(req.StorageJSON)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
	authID := strings.TrimSpace(req.AuthID)
	if authID == "" {
		authID = wbmodels.AuthIDFromCredential(cred)
	}
	models, err := wbmodels.FetchAndRegister(ctx, client, realm, cred.AccessToken, authID, defaultRegistry)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	return pluginapi.ModelResponse{
		Provider: wbauth.Provider,
		Models:   toPluginModelInfo(models),
	}, nil
}

// toPluginModelInfo 将 wbmodels.ModelInfo 转换为 pluginapi.ModelInfo。
func toPluginModelInfo(models []wbmodels.ModelInfo) []pluginapi.ModelInfo {
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		info := pluginapi.ModelInfo{
			ID:                m.ID,
			Object:            "model",
			OwnedBy:           wbauth.Provider,
			Name:              m.ID,
			DisplayName:       wbmodels.DisplayNameForModel(m.ID, m.Name),
			SupportedGenerationMethods: []string{"chat-completions"},
		}
		if m.SupportsReason {
			info.Thinking = &pluginapi.ThinkingSupport{ZeroAllowed: true}
		}
		if m.MaxInputTokens > 0 {
			info.InputTokenLimit = m.MaxInputTokens
		}
		out = append(out, info)
	}
	return out
}
