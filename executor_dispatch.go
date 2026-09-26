package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/mcheiyue/workbuddy-cpa/internal/wbmodels"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const executorID = "workbuddy"

// rpcExecutorRequest 包含 CPA SDK 字段 + 宿主回调 ID + 流式 stream ID。
type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleExecutorMethod(method string, raw []byte) ([]byte, error) {
	// executor.identifier 直接返回插件标识，不解析 request。
	if method == pluginabi.MethodExecutorIdentifier {
		return okEnvelope(struct {
			Identifier string `json:"identifier"`
		}{Identifier: executorID})
	}
	// executor.http_request 本计划未覆盖（需 CPA 宿主原生 HTTP 透传）。
	if method == pluginabi.MethodExecutorHTTPRequest {
		return notImplemented(method)
	}
	var request rpcExecutorRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return errorEnvelopeStatus("invalid_request", "invalid executor request", http.StatusBadRequest), nil
	}
	var result any
	var err error
	switch method {
	case pluginabi.MethodExecutorExecute:
		result, err = executorExecute(context.Background(), request)
	case pluginabi.MethodExecutorExecuteStream:
		response, streamErr := executorExecuteStream(context.Background(), request)
		err = streamErr
		result = struct {
			Headers http.Header `json:"headers,omitempty"`
		}{Headers: response.Headers}
	case pluginabi.MethodExecutorCountTokens:
		result, err = executorCountTokens(request)
	}
	if err != nil {
		failure := classifyExecutorError(err)
		// Scheduler hook: record model-scoped cooldown on ErrModelRateLimit (6004).
		if failure.code == "model_rate_limit" {
			aid := strings.TrimSpace(request.AuthID)
			if aid != "" {
				globalSchedulerState.setCooldown(aid, request.Model)
			}
		}
		return errorEnvelopeStatus(failure.code, failure.message, failure.status), nil
	}
	return okEnvelope(result)
}

func executorExecute(ctx context.Context, req rpcExecutorRequest) (pluginapi.ExecutorResponse, error) {
	cfg, err := buildExecutorConfig(req)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	execReq, err := buildExecuteRequest(req)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	raw, execErr := wbexecutor.Execute(ctx, cfg, execReq)
	if execErr != nil {
		return pluginapi.ExecutorResponse{}, execErr
	}
	// Execute 强制 stream:true，上游返回 SSE 流；聚合为单个 chat.completion JSON。
	aggregated, aggErr := wbexecutor.Aggregate(bytes.NewReader(raw))
	if aggErr != nil {
		// 聚合失败时回退到原始响应（上游可能直接返回 JSON）。
		aggregated = raw
	}
	// 注入公开模型 ID（聚合后覆盖内部模型名）。
	aggregated = injectModelField(aggregated, execReq.PublicModelID)
	return pluginapi.ExecutorResponse{
		Payload: aggregated,
		Headers: http.Header{"Content-Type": {"application/json"}},
	}, nil
}

// injectModelField 在 JSON 中设置 model 字段。
func injectModelField(raw []byte, model string) []byte {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	obj["model"] = model
	result, _ := json.Marshal(obj)
	return result
}

func executorExecuteStream(ctx context.Context, req rpcExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
	if strings.TrimSpace(req.StreamID) == "" {
		return pluginapi.ExecutorStreamResponse{}, &executorFailure{
			code: "invalid_request", message: "stream_id is required", status: http.StatusBadRequest,
		}
	}
	cfg, err := buildExecutorConfig(req)
	if err != nil {
		return pluginapi.ExecutorStreamResponse{}, err
	}
	cfg.StreamEmit = hostStreamEmit
	cfg.StreamClose = hostStreamClose
	execReq, err := buildExecuteRequest(req)
	if err != nil {
		return pluginapi.ExecutorStreamResponse{}, err
	}
	execErr := wbexecutor.ExecuteStream(ctx, cfg, execReq)
	if execErr != nil {
		return pluginapi.ExecutorStreamResponse{}, execErr
	}
	return pluginapi.ExecutorStreamResponse{
		Headers: http.Header{"Content-Type": {"text/event-stream"}},
	}, nil
}

func executorCountTokens(req rpcExecutorRequest) (pluginapi.ExecutorResponse, error) {
	tokens := wbexecutor.CountTokens(req.Payload)
	payload, err := json.Marshal(struct {
		TotalTokens int  `json:"total_tokens"`
		InputTokens int  `json:"input_tokens"`
		Estimated   bool `json:"estimated"`
	}{TotalTokens: tokens, InputTokens: tokens, Estimated: true})
	if err != nil {
		return pluginapi.ExecutorResponse{}, fmt.Errorf("encode token estimate: %w", err)
	}
	return pluginapi.ExecutorResponse{Payload: payload}, nil
}

// buildExecutorConfig 构建 wbexecutor 的依赖注入。
func buildExecutorConfig(req rpcExecutorRequest) (wbexecutor.Config, error) {
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return wbexecutor.Config{}, &executorFailure{
			code: "host_unavailable", message: err.Error(), status: http.StatusBadGateway,
		}
	}
	return wbexecutor.Config{
		Doer:       client.Transport.RoundTrip,
		StreamDoer: makeHostStreamDoer(req.HostCallbackID),
		Resolver:   defaultRegistry,
	}, nil
}

// buildExecuteRequest 从 RPC 请求构造 wbexecutor.ExecuteRequest。
func buildExecuteRequest(req rpcExecutorRequest) (wbexecutor.ExecuteRequest, error) {
	cred, err := wbauth.Parse(req.StorageJSON)
	if err != nil {
		return wbexecutor.ExecuteRequest{}, &executorFailure{
			code: "invalid_auth", message: "invalid workbuddy credential", status: http.StatusUnauthorized,
		}
	}
	authID := strings.TrimSpace(req.AuthID)
	if authID == "" {
		authID = wbmodels.AuthIDFromCredential(cred)
	}
	realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
	return wbexecutor.ExecuteRequest{
		AuthID:         authID,
		PublicModelID:  req.Model,
		Payload:        req.Payload,
		StreamID:       req.StreamID,
		ConversationID: req.Headers.Get("X-Conversation-ID"),
		ChatBaseURL:    wbauth.RealmBase(realm),
		Cred: wbexecutor.Credential{
			AccessToken:  cred.AccessToken,
			UID:          cred.UID,
			DeviceToken:  cred.DeviceToken,
			EnterpriseID: cred.EnterpriseID,
			Domain:       cred.Domain,
			Realm:        cred.Realm,
		},
	}, nil
}

// hostStreamEmit 通过 CPA 宿主回调发送裸 JSON chunk（禁止 "data: " 前缀）。
func hostStreamEmit(streamID string, payload []byte) error {
	_, err := callHostJSON(pluginabi.MethodHostStreamEmit, map[string]any{
		"stream_id": streamID,
		"payload":   payload,
	})
	return err
}

// hostStreamClose 通过 CPA 宿主回调关闭流；message 非空时为错误关闭。
func hostStreamClose(streamID string, message string) {
	closePayload := map[string]any{"stream_id": streamID}
	if message != "" {
		closePayload["error"] = message
	}
	callHostJSON(pluginabi.MethodHostStreamClose, closePayload)
}
