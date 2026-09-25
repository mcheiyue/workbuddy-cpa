// Package wbexecutor 实现 WorkBuddy Chat Completions 执行器的纯逻辑核心。
// 职责：请求 payload 构造、WorkBuddy 出站头、SSE 解析、流式转发、非流聚合、业务错误分类。
// 不依赖 CPA 宿主 ABI；宿主回调通过可注入 seam 传入。
package wbexecutor

import "net/http"

// ModelResolver 将公开模型 ID 还原为上游内部 key。
// 集成步注入 wbmodels 的实现；未注入时用 identity 回退。
type ModelResolver interface {
	ResolveModel(authID, publicModelID string) (string, error)
}

// identityResolver ponytail: 公开 ID 原样当内部 key，跳过还原。
// 升级路径：注入 wbmodels 实现后删除此回退。
type identityResolver struct{}

func (identityResolver) ResolveModel(_, publicModelID string) (string, error) {
	return publicModelID, nil
}

// Config 执行器依赖注入。
type Config struct {
	// Doer HTTP 传输 seam（宿主 hostRoundTripper 或 httptest）。
	Doer func(*http.Request) (*http.Response, error)
	// StreamEmit 发送裸 JSON chunk 给宿主（禁止带 "data: " 前缀）。
	StreamEmit func(streamID string, payload []byte) error
	// StreamClose 关闭宿主流；message 非空时为错误关闭。
	StreamClose func(streamID string, message string)
	// Resolver 模型 ID 还原；nil 时用 identity 回退。
	Resolver ModelResolver
}

func (c *Config) resolver() ModelResolver {
	if c.Resolver != nil {
		return c.Resolver
	}
	return identityResolver{}
}

// Credential 执行器需要的账号信息子集（从 wbauth.Credential 映射）。
type Credential struct {
	AccessToken  string
	UID          string
	DeviceToken  string
	EnterpriseID string
	Domain       string
	Realm        string
}

// ExecuteRequest 发往执行器的请求描述。
type ExecuteRequest struct {
	AuthID         string
	PublicModelID  string
	Payload        []byte // 下游原始 chat completion JSON
	StreamID       string // 流式模式的 stream ID
	ConversationID string // X-Conversation-ID 透传
	ChatBaseURL    string // 上游 base（如 https://copilot.tencent.com）
	Cred           Credential
}
