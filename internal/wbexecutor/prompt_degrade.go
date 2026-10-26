package wbexecutor

import (
	"encoding/json"
	"sync"
	"time"
)

// 提示词层内容拦截降级（ref workbuddy2api-hub wb_prompt.py）。
//
// 客户端（Codex / Claude Code / DeepSeek Harness 等）会在 system prompt 里注入固定
// 模板句，上游内容审核按逐字匹配时会把合法流量判为「非授权渠道调用」并拦掉
// （现网实证：2026-10-26 18:13 同一请求连打 7 个账号，全部 11128
// "Illegal API invocation from an unapproved channel"）。这类拦截与账号无关，
// 换号无用，只能从请求体消掉指纹来源。
//
// 处置：命中拦截后用一条最小中性提示词替换全部 system/developer 消息并**重试一次**；
// 同时开一个降级窗，窗内后续请求直接以中性提示词发出，避免每个请求都先撞一次拦截。
// 窗口到次日 00:00（固定 +08:00）自动结束，不续期。
//
// 与出站消息层的指纹清洗（tool_pairing/errors 等）是两层，互不替代：那层改的是
// 消息序列结构，这层改的是 system 文本本身。
const neutralSystemPrompt = "You are a helpful assistant. Respond in the user's language, " +
	"follow the user's instructions, and be direct and concise."

// ContentDegradeGate 进程内存降级窗：触发后到次日 00:00（UTC+8）为止，不续期。
// Now 为测试时间 seam（缺省 time.Now）。nil gate 恒视为未降级。
type ContentDegradeGate struct {
	mu    sync.Mutex
	until time.Time
	Now   func() time.Time
}

func NewContentDegradeGate() *ContentDegradeGate {
	return &ContentDegradeGate{Now: time.Now}
}

func (g *ContentDegradeGate) clock() time.Time {
	if g != nil && g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Active 当前是否处于降级窗内。
func (g *ContentDegradeGate) Active() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.clock().Before(g.until)
}

// Trigger 开窗（已开则不动，保证「到次日 00:00」不被后续触发续期）。
func (g *ContentDegradeGate) Trigger() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	if now.Before(g.until) {
		return
	}
	g.until = nextMidnightCST(now)
}

// degradeTriggerKind：只有内容/渠道拦截才触发提示词层降级。
// 账号级故障（ErrAccountFault）与 14018 等额度过错不在此列——那些换号有用，
// 改提示词只会掩盖真实原因。
func degradeTriggerKind(k ErrKind) bool {
	return k == ErrContentBlocked
}

// nextMidnightCST 返回 now 之后最近的 Asia/Shanghai 00:00。
// 用固定 +08:00 计算，不依赖容器时区（容器 TZ 不确定）。
func nextMidnightCST(now time.Time) time.Time {
	offset := 8 * 3600
	sec := now.Unix() + int64(offset)
	day := sec/86400 + 1
	return time.Unix(day*86400-int64(offset), 0)
}

// rewriteSystemNeutral 把 payload.messages 里的 system/developer 全部替换成一条
// 中性 system 并置于队首，其余消息逐字保留。解析失败时原样返回（绝不猜）。
func rewriteSystemNeutral(payload []byte) []byte {
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	rawMessages, ok := obj["messages"].([]any)
	if !ok {
		return payload
	}
	kept := make([]any, 0, len(rawMessages)+1)
	kept = append(kept, map[string]any{"role": "system", "content": neutralSystemPrompt})
	for _, m := range rawMessages {
		msg, ok := m.(map[string]any)
		if !ok {
			kept = append(kept, m)
			continue
		}
		switch msg["role"] {
		case "system", "developer":
			continue
		}
		kept = append(kept, msg)
	}
	obj["messages"] = kept
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}
