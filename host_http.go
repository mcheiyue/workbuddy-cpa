package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// controlHTTPTimeout 控制面直连上游的超时。
const controlHTTPTimeout = 30 * time.Second

// hostHTTPResponse 是宿主 HTTP 回调的响应。
type hostHTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func (r *hostHTTPResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		StatusCodeCamel int         `json:"StatusCode"`
		StatusCodeSnake int         `json:"status_code"`
		Headers         http.Header `json:"Headers"`
		HeadersLower    http.Header `json:"headers"`
		Body            []byte      `json:"Body"`
		BodyLower       []byte      `json:"body"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.StatusCode = wire.StatusCodeCamel
	if r.StatusCode == 0 {
		r.StatusCode = wire.StatusCodeSnake
	}
	r.Headers = wire.Headers
	if r.Headers == nil {
		r.Headers = wire.HeadersLower
	}
	r.Body = wire.Body
	if r.Body == nil {
		r.Body = wire.BodyLower
	}
	return nil
}

// hostRoundTripper 通过 CPA 宿主回调路由 HTTP 请求。
type hostRoundTripper struct {
	callbackID string
	call       func(string, any) (json.RawMessage, error)
}

func (t hostRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}
	request := map[string]any{
		"host_callback_id": t.callbackID,
		"request": map[string]any{
			"Method":  req.Method,
			"URL":     req.URL.String(),
			"Headers": req.Header.Clone(),
			"Body":    body,
		},
	}
	raw, err := t.call(pluginabi.MethodHostHTTPDo, request)
	if err != nil {
		return nil, fmt.Errorf("host HTTP call: %w", err)
	}
	var result hostHTTPResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode host HTTP response: %w", err)
	}
	if result.StatusCode == 0 {
		return nil, fmt.Errorf("host HTTP response missing status code")
	}
	return &http.Response{
		StatusCode: result.StatusCode,
		Header:     result.Headers,
		Body:       io.NopCloser(bytes.NewReader(result.Body)),
		Request:    req,
	}, nil
}

// newHostHTTPClient 创建通过宿主回调路由的 HTTP 客户端。
func newHostHTTPClient(callbackID string) (*http.Client, error) {
	return newHostHTTPClientWithCall(callbackID, callHostJSON)
}

// newDirectControlClient 返回控制面直连上游的 HTTP 客户端。
// 管理 API 与后台 ticker 都不在宿主分发上下文中，没有合法 host_callback_id，
// 走宿主 HTTP 桥会被拒绝（host callback ID is not open），故直连。
// 代价：绕过宿主代理配置；上游经 VPS 直连可达（2026-09-26 实测）。
func newDirectControlClient() (*http.Client, error) {
	return &http.Client{Timeout: controlHTTPTimeout}, nil
}

func newHostHTTPClientWithCall(callbackID string, call func(string, any) (json.RawMessage, error)) (*http.Client, error) {
	if callbackID == "" {
		return nil, fmt.Errorf("host callback ID is required")
	}
	if call == nil {
		return nil, fmt.Errorf("host callback is required")
	}
	return &http.Client{Transport: hostRoundTripper{callbackID: callbackID, call: call}}, nil
}

// isHostHTTPNotFoundError 报告错误是否为宿主 HTTP 404（宿主未实现 MethodHostHTTPDo）。
func isHostHTTPNotFoundError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "unknown method") ||
		strings.Contains(err.Error(), "not_implemented"))
}
