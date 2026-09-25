package wbexecutor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// ErrMissingTerminal 流结束时未收到 [DONE] 或有效数据帧。
// 对齐 qoder-cpa ErrMissingTerminal 模式：parser 已把无终止 EOF 转错误。
var ErrMissingTerminal = errors.New("wbexecutor: stream ended without terminal event")

// errEmptyStream 上游 200 但无有效 SSE 数据帧。
var errEmptyStream = errors.New("wbexecutor: upstream stream contained no valid data events")

// SSEEvent 解析后的 SSE 事件。
type SSEEvent struct {
	Payload []byte // 裸 JSON（不含 "data: " 前缀）
	Done    bool   // [DONE] 终止帧
}

// ParseSSELine 从原始 SSE 行提取事件。
// 返回 (payload, done, ok)：done=true 表示 [DONE]；ok=false 表示非数据行（注释/空行）。
// ref: qoder-cpa executorStreamPayload + workbuddy2api sse.go
func ParseSSELine(line string) (payload string, done, ok bool) {
	line = strings.TrimRight(line, "\r\n")
	if strings.HasPrefix(line, "data: [DONE]") {
		return "", true, true
	}
	if strings.HasPrefix(line, "data: ") {
		return strings.TrimPrefix(line, "data: "), false, true
	}
	return "", false, false
}

// aggregateResult 非流聚合结果。
type aggregateResult struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Model   string            `json:"model"`
	Choices []aggregateChoice `json:"choices"`
	Usage   any               `json:"usage,omitempty"`
}

type aggregateChoice struct {
	Index        int               `json:"index"`
	Message      aggregateMessage  `json:"message"`
	FinishReason any               `json:"finish_reason"`
}

type aggregateMessage struct {
	Role             string              `json:"role"`
	Content          string              `json:"content"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
	ToolCalls        []aggregateToolCall `json:"tool_calls,omitempty"`
}

type aggregateToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Aggregate 读取完整 SSE 流，聚合 delta 为单个 chat.completion 响应。
// ref: workbuddy2api/internal/upstream/sse.go Aggregate
func Aggregate(r io.Reader) ([]byte, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var (
		id, model    string
		created      float64
		content      strings.Builder
		reasoning    strings.Builder
		role         = "assistant"
		finishReason any
		usage        any
		validEvents  int
		sawDone      bool
		toolCalls    = map[int]*aggregateToolCall{}
		toolOrder    []int
	)
	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		payload, done, ok := ParseSSELine(trimmed)
		if done {
			sawDone = true
			break
		}
		if !ok {
			if err == io.EOF {
				break
			}
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			if err == io.EOF {
				break
			}
			continue
		}
		validEvents++
		if v, ok := chunk["id"].(string); ok && id == "" {
			id = v
		}
		if v, ok := chunk["model"].(string); ok && model == "" {
			model = v
		}
		if v, ok := chunk["created"].(float64); ok && created == 0 {
			created = v
		}
		if v, ok := chunk["usage"]; ok {
			usage = v
		}
		choices, _ := chunk["choices"].([]any)
		for _, ci := range choices {
			c, _ := ci.(map[string]any)
			if c == nil {
				continue
			}
			if fr, ok := c["finish_reason"]; ok && fr != nil {
				finishReason = fr
			}
			delta, _ := c["delta"].(map[string]any)
			if delta == nil {
				continue
			}
			if r2, ok := delta["role"].(string); ok && r2 != "" {
				role = r2
			}
			if txt, ok := delta["content"].(string); ok && txt != "" {
				content.WriteString(txt)
			}
			if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
				reasoning.WriteString(rc)
			}
			tcs, ok := delta["tool_calls"].([]any)
			if !ok {
				continue
			}
			for _, tci := range tcs {
				tc, ok := tci.(map[string]any)
				if !ok {
					continue
				}
				idx := 0
				if v, ok := tc["index"].(float64); ok {
					idx = int(v)
				}
				tool, exists := toolCalls[idx]
				if !exists {
					tool = &aggregateToolCall{Index: idx, Type: "function"}
					toolCalls[idx] = tool
					toolOrder = append(toolOrder, idx)
				}
				if v, ok := tc["id"].(string); ok && v != "" {
					tool.ID = v
				}
				if fn, ok := tc["function"].(map[string]any); ok {
					if v, ok := fn["name"].(string); ok && v != "" {
						tool.Function.Name = v
					}
					if v, ok := fn["arguments"].(string); ok && v != "" {
						tool.Function.Arguments += v
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	if validEvents == 0 {
		return nil, errEmptyStream
	}
	if !sawDone {
		return nil, ErrMissingTerminal
	}
	msg := aggregateMessage{Role: role, Content: content.String()}
	if reasoning.Len() > 0 {
		msg.ReasoningContent = reasoning.String()
	}
	if len(toolOrder) > 0 {
		sort.Ints(toolOrder)
		calls := make([]aggregateToolCall, 0, len(toolOrder))
		for _, idx := range toolOrder {
			calls = append(calls, *toolCalls[idx])
		}
		msg.ToolCalls = calls
	}
	result := aggregateResult{
		ID:     id,
		Object: "chat.completion",
		Model:  model,
		Choices: []aggregateChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: usage,
	}
	return json.Marshal(result)
}
