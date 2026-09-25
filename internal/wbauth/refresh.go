package wbauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RefreshResult 是 refresh 的返回值。
type RefreshResult struct {
	Credential       Credential
	NextRefreshAfter time.Time
}

// Refresh 刷新 access token。成功后返回新凭据；失败时返回错误，旧凭据不受影响。
// baseURL 是上游 base URL，origin 是 Origin/Referer 头值。
func Refresh(ctx context.Context, client *http.Client, baseURL, origin string, cred Credential) (RefreshResult, error) {
	if cred.RefreshToken == "" {
		return RefreshResult{}, fmt.Errorf("no refreshToken")
	}
	h := refreshHeaders(origin, cred)

	data, err := doJSON(ctx, client, http.MethodPost,
		baseURL+"/v2/plugin/auth/token/refresh", h, nil)
	if err != nil {
		return RefreshResult{}, sanitizeError(err, cred)
	}
	var tok struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return RefreshResult{}, fmt.Errorf("refresh_failed: no accessToken in response — re-login required")
	}

	// 原子替换：整体构建新凭据，不逐字段覆盖旧值。
	newCred := cred
	newCred.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		newCred.RefreshToken = tok.RefreshToken
	}
	if tok.Domain != "" {
		newCred.Domain = tok.Domain
	}
	if tok.ExpiresIn > 0 && time.Duration(tok.ExpiresIn)*time.Second < 10*365*24*time.Hour {
		newCred.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
	}

	return RefreshResult{
		Credential:       newCred,
		NextRefreshAfter: time.Unix(newCred.ExpiresAt, 0),
	}, nil
}

// refreshHeaders 构造 refresh 端点请求头。
func refreshHeaders(origin string, cred Credential) http.Header {
	h := commonHeaders(origin)
	h.Set("Authorization", "Bearer "+cred.AccessToken)
	h.Set("X-Refresh-Token", cred.RefreshToken)
	if cred.UID != "" {
		h.Set("X-User-Id", cred.UID)
	}
	if cred.EnterpriseID != "" {
		h.Set("X-Enterprise-Id", cred.EnterpriseID)
	}
	h.Set("X-Auth-Refresh-Source", "plugin")
	h.Set("X-Product", "WorkBuddy")
	if cred.DeviceToken != "" {
		h.Set("X-Device-Token", cred.DeviceToken)
	}
	return h
}

// sanitizeError 从错误文本中移除 token 类敏感值，防止泄露到日志/envelope。
func sanitizeError(err error, cred Credential) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, secret := range []string{cred.AccessToken, cred.RefreshToken, cred.DeviceToken} {
		if secret != "" && len(secret) > 4 {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return fmt.Errorf("%s", msg)
}

// RefreshMutex 是 per-credential refresh 锁。
// ponytail: 简单 per-key mutex map；若多账号并发刷新成为瓶颈，可升级为 singleflight.Group。
type RefreshMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewRefreshMutex 创建 RefreshMutex。
func NewRefreshMutex() *RefreshMutex {
	return &RefreshMutex{locks: make(map[string]*sync.Mutex)}
}

// Lock 获取指定 credential ID 的锁。
func (m *RefreshMutex) Lock(id string) {
	m.mu.Lock()
	if m.locks[id] == nil {
		m.locks[id] = &sync.Mutex{}
	}
	lk := m.locks[id]
	m.mu.Unlock()
	lk.Lock()
}

// Unlock 释放指定 credential ID 的锁。
func (m *RefreshMutex) Unlock(id string) {
	m.mu.Lock()
	lk := m.locks[id]
	m.mu.Unlock()
	if lk != nil {
		lk.Unlock()
	}
}
