package wbexecutor

import (
	"encoding/json"
	"net/http"
	"strings"
)

type modelErrorFields struct {
	Code    json.RawMessage   `json:"code"`
	Message string            `json:"message"`
	Msg     string            `json:"msg"`
	Display string            `json:"displayMsg"`
	Error   *modelErrorFields `json:"error"`
}

func (f modelErrorFields) modelError() (string, string) {
	code := strings.TrimSpace(strings.Trim(string(f.Code), `"`))
	message := f.Message
	if message == "" {
		message = f.Msg
	}
	if message == "" {
		message = f.Display
	}
	if code != "" || message != "" || f.Error == nil {
		return code, message
	}
	return f.Error.modelError()
}

func classifyModelError(status int, body string) ErrKind {
	if status != http.StatusBadRequest && status != http.StatusForbidden && status != http.StatusNotFound {
		return ErrNone
	}
	var fields modelErrorFields
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return ErrNone
	}
	code, message := fields.modelError()
	lowerMessage := strings.ToLower(message)
	if code == "11158" || strings.Contains(lowerMessage, "is not allowed for user type") {
		return ErrModelBlocked
	}
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return ErrNone
	}
	if code == "11102" || code == "14401" || strings.Contains(lowerMessage, "service info not found") {
		return ErrModelMissing
	}
	return ErrNone
}
