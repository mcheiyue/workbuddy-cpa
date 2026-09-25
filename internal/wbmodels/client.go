package wbmodels

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

const v3ConfigPath = "/v3/config"

// Doer 是可注入的 HTTP 执行器接口，测试用 httptest 假上游。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// FetchConfig 发起 GET /v3/config，返回原始响应体。
// realm 决定 base URL（cn → copilot.tencent.com，global → www.workbuddy.ai）。
func FetchConfig(ctx context.Context, doer Doer, realm, accessToken string) ([]byte, error) {
	base := wbauth.RealmBase(realm)
	url := base + v3ConfigPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("v3_config_request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	origin := wbauth.RealmOrigin(realm)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", "CLI/2.63.2 CodeBuddy/2.63.2")

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("v3_config_http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("v3_config_read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("v3_config_status=%d: %s", resp.StatusCode, truncate(string(raw), 120))
	}
	return raw, nil
}

// FetchAndParse 获取 /v3/config 并解析为过滤后的 ModelInfo 列表。
func FetchAndParse(ctx context.Context, doer Doer, realm, accessToken string) ([]ModelInfo, error) {
	raw, err := FetchConfig(ctx, doer, realm, accessToken)
	if err != nil {
		return nil, err
	}
	return parseConfigModels(raw)
}

// FetchAndRegister 获取目录、构建映射、注册到 Registry 并返回 ModelInfo 列表。
// authID 用于 per-AuthID 隔离。
func FetchAndRegister(ctx context.Context, doer Doer, realm, accessToken, authID string, reg *Registry) ([]ModelInfo, error) {
	models, err := FetchAndParse(ctx, doer, realm, accessToken)
	if err != nil {
		return nil, err
	}
	mapping, entries := FilterAndBuild(models)
	reg.Store(authID, mapping)
	return entries, nil
}

// ToPluginModels 将 ModelInfo 列表转换为 pluginapi.ModelInfo 格式。
// 由调用方（main.go 集成时）使用，此处提供纯函数供后续接线。
func ToPluginModels(provider string, models []ModelInfo) []modelOutput {
	out := make([]modelOutput, 0, len(models))
	for _, m := range models {
		mo := modelOutput{
			ID:                m.ID,
			Object:            "model",
			OwnedBy:           provider,
			Name:              m.ID, // Name = 内部 key（公开 ID 已在 FilterAndBuild 中替换）
			DisplayName:       DisplayNameForModel(m.ID, m.Name),
			SupportedGenerationMethods: []string{"chat-completions"},
		}
		if m.SupportsReason {
			mo.Thinking = &thinkingSupport{ZeroAllowed: true}
		}
		if m.MaxInputTokens > 0 {
			mo.InputTokenLimit = m.MaxInputTokens
		}
		out = append(out, mo)
	}
	return out
}

// modelOutput 是 ToPluginModels 的输出条目（扁平结构，避免循环依赖 pluginapi）。
type modelOutput struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	OwnedBy           string            `json:"owned_by"`
	Name              string            `json:"name"`
	DisplayName       string            `json:"display_name"`
	InputTokenLimit   int64             `json:"input_token_limit,omitempty"`
	SupportedGenerationMethods []string `json:"supported_generation_methods"`
	Thinking          *thinkingSupport  `json:"thinking,omitempty"`
}

type thinkingSupport struct {
	ZeroAllowed bool `json:"zero_allowed"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ParseConfigModels 导出 parseConfigModels 供测试直接调用。
func ParseConfigModels(raw []byte) ([]ModelInfo, error) {
	return parseConfigModels(raw)
}

// ModelForAuth 类型签名占位，供后续 model.for_auth dispatch 接线。
// 返回值语义：authID 对应的模型列表（从 Registry 读取，或触发一次 FetchAndRegister）。
type ModelForAuth func(ctx context.Context, authID string) ([]ModelInfo, error)

// AuthIDFromCredential 从凭据构造 authID（与 wbauth.AuthData.ID 对齐）。
func AuthIDFromCredential(cred wbauth.Credential) string {
	return wbauth.Provider + "-" + strings.TrimSpace(cred.UID)
}
