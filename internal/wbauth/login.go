package wbauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiEnvelope 是上游 {code,msg,data} 信封。
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// doJSON 发起 HTTP 请求并解析 {code,msg,data} 信封，code!=0 → error。
func doJSON(ctx context.Context, client *http.Client, method, url string, headers http.Header, body []byte) (json.RawMessage, error) {
	env, err := doJSONRaw(ctx, client, method, url, headers, body)
	if err != nil {
		return nil, err
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("code=%d msg=%s", env.Code, env.Msg)
	}
	return env.Data, nil
}

// doJSONRaw 发起 HTTP 请求并返回完整信封（不检查 code），调用方自行判断。
func doJSONRaw(ctx context.Context, client *http.Client, method, url string, headers http.Header, body []byte) (apiEnvelope, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return apiEnvelope{}, err
	}
	for k, vs := range headers {
		req.Header[k] = vs
	}
	resp, err := client.Do(req)
	if err != nil {
		return apiEnvelope{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiEnvelope{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return apiEnvelope{}, fmt.Errorf("http_error: upstream %d", resp.StatusCode)
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return apiEnvelope{}, fmt.Errorf("parse failed: %w", err)
	}
	return env, nil
}

// StartResult 是 Device OAuth start 的返回值。
type StartResult struct {
	State   string
	AuthURL string
}

// Start 发起 Device OAuth：POST /v2/plugin/auth/state?platform=CLI。
func Start(ctx context.Context, client *http.Client, baseURL, origin string) (StartResult, error) {
	h := commonHeaders(origin)
	data, err := doJSON(ctx, client, http.MethodPost,
		baseURL+"/v2/plugin/auth/state?platform=CLI", h, []byte("{}"))
	if err != nil {
		return StartResult{}, fmt.Errorf("auth state: %w", err)
	}
	var st struct {
		State   string `json:"state"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(data, &st); err != nil || st.State == "" || st.AuthURL == "" {
		return StartResult{}, fmt.Errorf("auth state: missing state or authUrl")
	}
	return StartResult{State: st.State, AuthURL: st.AuthURL}, nil
}

// PollResult 是 Device OAuth poll 的返回值。
type PollResult struct {
	Credential Credential
	Pending    bool
}

// Poll 轮询登录状态：GET /v2/plugin/auth/token + /v2/plugin/login/account。
func Poll(ctx context.Context, client *http.Client, baseURL, origin, realm, state string) (PollResult, error) {
	h := commonHeaders(origin)

	// token 端点：code!=0 表示 pending（登录未完成），code=0 表示成功。
	env, err := doJSONRaw(ctx, client, http.MethodGet,
		baseURL+"/v2/plugin/auth/token?state="+state, h, nil)
	if err != nil {
		return PollResult{}, fmt.Errorf("token endpoint: %w", err)
	}
	if env.Code != 0 {
		return PollResult{Pending: true}, nil
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(env.Data, &tok); err != nil || tok.AccessToken == "" {
		return PollResult{Pending: true}, nil
	}

	// account 端点拿 uid/nickname（带 Bearer）
	acctH := h.Clone()
	acctH.Set("Authorization", "Bearer "+tok.AccessToken)
	var acct struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	}
	if acctRaw, errAcct := doJSON(ctx, client, http.MethodGet,
		baseURL+"/v2/plugin/login/account?state="+state, acctH, nil); errAcct == nil {
		_ = json.Unmarshal(acctRaw, &acct)
	}

	cred := Credential{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
		Domain:       tok.Domain,
		Realm:        ResolveRealm(realm, tok.Domain),
		UID:          acct.UID,
		EnterpriseID: acct.EnterpriseID,
		Nickname:     acct.Nickname,
	}
	return PollResult{Credential: cred}, nil
}

// commonHeaders 构造通用请求头。
func commonHeaders(origin string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/plain, */*")
	h.Set("X-Requested-With", "XMLHttpRequest")
	h.Set("Origin", origin)
	h.Set("Referer", origin+"/")
	h.Set("User-Agent", "CLI/2.63.2 CodeBuddy/2.63.2")
	return h
}
