package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// billing 路径常量（逐字对照 reference upstream/client.go）。
const (
	billingMeterPath   = "/billing/meter/get-user-resource"
	dailyCheckinPath   = "/billing/meter/daily-checkin"
	billingMeterPathV2 = "/v2/billing/meter/get-user-resource"
	dailyCheckinPathV2 = "/v2/billing/meter/daily-checkin"
)

// billingMeterPaths 按 realm 返回候选路径序列：
// global → [/billing/meter/*, /v2/billing/meter/*]（404 回落）；
// cn → [/v2/billing/meter/*]（现状逐字）。
func billingMeterPaths(realm string) []string {
	if realm == wbauth.RealmGlobal {
		return []string{billingMeterPath, billingMeterPathV2}
	}
	return []string{billingMeterPathV2}
}

// checkinMeterPaths 同上，针对 daily-checkin。
func checkinMeterPaths(realm string) []string {
	if realm == wbauth.RealmGlobal {
		return []string{dailyCheckinPath, dailyCheckinPathV2}
	}
	return []string{dailyCheckinPathV2}
}

// apiEnvelope 上游统一信封。
type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// userResourceResp get-user-resource 响应结构（外层 env.Data 解包后）。
type userResourceResp struct {
	Response struct {
		Data struct {
			TotalDosage int64            `json:"TotalDosage"`
			Accounts    []map[string]any `json:"Accounts"`
		} `json:"Data"`
	} `json:"Response"`
}

// packageEndLayout 上游时间格式（墙钟）。
const packageEndLayout = "2006-01-02 15:04:05"

// resourceSummary 查询账号积分套餐聚合口径。
func resourceSummary(client *http.Client, realm, accessToken string) (remain, used, size int64, packs int, err error) {
	resp, err := getUserResourceBody(client, realm, accessToken)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	for _, acct := range resp.Response.Data.Accounts {
		r, u, s := packageRemainUsed(acct)
		remain += r
		used += u
		size += s
	}
	packs = len(resp.Response.Data.Accounts)
	if size > 0 {
		if derived := size - remain; derived > used {
			used = derived
		}
	}
	if dosage := resp.Response.Data.TotalDosage; dosage > size {
		size = dosage
		if derived := size - remain; derived > used {
			used = derived
		}
	}
	return remain, used, size, packs, nil
}

// getUserResourceBody 发 get-user-resource 请求并解析响应。
func getUserResourceBody(client *http.Client, realm, accessToken string) (*userResourceResp, error) {
	now := time.Now()
	body := map[string]any{
		"PageNumber":               1,
		"PageSize":                 100,
		"ProductCode":              "p_tcaca",
		"Status":                   []int{0, 3},
		"PackageEndTimeRangeBegin": now.Format(packageEndLayout),
		"PackageEndTimeRangeEnd":   now.Add(365 * 101 * 24 * time.Hour).Format(packageEndLayout),
	}
	data, err := billingMeterJSON(client, realm, accessToken, http.MethodPost, billingMeterPaths(realm), body)
	if err != nil {
		return nil, err
	}
	var resp userResourceResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("resource parse: %w", err)
	}
	return &resp, nil
}

// dailyCheckin 执行每日签到。
func dailyCheckin(client *http.Client, realm, accessToken string) error {
	_, err := billingMeterJSON(client, realm, accessToken, http.MethodPost, checkinMeterPaths(realm), map[string]any{})
	return err
}

// billingMeterJSON 按 realm 候选路径发请求，404 时回落下一路径。
func billingMeterJSON(client *http.Client, realm, accessToken string, method string, paths []string, body any) (json.RawMessage, error) {
	var lastErr error
	for i, p := range paths {
		data, err := doBillingJSON(client, realm, accessToken, method, p, body)
		if err != nil {
			lastErr = err
			var ue *upstreamError
			if i < len(paths)-1 && asUpstreamError(err, &ue) && ue.status == http.StatusNotFound {
				continue
			}
			return nil, err
		}
		return data, nil
	}
	return nil, lastErr
}

// doBillingJSON 发请求并解信封。
func doBillingJSON(client *http.Client, realm, accessToken, method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	base := wbauth.RealmBase(realm)
	req, err := http.NewRequest(method, base+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Origin", wbauth.RealmOrigin(realm))
	req.Header.Set("Referer", wbauth.RealmOrigin(realm))
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
		return nil, fmt.Errorf("parse failed: %w (body: %s)", err, truncateStr(string(raw), 120))
	}
	if env.Code != 0 {
		return nil, &upstreamError{status: resp.StatusCode, code: env.Code, msg: env.Msg}
	}
	return env.Data, nil
}

// upstreamError 带状态码的上游错误。
type upstreamError struct {
	status int
	code   int
	msg    string
}

func (e *upstreamError) Error() string {
	if e.code != 0 {
		return fmt.Sprintf("upstream %d code=%d: %s", e.status, e.code, e.msg)
	}
	return fmt.Sprintf("upstream %d: %s", e.status, e.msg)
}

func asUpstreamError(err error, target **upstreamError) bool {
	if e, ok := err.(*upstreamError); ok {
		*target = e
		return true
	}
	return false
}

// truncateStr 截断字符串。
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// packageRemainUsed 聚合单套餐 remain/used/size（逐字对照 reference）。
func packageRemainUsed(acct map[string]any) (remain, used, size int64) {
	cycleSize := int64(jsonNumber(acct["CycleCapacitySize"]))
	cycleRemain := int64(jsonNumber(acct["CycleCapacityRemain"]))
	cycleUsed := int64(jsonNumber(acct["CycleCapacityUsed"]))
	capSize := int64(jsonNumber(acct["CapacitySize"]))
	capRemain := int64(jsonNumber(acct["CapacityRemain"]))
	capUsed := int64(jsonNumber(acct["CapacityUsed"]))
	if cycleSize > 0 {
		remain = cycleRemain
		size = cycleSize
		if remain < 0 {
			remain = 0
		}
		if remain > size {
			remain = size
		}
		used = size - remain
		if cycleUsed > used {
			used = cycleUsed
			if size >= used {
				remain = size - used
			}
		}
		return remain, used, size
	}
	remain = capRemain
	used = capUsed
	size = capSize
	if used == 0 && size > remain {
		used = size - remain
	}
	return remain, used, size
}

// jsonNumber 辅助从 map 取数字值。
func jsonNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}
