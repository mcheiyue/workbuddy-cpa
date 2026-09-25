package wbexecutor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrKind 错误分类，对应上游业务码和 HTTP 状态码。
type ErrKind int

const (
	ErrNone           ErrKind = iota // 成功
	ErrModelRateLimit                // 6004 模型级限流
	ErrSessionDead                   // 12153 会话失效
	ErrBadParams                     // 11128/11101 请求参数错误
	ErrModelBlocked                  // 11102 该后端无此模型
	ErrPromptTooLong                 // 11115 prompt 超长
	ErrContentBlocked                // 审核拦截
	ErrAccountFault                  // 账号级故障（request illegal 等）
	ErrHardCredit                    // 余额不足
	ErrSoftRate                      // 软限流
	ErrServer                        // 5xx
	ErrClient                        // 其他 4xx
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
	default:
		return "none"
	}
}

// ExecError 带分类的执行错误。
type ExecError struct {
	Kind   ErrKind
	Status int
	Msg    string // 脱敏摘要，不含 token/Authorization
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("upstream %s (http %d): %s", e.Kind, e.Status, e.Msg)
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
	if strings.Contains(lower, "request illegal") || strings.Contains(lower, "trial not activated") {
		return ErrAccountFault
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
