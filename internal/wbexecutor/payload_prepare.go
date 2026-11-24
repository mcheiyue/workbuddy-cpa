package wbexecutor

import "encoding/json"

// PreparePayload 对下游 chat completion JSON 做上游适配改写：
//  1. 强制 stream:true（上游拒绝非流式）
//  2. max_completion_tokens → max_tokens 翻译（上游只认 max_tokens）
//  3. stream_options: {include_usage: true} 注入（末帧返回 usage）
//  4. tool_choice 归一化（上游该字段是 string，对象形式 400）
//  5. developer 角色 → system（上游 role 白名单不含 developer）
//
// ref: workbuddy2api/internal/upstream/payload.go
func PreparePayload(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	normalizeToolSchemaPatterns(obj)
	translateMaxCompletionTokens(obj)
	// Orchids 92cc1391：上游 working shape 缺省 max_tokens=8192；显式值保留。
	if _, has := obj["max_tokens"]; !has {
		obj["max_tokens"] = 8192
	}
	// Orchids d3b099ef：缓存选择与并行调度留给上游，顶层键省略。
	delete(obj, "prompt_cache_key")
	delete(obj, "parallel_tool_calls")
	if _, has := obj["stream_options"]; !has {
		obj["stream_options"] = map[string]any{"include_usage": true}
	}
	normalizeToolChoice(obj)
	normalizeRoles(obj)
	// E2 A3a：tool 结果紧跟 tool_calls + 孤儿清理（ref tool_pairing.go，11148 破序防）
	if msgs, ok := obj["messages"].([]any); ok {
		if repacked, changed := repackToolResultBlocks(msgs); changed {
			msgs = repacked
			obj["messages"] = msgs
		}
		if cleaned, changed := cleanupOrphanToolCalls(msgs); changed {
			obj["messages"] = cleaned
		}
	}
	sanitizeBillingHeaders(obj)
	// 必须在 normalizeRoles（developer→system）与 A3a 重排之后跑：
	// 上游 11128 实测拒首条非 system；把首个 system 挪到首位或注入缺省。
	ensureLeadingSystem(obj)
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}
