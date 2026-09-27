package wbexecutor

import "encoding/json"

// noteUsageJSON 从上游响应/chunk JSON 顶层提取 usage.credit 消耗观测并回调
// cfg.OnUsage（C4）。语义对齐 ref handler_cost P0 与 pool.NoteModelCost：
//   - usage 缺失、credit 字段缺失 → 不记（缺失≠0，防零成本污染账本）；
//   - credit=0 是合法观测（免费模型）照记；
//   - tokens 取 total_tokens，缺失时回退 prompt+completion；tokens<=0 不记。
func noteUsageJSON(cfg Config, authID, model string, raw []byte) {
	if cfg.OnUsage == nil || authID == "" || model == "" {
		return
	}
	var parsed struct {
		Usage *struct {
			Credit           *float64 `json:"credit"`
			TotalTokens      *int     `json:"total_tokens"`
			PromptTokens     *int     `json:"prompt_tokens"`
			CompletionTokens *int     `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Usage == nil || parsed.Usage.Credit == nil {
		return
	}
	tokens := 0
	if parsed.Usage.TotalTokens != nil {
		tokens = *parsed.Usage.TotalTokens
	} else {
		if parsed.Usage.PromptTokens != nil {
			tokens += *parsed.Usage.PromptTokens
		}
		if parsed.Usage.CompletionTokens != nil {
			tokens += *parsed.Usage.CompletionTokens
		}
	}
	if tokens <= 0 {
		return
	}
	cfg.OnUsage(authID, model, *parsed.Usage.Credit, tokens)
}
