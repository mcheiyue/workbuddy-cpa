package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// billingUA 逐字对照 reference headers.go（client_name != SaaS 的默认 billing UA）。
const billingUA = "WorkBuddy/5.5.4"

// doBillingJSON 发 billing 域请求并解信封。
// base 与请求头对照 reference client.go.New/BillingHeaders：
// CN 现与 chat 同域 www.workbuddy.cn（2026-09-26 切换，旧 codebuddy.cn 为回退点）。
func doBillingJSON(client *http.Client, realm string, cred wbauth.Credential, method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, wbauth.BillingBase(realm)+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CodeBuddy-Request", "1")
	if realm == wbauth.RealmGlobal {
		req.Header.Set("Accept-Language", "en-US")
	} else {
		req.Header.Set("Accept-Language", "zh-CN")
	}
	req.Header.Set("User-Agent", billingUA)
	if cred.UID != "" {
		req.Header.Set("X-User-Id", cred.UID)
	}
	if cred.EnterpriseID != "" {
		req.Header.Set("X-Enterprise-Id", cred.EnterpriseID)
		req.Header.Set("X-Tenant-Id", cred.EnterpriseID)
	}
	if cred.Domain != "" {
		req.Header.Set("X-Domain", cred.Domain)
	}
	if cred.DeviceToken != "" {
		req.Header.Set("X-Device-Token", cred.DeviceToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &upstreamError{status: resp.StatusCode, msg: truncateStr(string(raw), 200)}
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}
	if env.Code != 0 {
		return nil, &upstreamError{status: resp.StatusCode, code: env.Code, msg: env.Msg}
	}
	return env.Data, nil
}
