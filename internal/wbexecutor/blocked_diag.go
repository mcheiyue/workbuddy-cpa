package wbexecutor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
)

// noteChannelBlock 在 11128（Illegal API invocation from an unapproved channel）时
// 记录请求的脱敏摘要。
//
// 为什么必须记：2026-10-27 用十一组请求形状（最小 / Claude Code 指纹 / DeepSeek
// Harness 指纹 / tools / 长上下文 / 多轮 / reasoning_effort=max / 正文含裸串
// 11128 / 正文含 unapproved channel）**全部复现失败**（11/11 返回 200），判据不在
// 我们能枚举的形状里。没有真实被拦请求的特征，下一次仍然只能靠猜。
//
// 只记形状、不记内容：正文一律不落日志，system 只留长度与哈希。
func noteChannelBlock(req ExecuteRequest, err *ExecError) {
	if err == nil || !strings.Contains(err.Msg, "11128") {
		return
	}
	log.Printf("WARN: [workbuddy] channel-blocked 11128 model=%s %s", req.PublicModelID, summarizeRequest(req))
}

// summarizeRequest 输出可用于比对的一行形状摘要。sha 是 payload 全量哈希，
// 宿主把同一请求轮流打给多个账号时，各行 sha 相同即可确认是同一请求。
func summarizeRequest(req ExecuteRequest) string {
	sum := sha256.Sum256(req.Payload)
	var p struct {
		Stream     bool              `json:"stream"`
		Tools      []json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage   `json:"tool_choice"`
		Effort     json.RawMessage   `json:"reasoning_effort"`
		Messages   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return fmt.Sprintf("bytes=%d sha=%x unparsable", len(req.Payload), sum[:4])
	}
	roles := map[string]int{}
	var sys strings.Builder
	for _, m := range p.Messages {
		roles[m.Role]++
		if m.Role == "system" || m.Role == "developer" {
			var s string
			if json.Unmarshal(m.Content, &s) == nil {
				sys.WriteString(s)
			}
		}
	}
	sysText := sys.String()
	sysSum := sha256.Sum256([]byte(sysText))
	return fmt.Sprintf(
		"bytes=%d sha=%x stream=%t msgs=%d roles=%s tools=%d tool_choice=%t effort=%s sys_len=%d sys_sha=%x markers=%s has_11128=%t conv=%.8s",
		len(req.Payload), sum[:4], p.Stream, len(p.Messages), roleSummary(roles),
		len(p.Tools), isSet(p.ToolChoice), effortOf(p.Effort),
		len(sysText), sysSum[:4], matchMarkers(sysText),
		strings.Contains(string(req.Payload), "11128"), req.ConversationID)
}

func roleSummary(roles map[string]int) string {
	if len(roles) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(roles))
	for k := range roles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		name := k
		if name == "" {
			name = "?"
		}
		parts = append(parts, fmt.Sprintf("%s:%d", name, roles[k]))
	}
	return strings.Join(parts, ",")
}

func isSet(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func effortOf(raw json.RawMessage) string {
	if !isSet(raw) {
		return "none"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return "empty"
		}
		return s
	}
	return "raw"
}

// matchMarkers 只报命中的客户端指纹名，不回显命中的原文片段。
func matchMarkers(sys string) string {
	lower := strings.ToLower(sys)
	known := []struct{ name, needle string }{
		{"claude-code", "claude code"},
		{"deepseek-harness", "deepseek harness"},
		{"anthropic", "anthropic"},
		{"cursor", "cursor"},
		{"cline", "cline"},
		{"aider", "aider"},
		{"codex", "codex"},
		{"opencode", "opencode"},
		{"roo", "roo code"},
		{"windsurf", "windsurf"},
		{"copilot", "github copilot"},
	}
	var hit []string
	for _, k := range known {
		if strings.Contains(lower, k.needle) {
			hit = append(hit, k.name)
		}
	}
	if len(hit) == 0 {
		return "none"
	}
	return strings.Join(hit, ",")
}
