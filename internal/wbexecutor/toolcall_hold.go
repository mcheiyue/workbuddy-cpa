package wbexecutor

import (
	"bytes"
	"encoding/json"
	"sort"
)

// feed 处理一帧（已 NormalizeChunk）。无缓存且帧不含 tool_calls → 直通；
// 激活后剥离 delta.tool_calls 进聚合，finish 帧触发决策 flush。
func (h *toolCallHold) feed(cfg Config, streamID, publicModel string, chunk []byte) *ExecError {
	if !h.active && !bytes.Contains(chunk, []byte(`"tool_calls"`)) {
		return emitChunk(cfg, streamID, chunk)
	}
	var obj map[string]any
	if json.Unmarshal(chunk, &obj) != nil {
		return emitChunk(cfg, streamID, chunk)
	}
	fr := ""
	hadTC := false
	choices, _ := obj["choices"].([]any)
	for _, ci := range choices {
		c, _ := ci.(map[string]any)
		if c == nil {
			continue
		}
		if s, ok := c["finish_reason"].(string); ok && s != "" {
			fr = s
		}
		delta, _ := c["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		if tcs, _ := delta["tool_calls"].([]any); len(tcs) > 0 {
			h.merge(tcs)
			delete(delta, "tool_calls")
			hadTC = true
		}
	}
	if hadTC {
		h.active = true
	}
	if !h.active {
		// 内容文本带 "tool_calls" 字样但无真实调用 → 原帧直通。
		return emitChunk(cfg, streamID, chunk)
	}
	if fr != "" {
		finishJSON, err := json.Marshal(obj)
		if err != nil {
			finishJSON = nil
		}
		return h.flush(cfg, streamID, publicModel, fr == "length", finishJSON)
	}
	if !frameWorthEmitting(obj) {
		return nil // 纯 tool_calls 帧剥离后的空壳：吞掉，调用随 flush 放行。
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return emitChunk(cfg, streamID, chunk)
	}
	return emitChunk(cfg, streamID, out)
}

// flush 放行缓存的聚合 tool_calls（filter=true 时剔半截 arguments），随后（如有）finish 帧。
func (h *toolCallHold) flush(cfg Config, streamID, publicModel string, filter bool, finishJSON []byte) *ExecError {
	if h.active && len(h.calls) > 0 {
		idxs := append([]int(nil), h.order...)
		sort.Ints(idxs)
		kept := make([]aggregateToolCall, 0, len(idxs))
		for _, idx := range idxs {
			c := h.calls[idx]
			if filter && isTruncatedArguments(c.Function.Arguments) {
				continue
			}
			kept = append(kept, *c)
		}
		if len(kept) > 0 {
			if execErr := emitChunk(cfg, streamID, syntheticToolCallChunk(publicModel, kept)); execErr != nil {
				return execErr
			}
		}
	}
	if finishJSON != nil {
		if execErr := emitChunk(cfg, streamID, finishJSON); execErr != nil {
			return execErr
		}
	}
	h.reset()
	return nil
}

// merge 把一帧的 tool_calls 片段并入聚合：index 分槽，id/type/name 取首个非空，
// arguments 按到达顺序拼接（与客户端聚合语义一致）。
func (h *toolCallHold) merge(tcs []any) {
	if h.calls == nil {
		h.calls = map[int]*aggregateToolCall{}
	}
	for _, raw := range tcs {
		tc, _ := raw.(map[string]any)
		if tc == nil {
			continue
		}
		idx := 0
		switch v := tc["index"].(type) {
		case float64:
			idx = int(v)
		case int:
			idx = v
		}
		c, ok := h.calls[idx]
		if !ok {
			c = &aggregateToolCall{Index: idx}
			h.calls[idx] = c
			h.order = append(h.order, idx)
		}
		if id, _ := tc["id"].(string); id != "" {
			c.ID = id
		}
		if typ, _ := tc["type"].(string); typ != "" {
			c.Type = typ
		}
		if fn, _ := tc["function"].(map[string]any); fn != nil {
			if name, _ := fn["name"].(string); name != "" {
				c.Function.Name = name
			}
			if args, _ := fn["arguments"].(string); args != "" {
				c.Function.Arguments += args
			}
		}
	}
}

func (h *toolCallHold) reset() {
	h.active = false
	h.calls = nil
	h.order = nil
}

// syntheticToolCallChunk 把聚合结果打成单帧（与决策后放行的 finish 帧相邻）。
func syntheticToolCallChunk(publicModel string, kept []aggregateToolCall) []byte {
	tcs := make([]any, 0, len(kept))
	for _, c := range kept {
		tcs = append(tcs, map[string]any{
			"index": c.Index,
			"id":    c.ID,
			"type":  c.Type,
			"function": map[string]any{
				"name":      c.Function.Name,
				"arguments": c.Function.Arguments,
			},
		})
	}
	b, err := json.Marshal(map[string]any{
		"model": publicModel,
		"choices": []any{map[string]any{
			"index": float64(0),
			"delta": map[string]any{
				"role":       "assistant",
				"tool_calls": tcs,
			},
		}},
	})
	if err != nil {
		return nil
	}
	return b
}

// frameWorthEmitting 判定剥离 tool_calls 后的帧是否还有内容（delta 非空或带 usage）。
func frameWorthEmitting(obj map[string]any) bool {
	if _, has := obj["usage"]; has {
		return true
	}
	choices, _ := obj["choices"].([]any)
	for _, ci := range choices {
		c, _ := ci.(map[string]any)
		if c == nil {
			continue
		}
		if delta, _ := c["delta"].(map[string]any); len(delta) > 0 {
			return true
		}
	}
	return false
}

func emitChunk(cfg Config, streamID string, chunk []byte) *ExecError {
	if chunk == nil {
		return nil
	}
	if err := cfg.StreamEmit(streamID, chunk); err != nil {
		return &ExecError{Kind: ErrClient, Status: 0, Msg: "stream emit failed"}
	}
	return nil
}
