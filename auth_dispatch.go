package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// handleAuthMethod 分发 auth 系列 RPC 到 wbauth 包。
func handleAuthMethod(method string, raw []byte) ([]byte, error) {
	ctx := context.Background()
	var result any
	var err error
	switch method {
	case pluginabi.MethodAuthIdentifier:
		result = struct {
			Identifier string `json:"identifier"`
		}{Identifier: wbauth.Provider}
	case pluginabi.MethodAuthParse:
		result, err = handleAuthParse(raw)
	case pluginabi.MethodAuthLoginStart:
		result, err = handleAuthLoginStart(ctx, raw)
	case pluginabi.MethodAuthLoginPoll:
		result, err = handleAuthLoginPoll(ctx, raw)
	case pluginabi.MethodAuthRefresh:
		result, err = handleAuthRefresh(ctx, raw)
	}
	if err != nil {
		return errorEnvelope("auth_error", err.Error()), nil
	}
	return okEnvelope(result)
}

// rpcAuthLoginStartRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthLoginStartRequest struct {
	pluginapi.AuthLoginStartRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// rpcAuthLoginPollRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthLoginPollRequest struct {
	pluginapi.AuthLoginPollRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// rpcAuthRefreshRequest 包含 CPA SDK 字段 + 宿主回调 ID。
type rpcAuthRefreshRequest struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleAuthParse(raw []byte) (pluginapi.AuthParseResponse, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	// 非 workbuddy provider → 不处理。
	if req.Provider != "" && !strings.EqualFold(req.Provider, wbauth.Provider) {
		return pluginapi.AuthParseResponse{}, nil
	}
	cred, err := wbauth.Parse(req.RawJSON)
	if err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	return pluginapi.AuthParseResponse{
		Handled: true,
		Auth:    cred.AuthData(req.FileName),
	}, nil
}

func handleAuthLoginStart(ctx context.Context, raw []byte) (pluginapi.AuthLoginStartResponse, error) {
	var req rpcAuthLoginStartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	realm := wbauth.RealmCN
	if v, ok := req.Metadata["realm"].(string); ok {
		realm = wbauth.ResolveRealm(v, "")
	}
	start, err := wbauth.Start(ctx, client, wbauth.RealmBase(realm), wbauth.RealmOrigin(realm))
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	return pluginapi.AuthLoginStartResponse{
		Provider: wbauth.Provider,
		URL:      start.AuthURL,
		State:    start.State,
	}, nil
}

func handleAuthLoginPoll(ctx context.Context, raw []byte) (pluginapi.AuthLoginPollResponse, error) {
	var req rpcAuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	realm := wbauth.RealmCN
	if v, ok := req.Metadata["realm"].(string); ok {
		realm = wbauth.ResolveRealm(v, "")
	}
	poll, err := wbauth.Poll(ctx, client, wbauth.RealmBase(realm), wbauth.RealmOrigin(realm), realm, req.State)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	if poll.Pending {
		return pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for login",
		}, nil
	}
	// Start ops ticker now that an account exists.
	EnsureOpsStarted()
	return pluginapi.AuthLoginPollResponse{
		Status: pluginapi.AuthLoginStatusSuccess,
		Auth:   poll.Credential.AuthData(""),
	}, nil
}

func handleAuthRefresh(ctx context.Context, raw []byte) (pluginapi.AuthRefreshResponse, error) {
	var req rpcAuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	cred, err := wbauth.Parse(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	// 与 ticker 保活共锁：防同一 refresh_token 并发轮换。
	unlock := lockAuthRefresh(req.AuthID)
	defer unlock()
	client, err := newHostHTTPClient(req.HostCallbackID)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	refreshed, err := wbauth.Refresh(ctx, client, wbauth.RealmBase(wbauth.ResolveRealm(cred.Realm, cred.Domain)), wbauth.RealmOrigin(wbauth.ResolveRealm(cred.Realm, cred.Domain)), cred)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	// Start ops ticker now that an account exists (refresh confirms validity).
	EnsureOpsStarted()
	return pluginapi.AuthRefreshResponse{
		Auth:             refreshed.Credential.AuthData(""),
		NextRefreshAfter: refreshed.NextRefreshAfter,
	}, nil
}
