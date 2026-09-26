package wbexecutor

import "encoding/json"

// NormalizeChunk 规范化上游 chunk 并注入公开 model ID，供 pumpStream 逐帧调用。
//
// 上游 WorkBuddy 序列化为全字段平铺形状：delta 每帧都带 content:""、refusal:""、
// tool_calls:[]、function_call:null、extra_fields:null，finish_reason 为 "" 而非
// null。客户端（按 key 存在性判定思考块边界的实现）会把每一帧都当成思考块边界，
// 将一次完整思考切成每 token 一个 Thought 小块。
// 规范化后与 OpenAI/qoder 惯例一致（活跃字段才输出）：delta 空值字段剥离、
// finish_reason "" → null、顶层 null usage 删除；非空值（正文、工具调用、
// 末帧 usage、finish_reason: stop）全部保留，语义零变化。
// ref: 现网 wb3 抓包（全字段平铺）+ qoder chat_sse.go omitempty 约定。
func NormalizeChunk(raw []byte, publicModel string) []byte {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	obj["model"] = publicModel
	if usage, ok := obj["usage"]; ok && usage == nil {
		delete(obj, "usage")
	}
	choices, _ := obj["choices"].([]any)
	for _, ci := range choices {
		c, _ := ci.(map[string]any)
		if c == nil {
			continue
		}
		if fr, ok := c["finish_reason"]; ok {
			if s, isStr := fr.(string); isStr && s == "" {
				c["finish_reason"] = nil
			}
		}
		delta, _ := c["delta"].(map[string]any)
		stripEmptyDelta(delta)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// stripEmptyDelta 删除 delta 中语义等价于缺省的空值：空串、null、空数组。
// 非空值（含 role、活跃 token、工具调用参数）一律保留。
func stripEmptyDelta(delta map[string]any) {
	for k, v := range delta {
		switch tv := v.(type) {
		case string:
			if tv == "" {
				delete(delta, k)
			}
		case nil:
			delete(delta, k)
		case []any:
			if len(tv) == 0 {
				delete(delta, k)
			}
		}
	}
}
