package wbexecutor

import (
	"encoding/json"
	"log"
	"strings"
)

var effortRank = map[string]int{
	"off": 0, "minimal": 1, "low": 2, "medium": 3,
	"high": 4, "xhigh": 5, "max": 6,
}

func normalizeReasoningEffort(obj map[string]any, supported []string) {
	if len(supported) == 0 {
		return
	}
	key := ""
	if _, ok := obj["reasoning_effort"]; ok {
		key = "reasoning_effort"
	} else if _, ok := obj["reasoningEffort"]; ok {
		key = "reasoningEffort"
	} else {
		return
	}
	req, ok := obj[key].(string)
	if !ok {
		return
	}
	req = strings.TrimSpace(strings.ToLower(req))
	reqRank, ok := effortRank[req]
	if !ok {
		return
	}

	best, bestRank := "", -1
	for _, candidate := range supported {
		rank, known := effortRank[strings.TrimSpace(strings.ToLower(candidate))]
		if known && rank <= reqRank && rank > bestRank {
			best, bestRank = candidate, rank
		}
	}
	if best != "" {
		if !strings.EqualFold(best, req) {
			obj[key] = best
			log.Printf("WARN: [workbuddy] reasoning_effort downgraded %s -> %s", req, best)
		}
		return
	}

	lowest, lowestRank := "", 1<<30
	for _, candidate := range supported {
		rank, known := effortRank[strings.TrimSpace(strings.ToLower(candidate))]
		if known && rank < lowestRank {
			lowest, lowestRank = candidate, rank
		}
	}
	if lowest != "" {
		obj[key] = lowest
		log.Printf("WARN: [workbuddy] reasoning_effort floored %s -> %s", req, lowest)
	}
}

func setModel(body []byte, model string, supported []string) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj["model"] = model
	normalizeReasoningEffort(obj, supported)
	return json.Marshal(obj)
}

// translateMaxCompletionTokens 把 OpenAI 别名 max_completion_tokens 翻译为上游
// 认的 max_tokens。显式 max_tokens 优先；别名非正数值不翻译。
// ref: payload.go:translateMaxCompletionTokens
func translateMaxCompletionTokens(obj map[string]any) {
	alias, has := obj["max_completion_tokens"]
	delete(obj, "max_completion_tokens")
	if !has {
		return
	}
	if _, explicit := obj["max_tokens"]; explicit {
		return
	}
	switch v := alias.(type) {
	case float64:
		if v > 0 && v == float64(int64(v)) {
			obj["max_tokens"] = int64(v)
		}
	case int64:
		if v > 0 {
			obj["max_tokens"] = v
		}
	case int:
		if v > 0 {
			obj["max_tokens"] = int64(v)
		}
	}
}

// normalizeToolChoice 按上游 Go struct（string 类型）改写 OpenAI tool_choice。
// "none" → 删 tool_choice + 删 tools；对象形式 → 字符串。
// ref: payload.go:normalizeToolChoice
func normalizeToolChoice(obj map[string]any) {
	tc, present := obj["tool_choice"]
	if !present {
		return
	}
	switch v := tc.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			delete(obj, "tool_choice")
			delete(obj, "tools")
			delete(obj, "functions")
		}
	case map[string]any:
		typ, _ := v["type"].(string)
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "none":
			delete(obj, "tool_choice")
			delete(obj, "tools")
			delete(obj, "functions")
		case "auto", "required":
			obj["tool_choice"] = typ
		case "function":
			name := ""
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
			if name == "" {
				name, _ = v["name"].(string)
			}
			if name = strings.TrimSpace(name); name != "" {
				obj["tool_choice"] = name
			} else {
				obj["tool_choice"] = "auto"
			}
		default:
			delete(obj, "tool_choice")
		}
	default:
		delete(obj, "tool_choice")
	}
}

// normalizeRoles 把 messages 里的 developer 角色归一为 system。
// ref: payload.go:normalizeRoles
func normalizeRoles(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, ok := msg["role"].(string)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(role), "developer") {
			msg["role"] = "system"
		}
	}
}

// ensureLeadingSystem 保证 messages[0] 为 system：上游 11128 实测拒首条非 system
// （ref buildMessages/Orchids 04753c4c）。首条已是 system 则零改动；中段存在首个
// system 则挪到首位（content 含数组形态原样搬移，其余消息相对顺序不变）；全无则
// 注入 Orchids defaultSystem。多个 system 不合并（合并语义无我方实证，触发式保留）。
func ensureLeadingSystem(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	leadRole, _ := firstRole(msgs[0])
	if leadRole == "system" {
		return
	}
	for i, m := range msgs {
		if r, _ := firstRole(m); r == "system" {
			rest := make([]any, 0, len(msgs))
			rest = append(rest, m)
			rest = append(rest, msgs[:i]...)
			rest = append(rest, msgs[i+1:]...)
			obj["messages"] = rest
			return
		}
	}
	obj["messages"] = append([]any{map[string]any{
		"role": "system", "content": "You are a helpful assistant.",
	}}, msgs...)
}

func firstRole(m any) (string, bool) {
	mm, ok := m.(map[string]any)
	if !ok {
		return "", false
	}
	r, _ := mm["role"].(string)
	return r, true
}
