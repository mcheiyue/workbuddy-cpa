package wbexecutor

import (
	"regexp"
	"strings"
)

var (
	billingHeaderLineRE   = regexp.MustCompile(`(?im)^[ \t]*x-anthropic-billing-(?:header|hdr)[ \t]*:[^\r\n]*(?:\r?\n|$)`)
	billingHeaderInlineRE = regexp.MustCompile(`(?i)x-anthropic-billing-(?:header|hdr)[ \t]*:[ \t]*[^;\r\n]*(?:;[ \t]*)?`)
)

func sanitizeBillingHeaders(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	cleaned := make([]any, 0, len(msgs))
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			cleaned = append(cleaned, raw)
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" && role != "assistant" {
			cleaned = append(cleaned, raw)
			continue
		}
		content := msg["content"]
		content, changed, empty := sanitizeBillingContent(content)
		if changed {
			msg["content"] = content
		}
		if (role == "system" || role == "developer") && changed && empty {
			continue
		}
		cleaned = append(cleaned, raw)
	}
	obj["messages"] = cleaned
}

func sanitizeBillingContent(content any) (any, bool, bool) {
	switch value := content.(type) {
	case string:
		cleaned := sanitizeBillingText(value)
		changed := cleaned != value
		return cleaned, changed, changed && strings.TrimSpace(cleaned) == ""
	case []any:
		blocks := make([]any, 0, len(value))
		changed := false
		for _, raw := range value {
			block, ok := raw.(map[string]any)
			if !ok {
				blocks = append(blocks, raw)
				continue
			}
			blockType, _ := block["type"].(string)
			text, ok := block["text"].(string)
			if blockType != "text" || !ok {
				blocks = append(blocks, raw)
				continue
			}
			cleaned := sanitizeBillingText(text)
			if cleaned == text {
				blocks = append(blocks, raw)
				continue
			}
			changed = true
			if strings.TrimSpace(cleaned) == "" {
				continue
			}
			block["text"] = cleaned
			blocks = append(blocks, raw)
		}
		return blocks, changed, changed && len(blocks) == 0
	default:
		return content, false, false
	}
}

func sanitizeBillingText(text string) string {
	text = billingHeaderLineRE.ReplaceAllString(text, "")
	return billingHeaderInlineRE.ReplaceAllString(text, "")
}
