package wbexecutor

// E2 A3a：请求侧工具配对重排 + 孤儿清理。
// ref: workbuddy2api/internal/upstream/tool_pairing.go（逐语义同构）
//
// WorkBuddy（Codex 系）会在 assistant.tool_calls 与其 tool 结果之间插入 developer 消息，
// 上游判序列错 11148 tool_call_sequence_broken 顶死会话。
//   - repackToolResultBlocks：把插队消息后移到整组结果之后；
//   - cleanupOrphanToolCalls：按 keepCalls（调用与结果 id 双侧齐全的集合）对称裁剪——
//     修历史 bug：批内只回部分结果时不能删空整个 tool_calls 键（留下孤儿结果仍 11148）。
// 无改动时返回原 slice（changed=false），下游按值写回不动对象。

func repackToolResultBlocks(msgs []any) ([]any, bool) {
	if len(msgs) == 0 {
		return msgs, false
	}
	out := make([]any, 0, len(msgs))
	changed := false
	i := 0
	for i < len(msgs) {
		m, ok := msgs[i].(map[string]any)
		if !ok || m["role"] != "assistant" {
			out = append(out, msgs[i])
			i++
			continue
		}
		tcs, hasCalls := m["tool_calls"].([]any)
		if !hasCalls || len(tcs) == 0 {
			out = append(out, msgs[i])
			i++
			continue
		}
		want := map[string]bool{}
		for _, tci := range tcs {
			if tc, ok := tci.(map[string]any); ok {
				if id, _ := tc["id"].(string); id != "" {
					want[id] = true
				}
			}
		}
		out = append(out, msgs[i])
		i++
		var results []any
		var between []any
		sawNonTool := false
		for i < len(msgs) {
			mm, ok := msgs[i].(map[string]any)
			if !ok {
				break
			}
			role, _ := mm["role"].(string)
			if role == "tool" {
				id, _ := mm["tool_call_id"].(string)
				if !want[id] {
					break
				}
				results = append(results, msgs[i])
				if sawNonTool {
					changed = true
				}
				i++
				continue
			}
			if len(results) == 0 {
				break
			}
			// 下一个 assistant.tool_calls 块的头不能被吃进本组 between。
			if role == "assistant" {
				if next, _ := mm["tool_calls"].([]any); len(next) > 0 {
					break
				}
			}
			between = append(between, msgs[i])
			sawNonTool = true
			i++
		}
		out = append(out, results...)
		out = append(out, between...)
	}
	if !changed {
		return msgs, false
	}
	return out, true
}

func cleanupOrphanToolCalls(msgs []any) ([]any, bool) {
	if len(msgs) == 0 {
		return msgs, false
	}
	callIDs := map[string]bool{}
	resultIDs := map[string]bool{}
	hasTraffic := false
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch msg["role"] {
		case "tool":
			if id, ok := msg["tool_call_id"].(string); ok && id != "" {
				resultIDs[id] = true
				hasTraffic = true
			}
		case "assistant":
			if tcs, ok := msg["tool_calls"].([]any); ok {
				for _, tci := range tcs {
					tc, ok := tci.(map[string]any)
					if !ok {
						continue
					}
					if id, ok := tc["id"].(string); ok && id != "" {
						callIDs[id] = true
						hasTraffic = true
					}
				}
			}
		}
	}
	if !hasTraffic {
		return msgs, false
	}
	keepCalls := map[string]bool{}
	for id := range callIDs {
		if resultIDs[id] {
			keepCalls[id] = true
		}
	}
	changed := false
	// 1) assistant.tool_calls 按 keepCalls 对称裁剪（只留配对齐全的调用）。
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		tcs, ok := msg["tool_calls"].([]any)
		if !ok || len(tcs) == 0 {
			continue
		}
		keptCalls := make([]any, 0, len(tcs))
		for _, tci := range tcs {
			tc, ok := tci.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := tc["id"].(string); keepCalls[id] {
				keptCalls = append(keptCalls, tc)
			}
		}
		if len(keptCalls) == len(tcs) {
			continue
		}
		changed = true
		if len(keptCalls) == 0 {
			delete(msg, "tool_calls")
			continue
		}
		msg["tool_calls"] = keptCalls
	}
	// 2) role:tool 结果只在对应调用保留时留下（孤儿结果删除）。
	kept := make([]any, 0, len(msgs))
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			kept = append(kept, m)
			continue
		}
		if role, _ := msg["role"].(string); role == "tool" {
			id, _ := msg["tool_call_id"].(string)
			if !keepCalls[id] {
				changed = true
				continue
			}
		}
		kept = append(kept, m)
	}
	if !changed {
		return msgs, false
	}
	return kept, true
}
