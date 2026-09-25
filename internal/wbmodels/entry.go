// Package wbmodels 提供 WorkBuddy 模型目录获取、解析和 per-AuthID 动态反向映射。
//
// 模型目录来源：上游 /v3/config（按 realm 切 base）。
// 公开 ID 生成：优先上游 display name；冲突时生成稳定可逆后缀。
// 反向映射：per-AuthID 隔离，RWMutex 并发安全。
package wbmodels

import (
	"encoding/json"
	"fmt"
	"strings"
)

// provider 是 WorkBuddy 在 CPA 中的 provider 标识，用于公开 ID 前缀。
const provider = "workbuddy"

// ModelInfo 是上游单条模型的解析形态，对应 /v3/config 的 data.models[] 条目。
// 字段语义严格对齐 qoder-cpa 已验证结论：ID=上游内部 key，Name=上游展示名。
type ModelInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Disabled        bool     `json:"disabled"`
	MaxInputTokens  int64    `json:"maxInputTokens"`
	MaxOutputTokens int64    `json:"maxOutputTokens"`
	Tags            []string `json:"tags"`
	SupportsImages  bool     `json:"supportsImages"`
	SupportsReason  bool     `json:"supportsReasoning"`
	SupportsTool    bool     `json:"supportsToolCall"`
	OnlyReasoning   bool     `json:"onlyReasoning"`
	Reasoning       struct {
		Effort           string   `json:"effort"`
		SupportedEfforts []string `json:"supportedEfforts"`
		DefaultEffort    string   `json:"defaultEffort"`
	} `json:"reasoning"`
}

// NonChatModel 判定是否非对话模型（应从模型列表过滤）。
// 三类规则：id 前缀 nes-/completion-/codewise-；maxOutputTokens ≤ 256；tags 含 text-to-image。
func NonChatModel(id string, maxOutputTokens int64, tags []string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range [...]string{"nes-", "completion-", "codewise-"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	if maxOutputTokens > 0 && maxOutputTokens <= 256 {
		return true
	}
	for _, t := range tags {
		if t == "text-to-image" {
			return true
		}
	}
	return false
}

// v3ConfigResponse 是 /v3/config 的顶层 JSON 响应结构。
type v3ConfigResponse struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

// parseConfigModels 解析 /v3/config 响应，返回过滤后的 ModelInfo 列表。
// 只接受 code=0；data 为对象形态 {models:[...]}。
func parseConfigModels(raw []byte) ([]ModelInfo, error) {
	var env v3ConfigResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("model_catalog_parse: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("model_catalog_code=%d", env.Code)
	}
	var obj struct {
		Models []ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(env.Data, &obj); err != nil {
		return nil, fmt.Errorf("model_catalog_data_parse: %w", err)
	}
	out := make([]ModelInfo, 0, len(obj.Models))
	for _, m := range obj.Models {
		id := m.ID
		if id == "" {
			id = m.Name
		}
		if id == "" || m.Disabled {
			continue
		}
		if NonChatModel(id, m.MaxOutputTokens, m.Tags) {
			continue
		}
		m.ID = id
		out = append(out, m)
	}
	return out, nil
}

// DisplayNameForModel 返回模型的展示名：优先上游 Name（即 display name），
// 缺失时回退到 ID（内部 key）。
func DisplayNameForModel(id, upstreamName string) string {
	if name := strings.TrimSpace(upstreamName); name != "" {
		return name
	}
	return strings.TrimSpace(id)
}

// PublicModelID 生成公开模型 ID（provider/name 格式）。
func PublicModelID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return provider + "/" + name
}

// UniquePublicModelID 生成不冲突的公开 ID。映射冲突时先尝试用内部 key，
// 再递增后缀。mapping 在同一批次构建过程中传入（原子快照由 Registry 负责）。
func UniquePublicModelID(name, internalID string, mapping map[string]string) string {
	preferred := PublicModelID(name)
	if preferred == "" {
		return ""
	}
	if existing, ok := mapping[preferred]; !ok || existing == internalID {
		return preferred
	}
	fallback := PublicModelID(internalID)
	if existing, ok := mapping[fallback]; !ok || existing == internalID {
		return fallback
	}
	for suffix := 2; ; suffix++ {
		candidate := PublicModelID(internalID + "-" + fmt.Sprint(suffix))
		if _, exists := mapping[candidate]; !exists {
			return candidate
		}
	}
}

// FilterAndBuild 从 ModelInfo 列表构建映射表和 ModelInfo 条目列表。
// 去重 key = 内部 ID（id 字段），目录顺序变化不影响结果。
// 返回：mapping（公开 ID→内部 ID）、entries（ModelInfo 列表，ID 已替换为公开 ID）。
func FilterAndBuild(models []ModelInfo) (mapping map[string]string, entries []ModelInfo) {
	mapping = make(map[string]string, len(models))
	entries = make([]ModelInfo, 0, len(models))
	seenInternal := make(map[string]struct{}, len(models))

	for _, m := range models {
		internalID := strings.TrimSpace(m.ID)
		if internalID == "" {
			// id 为空时回退到 name 作为内部 key（与 parseConfigModels 一致）
			internalID = strings.TrimSpace(m.Name)
		}
		if internalID == "" {
			continue
		}
		if _, exists := seenInternal[internalID]; exists {
			continue
		}
		seenInternal[internalID] = struct{}{}

		name := DisplayNameForModel(internalID, m.Name)
		publicID := UniquePublicModelID(name, internalID, mapping)
		if publicID == "" {
			continue
		}
		mapping[publicID] = internalID

		out := m
		out.ID = publicID
		entries = append(entries, out)
	}
	return mapping, entries
}
