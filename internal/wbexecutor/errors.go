package wbexecutor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrKind 错误分类，对应上游业务码和 HTTP 状态码。
type ErrKind int

const (
	ErrNone             ErrKind = iota // 成功
	ErrModelRateLimit                  // 6004 模型级限流
	ErrSessionDead                     // 12153 会话失效
	ErrBadParams                       // 11128/11101 请求参数错误
	ErrModelBlocked                    // 11102 该后端无此模型
	ErrPromptTooLong                   // 11115 prompt 超长
	ErrContentBlocked                  // 审核拦截
	ErrAccountFault                    // 账号级故障（request illegal 等）
	ErrHardCredit                      // 余额不足
	ErrSoftRate                        // 软限流
	ErrServer                          // 5xx
	ErrClient                          // 其他 4xx
	ErrDailyBudget                     // daily_budget_exceeded 当日积分预算耗尽（网关预算闸语义）
	ErrToolCallMismatch                // 11148 工具调用/结果历史错配（请求级确定性错误，换号无用，不罚号）
)

func (k ErrKind) String() string {
	switch k {
	case ErrModelRateLimit:
		return "model_rate_limit"
	case ErrSessionDead:
		return "session_dead"
	case ErrBadParams:
		return "bad_params"
	case ErrModelBlocked:
		return "model_blocked"
	case ErrPromptTooLong:
		return "prompt_too_long"
	case ErrContentBlocked:
		return "content_blocked"
	case ErrAccountFault:
		return "account_fault"
	case ErrHardCredit:
		return "hard_credit"
	case ErrSoftRate:
		return "soft_rate"
	case ErrServer:
		return "server"
	case ErrClient:
		return "client"
	case ErrDailyBudget:
		return "daily_budget_exceeded"
	case ErrToolCallMismatch:
		return "tool_call_mismatch"
	default:
		return "none"
	}
}

// ExecError 带分类的执行错误。
type ExecError struct {
	Kind          ErrKind
	Status        int
	Msg           string // 脱敏摘要，不含 token/Authorization
	RetryAfterSec int    // 上游 Retry-After 真实秒数（0=无/坏值），供降权开窗取值
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("upstream %s (http %d): %s", e.Kind, e.Status, e.Msg)
}

