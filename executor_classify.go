package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

// executorFailure 带结构化错误码的执行器错误。
type executorFailure struct {
	code    string
	message string
	status  int
	cause   error
}

func (e *executorFailure) Error() string { return e.message }
func (e *executorFailure) Unwrap() error { return e.cause }

// classifyExecutorError 将各类错误映射为结构化 executorFailure。
func classifyExecutorError(err error) *executorFailure {
	var failure *executorFailure
	if errors.As(err, &failure) {
		return failure
	}
	var execErr *wbexecutor.ExecError
	if errors.As(err, &execErr) {
		return &executorFailure{
			code:    execErr.Kind.String(),
			message: execErr.Msg,
			status:  execErr.Status,
			cause:   err,
		}
	}
	if errors.Is(err, context.Canceled) {
		return &executorFailure{code: "client_canceled", message: "request canceled", status: 499, cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &executorFailure{
			code: "upstream_timeout", message: "upstream timed out",
			status: http.StatusGatewayTimeout, cause: err,
		}
	}
	return &executorFailure{code: "upstream_error", message: scrubErrorMessage(err.Error()), status: http.StatusBadGateway, cause: err}
}

// scrubErrorMessage 截断并移除可能泄露的敏感信息（Bearer token、Authorization header 等）。
func scrubErrorMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	s = tokenScrub(s)
	lower := strings.ToLower(s)
	if i := strings.Index(lower, "authorization"); i >= 0 {
		s = s[:i] + "[REDACTED]"
	}
	return s
}
