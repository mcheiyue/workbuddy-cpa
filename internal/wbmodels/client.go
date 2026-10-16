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

// 目录拉取 UA 的版本段：与 internal/wbexecutor/headers.go 的
// defaultClientVersion/defaultCliVersion 同源（chat 侧 UA）。本包不 import wbexecutor，
// 避免 models→executor 反向依赖；沿用 billing_http.go「逐字对照 + 注释」的既有惯例。
const (
	catalogClientVersion = "5.5.6"
	catalogCliVersion    = "2.137.1"
	// catalogLegacyCliUA 是历史沿用的纯 CLI UA，保持逐字不变以维持既有目录内容与顺序。
	catalogLegacyCliUA = "CLI/2.63.2 CodeBuddy/2.63.2"
)

// catalogUserAgentsFor 返回该 realm 的目录拉取 UA 列表（顺序即合并优先级）。
// 上游按 UA 段门控目录规模，两种 UA 各能拿到对方没有的模型——实测 2026-10-16：
// global 纯 CLI 20 个 / WorkBuddy 27 个（纯增 7）；cn 纯 CLI 33 个 / WorkBuddy 60 个，
// 但 cn 的 CLI 独有 default/glm-5.0/glm-4.7 在 WorkBuddy UA 下消失。故必须取并集，
// 不能替换。CLI 放首位，保证重叠模型沿用既有展示名（公开 ID 由展示名派生）。
func catalogUserAgentsFor(realm string) []string {
	platform := "WorkBuddy"
	if realm == wbauth.RealmGlobal {
		platform = "WorkBuddy AI"
	}
	return []string{
		catalogLegacyCliUA,
		"WorkBuddy/" + catalogClientVersion + " " + platform + "/" + catalogClientVersion + " CLI/" + catalogCliVersion,
	}
}

// Doer 是可注入的 HTTP 执行器接口，测试用 httptest 假上游。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// FetchConfig 发起 GET /v3/config，返回原始响应体（单 UA，保留给调用方按需使用）。
// realm 决定 base URL（cn → workbuddy.cn，global → workbuddy.ai）。
func FetchConfig(ctx context.Context, doer Doer, realm, accessToken string) ([]byte, error) {
	return fetchConfigWithUA(ctx, doer, realm, accessToken, catalogUserAgentsFor(realm)[0])
}

// fetchConfigWithUA 用指定 UA 发起 GET /v3/config。
func fetchConfigWithUA(ctx context.Context, doer Doer, realm, accessToken, userAgent string) ([]byte, error) {
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
	req.Header.Set("User-Agent", userAgent)

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
// 对 realm 的每个目录 UA 各拉一次，按内部 ID 取并集（见 catalogUserAgentsFor 的实测依据）。
// 单一 UA 失败不致命：只要有一个 UA 成功就返回其结果，全部失败才报错。
func FetchAndParse(ctx context.Context, doer Doer, realm, accessToken string) ([]ModelInfo, error) {
	uas := catalogUserAgentsFor(realm)
	merged := make([]ModelInfo, 0, 64)
	seen := make(map[string]struct{}, 64)
	var firstErr error
	ok := 0
	for _, ua := range uas {
		raw, err := fetchConfigWithUA(ctx, doer, realm, accessToken, ua)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		models, perr := parseConfigModels(raw)
		if perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		ok++
		for _, m := range models {
			if _, dup := seen[m.ID]; dup {
				continue
			}
			seen[m.ID] = struct{}{}
			merged = append(merged, m)
		}
	}
	if ok == 0 {
		return nil, firstErr
	}
	return merged, nil
}

// FetchAndRegister 获取目录、构建映射、注册到 Registry 并返回 ModelInfo 列表。
// authID 用于 per-AuthID 隔离。
func FetchAndRegister(ctx context.Context, doer Doer, realm, accessToken, authID string, reg *Registry) ([]ModelInfo, error) {
	models, err := FetchAndParse(ctx, doer, realm, accessToken)
	if err != nil {
		return nil, err
	}
	mapping, entries := FilterAndBuild(models)
	efforts := make(map[string][]string, len(entries))
	for _, entry := range entries {
		levels := append([]string(nil), entry.Reasoning.SupportedEfforts...)
		if len(levels) == 0 && strings.TrimSpace(entry.Reasoning.Effort) != "" {
			levels = []string{entry.Reasoning.Effort}
		}
		if len(levels) > 0 {
			efforts[entry.ID] = levels
		}
	}
	reg.StoreWithMeta(authID, mapping, efforts)
	return entries, nil
}

// ToPluginModels 将 ModelInfo 列表转换为 pluginapi.ModelInfo 格式。
// 由调用方（main.go 集成时）使用，此处提供纯函数供后续接线。
func ToPluginModels(provider string, models []ModelInfo) []modelOutput {
	out := make([]modelOutput, 0, len(models))
	for _, m := range models {
		mo := modelOutput{
			ID:                         m.ID,
			Object:                     "model",
			OwnedBy:                    provider,
			Name:                       m.ID, // Name = 内部 key（公开 ID 已在 FilterAndBuild 中替换）
			DisplayName:                DisplayNameForModel(m.ID, m.Name),
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
	ID                         string           `json:"id"`
	Object                     string           `json:"object"`
	OwnedBy                    string           `json:"owned_by"`
	Name                       string           `json:"name"`
	DisplayName                string           `json:"display_name"`
	InputTokenLimit            int64            `json:"input_token_limit,omitempty"`
	SupportedGenerationMethods []string         `json:"supported_generation_methods"`
	Thinking                   *thinkingSupport `json:"thinking,omitempty"`
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