// ParseRetryAfter 解析 Retry-After 头（delta-seconds 或 HTTP-date）为秒数；
// 缺失/坏值/过去时间归零（对齐 qoder classify.go ParseRetryAfter 口径）。
func ParseRetryAfter(v string, now time.Time) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return secs
	}
	if t, err := http.ParseTime(v); err == nil {
		d := int(t.Sub(now).Round(time.Second).Seconds())
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// softRateResetLoc 上游 429 文案里的重置时刻固定按 UTC+8 解释（上游文案如此，
// 与容器时区无关；ref workbuddy2api client.go softRateResetLoc）。
var softRateResetLoc = time.FixedZone("UTC+8", 8*60*60)

// 文案墙钟正则（ref workbuddy2api client.go softRateResetPattern*）：CN「将在 … 重置」
// 与 EN「reset at <ts>」。预编译为包级 var，错误风暴（429 轰炸）时避免重复编译。
var (
	reSoftRateResetCN = regexp.MustCompile(`将在 (.+?) 重置`)
	reSoftRateResetEN = regexp.MustCompile(`(?i)reset at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)
)

const softRateTimeLayout = "2006-01-02 15:04:05"

// ParseRateReset 从限流文案提取上游明说的恢复时刻，返回距 now 的秒数；
// 无文案/坏时间/已过期返回 (0,false)，绝不臆造时间（调用方回退 Retry-After 头）。
// 文案是比 Retry-After 头更权威的口径（ref workbuddy2api ParseRateReset）。
func ParseRateReset(body string, now time.Time) (int, bool) {
	m := reSoftRateResetCN.FindStringSubmatch(body)
	if len(m) < 2 {
		m = reSoftRateResetEN.FindStringSubmatch(body)
	}
	if len(m) < 2 {
		return 0, false
	}
	ts := strings.TrimSuffix(strings.TrimSpace(m[1]), " UTC+8")
	t, err := time.ParseInLocation(softRateTimeLayout, ts, softRateResetLoc)
	if err != nil {
		return 0, false
	}
	secs := int(t.Sub(now).Round(time.Second).Seconds())
	if secs <= 0 {
		return 0, false
	}
	return secs, true
}

// retryAfterFor 计算降权开窗用的恢复秒数：优先取 body 文案墙钟（上游更权威），
// 无文案时回退 Retry-After 头（ref workbuddy2api applyErrorPolicy 优先级）。
func retryAfterFor(body, header string, now time.Time) int {
	if secs, ok := ParseRateReset(body, now); ok {
		return secs
	}
	return ParseRetryAfter(header, now)
}

// Classify 按 HTTP 状态码 + body 判定错误分类。
// 判定顺序对齐 workbuddy2api Classify（ref: client.go）。
func Classify(status int, body string) ErrKind {
	// 11102 最先判：「该后端无此模型」语义最具体。
	if isModelBlocked(status, body) {
		return ErrModelBlocked
	}
	if status == http.StatusPaymentRequired {
		return ErrHardCredit
	}
	lower := strings.ToLower(body)
	// 6004 模型级限流（先于通用 429 判定）。
	if status == http.StatusTooManyRequests && hasBusinessCode(body, "6004") {
		return ErrModelRateLimit
	}
	// sessionDead / accountFault 先于通用限流判定。
	if strings.Contains(body, "12153") || strings.Contains(body, "Offline user session not found") {
		return ErrSessionDead
	}
	// 11140 账号受限（request illegal / 账号异常行为被限制）与 14017 试用未激活均属
	// 账号级故障：换号有用、冷却可挡。注意 11140 也承载模型级限流文案
	// （"The model provider is rate-limiting requests."），故只认受限文案，绝不按裸码判定。
	if strings.Contains(lower, "request illegal") || strings.Contains(lower, "trial not activated") ||
		strings.Contains(lower, "restricted due to unusual activity") || strings.Contains(body, "账号存在异常行为") {
		return ErrAccountFault
	}
	// 14018 账号积分耗尽（明确业务码，非 429 专属：403/402 亦见）：
	// 先于 429 通用兜底，否则被误判为可自愈软限流。只认 code 字段值，不靠文案
	// （ref workbuddy2api client.go 第 4 层 issue #175；codebuddy-intl-cpa failover.go 要求 status>=400）。
	if status >= 400 && hasBusinessCode(body, "14018") {
		return ErrHardCredit
	}
	// daily_budget_exceeded 当日积分预算闸（ref workbuddy2api deff7ae），独立于通用 429 兜底。
	if strings.Contains(body, "daily_budget_exceeded") {
		return ErrDailyBudget
	}
	// 限流文案（通用 fallback）。
	if status == http.StatusTooManyRequests || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") {
		return ErrSoftRate
	}
	// 11115 prompt too long。
	if isPromptTooLongStatus(status) && (strings.Contains(body, "11115") || strings.Contains(lower, "prompt is too long")) {
		return ErrPromptTooLong
	}
	if status == http.StatusNotFound {
		return ErrClient
	}
	if status >= 500 {
		return ErrServer
	}
	if status >= 400 {
		// 11148 工具历史错配：请求体决定的确定性错误，换号无法修复（ref b262660b ErrToolCallMismatch）。
		if status == http.StatusBadRequest && isToolCallMismatch(lower) {
			return ErrToolCallMismatch
		}
		// 内容策略拦截。
		if strings.Contains(lower, "blocked by security policy") || strings.Contains(lower, "unapproved channel") {
			return ErrContentBlocked
		}
		// badParams：11101/11128。
		if strings.Contains(body, "11101") || strings.Contains(body, "11128") || strings.Contains(body, "Unmarshal chat params failed") {
			return ErrBadParams
		}
		return ErrClient
	}
	return ErrNone
}

// isModelBlocked 判定是否为 11102「该后端无此模型」。
func isModelBlocked(status int, body string) bool {
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return false
	}
	if body == "" {
		return false
	}
	return strings.Contains(body, "11102") || strings.Contains(strings.ToLower(body), "service info not found")
}

func isPromptTooLongStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusNotFound ||
		status == http.StatusRequestEntityTooLarge
}

// isToolCallMismatch 判定 11148 工具历史错配（ref b262660b isToolCallMismatch）。
// 仅认 code 字段位置的 11148，requestId 等其他字段里的同数字不误判。
func isToolCallMismatch(lower string) bool {
	return strings.Contains(lower, "tool calls and tool results do not match") ||
		strings.Contains(lower, "tool_call_sequence_broken") ||
		strings.Contains(lower, `"code":11148`) ||
		strings.Contains(lower, `"code":"11148"`) ||
		strings.Contains(lower, `"code": "11148"`)
}

// hasBusinessCode 检查 JSON body 中是否包含指定的业务 code 字段值。
func hasBusinessCode(body, want string) bool {
	return strings.Contains(body, `"code":`+want) || strings.Contains(body, `"code": "`+want+`"`)
}

// classifyUpstreamError 将各类错误转为 *ExecError。
func classifyUpstreamError(err error) *ExecError {
	if err == nil {
		return nil
	}
	var execErr *ExecError
	if errors.As(err, &execErr) {
		return execErr
	}
	if errors.Is(err, context.Canceled) {
		return &ExecError{Kind: ErrClient, Status: 499, Msg: "request canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ExecError{Kind: ErrServer, Status: http.StatusGatewayTimeout, Msg: "upstream timed out"}
	}
	return &ExecError{Kind: ErrServer, Status: http.StatusBadGateway, Msg: sanitizeMsg(err.Error())}
}

// sanitizeMsg 脱敏错误消息：截断并去除可能的敏感信息。
func sanitizeMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	lower := strings.ToLower(s)
	// 去除 Authorization header 值泄露（大小写不敏感）。
	if i := strings.Index(lower, "authorization"); i >= 0 {
		s = s[:i] + "[REDACTED]"
	}
	return s
}
