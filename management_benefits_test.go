package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// benefitHostCall 多账号 hostCall mock：auth.list 返回 files，auth.get 按 auth_index
// 返回对应凭证 JSON；宿主 HTTP 桥调用记违规（控制面必须直连）。
func benefitHostCall(violations *[]string, files []any, creds map[string][]byte) func(string, any) (json.RawMessage, error) {
	return func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPDo:
			*violations = append(*violations, method)
			return nil, errHostCallbackNotOpen
		case pluginabi.MethodHostAuthList:
			return json.Marshal(map[string]any{"files": files})
		case pluginabi.MethodHostAuthGet:
			req, _ := payload.(pluginapi.HostAuthGetRequest)
			cred, ok := creds[req.AuthIndex]
			if !ok {
				return nil, errUnexpectedMethod
			}
			return json.Marshal(pluginapi.HostAuthGetResponse{AuthIndex: req.AuthIndex, JSON: cred})
		default:
			return nil, errUnexpectedMethod
		}
	}
}

// benefitTestFiles 构造 CN + global 各一号的 auth.list。
func benefitTestFiles() []any {
	return []any{
		map[string]any{"auth_index": "cn1", "provider": "workbuddy", "name": "cn1.json"},
		map[string]any{"auth_index": "gl1", "provider": "workbuddy", "name": "gl1.json"},
	}
}

// benefitParseResp 解析 handler 响应体。
func benefitParseResp(t *testing.T, resp pluginapi.ManagementResponse) map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode %s: %v", resp.Body, err)
	}
	return payload
}

// TestBenefitsHandler_DirectNotHostBridged 权益查询必须直连，且响应不含 token（#1819）。
func TestBenefitsHandler_DirectNotHostBridged(t *testing.T) {
	var violations []string
	creds := map[string][]byte{
		"cn1": controlTestCredential(),
		"gl1": []byte(`{"auth":{"accessToken":"gl-token","refreshToken":"gl-refresh","domain":"www.workbuddy.ai","realm":"global"},"account":{"uid":"uid-gl-1","nickname":"gl"}}`),
	}
	svc := &managementService{
		hostCall: benefitHostCall(&violations, benefitTestFiles()[:1], creds),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "check-gift-claimed") {
					w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":true,"active":true,"credit_num":1500,"end_time":"2026-10-31 23:59:59"}}`))
					return
				}
				w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":false,"active":true,"credit_num":500,"end_time":"2026-10-22 23:59:59"}}`))
			}}}, nil
		},
	}
	resp, err := svc.benefitsHandler([]byte(`{"auth_index":""}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := benefitParseResp(t, resp)
	rows := payload["benefits"].([]any)
	if len(rows) != 1 {
		t.Fatalf("benefits=%s (want 1 row)", resp.Body)
	}
	row := rows[0].(map[string]any)
	if row["realm"] != "cn" || !strings.HasSuffix(row["uid_tail"].(string), "ol-1") {
		t.Fatalf("row=%v (want realm=cn, uid_tail 以 ol-1 结尾)", row)
	}
	gift := row["gift"].(map[string]any)
	comp := row["compensation"].(map[string]any)
	if gift["claimed"] != true || gift["credit_num"].(float64) != 1500 {
		t.Fatalf("gift=%v (want claimed=true credit=1500)", gift)
	}
	if comp["claimed"] != false || comp["active"] != true || comp["credit_num"].(float64) != 500 {
		t.Fatalf("compensation=%v (want claimed=false active=true credit=500)", comp)
	}
	if strings.Contains(string(resp.Body), "cp-token") || strings.Contains(string(resp.Body), "cp-refresh") {
		t.Fatalf("响应泄漏 token: %s", resp.Body)
	}
	if len(violations) != 0 {
		t.Fatalf("control plane used host HTTP bridge: %v", violations)
	}
}

// TestClaimBenefitsHandler_ClaimsOnlyClaimable 只对 active 且未领的权益发 claim：
// CN 补偿可领 → 领取 + 复核置 OK；CN 赠送已领 → ALREADY；global 窗口已关 → SKIP 且零上游写请求。
// 复核（claim 后重查）是必需证据：只有上游 claimed 真的翻转为 true 才报 OK。
func TestClaimBenefitsHandler_ClaimsOnlyClaimable(t *testing.T) {
	var violations []string
	var claimPaths []string
	compClaimed := false
	creds := map[string][]byte{
		"cn1": controlTestCredential(),
		"gl1": []byte(`{"auth":{"accessToken":"gl-token","refreshToken":"gl-refresh","domain":"www.workbuddy.ai","realm":"global"},"account":{"uid":"uid-gl-1","nickname":"gl"}}`),
	}
	svc := &managementService{
		hostCall: benefitHostCall(&violations, benefitTestFiles(), creds),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/claim-") {
					claimPaths = append(claimPaths, r.URL.Path)
					compClaimed = true
					w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
					return
				}
				if r.URL.Host == "www.workbuddy.ai" {
					// global 两侧窗口 4 月已关：active=false。
					w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":false,"active":false,"credit_num":1000,"end_time":"2026-04-15 23:59:59"}}`))
					return
				}
				if strings.Contains(r.URL.Path, "check-gift-claimed") {
					w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":true,"active":true,"credit_num":1500,"end_time":"2026-10-31 23:59:59"}}`))
					return
				}
				if compClaimed {
					w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":true,"active":true,"credit_num":500,"end_time":"2026-10-22 23:59:59"}}`))
					return
				}
				w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":false,"active":true,"credit_num":500,"end_time":"2026-10-22 23:59:59"}}`))
			}}}, nil
		},
	}
	resp, err := svc.claimBenefitsHandler([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := benefitParseResp(t, resp)
	rows := payload["results"].([]any)
	if len(rows) != 4 {
		t.Fatalf("results=%s (want 4 行 = 2 账号 × 2 权益)", resp.Body)
	}
	got := map[string]string{}
	for _, r := range rows {
		row := r.(map[string]any)
		key := row["auth_index"].(string) + "/" + row["kind"].(string)
		got[key] = row["status"].(string)
		if strings.Contains(string(resp.Body), "gl-token") {
			t.Fatalf("响应泄漏 token: %s", resp.Body)
		}
	}
	want := map[string]string{
		"cn1/" + benefitGift:         "ALREADY",
		"cn1/" + benefitCompensation: "OK",
		"gl1/" + benefitGift:         "SKIP",
		"gl1/" + benefitCompensation: "SKIP",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("result[%s]=%q, want %q (all=%v)", k, got[k], v, got)
		}
	}
	if len(claimPaths) != 1 || !strings.HasSuffix(claimPaths[0], "/billing/meter/claim-compensation") {
		t.Fatalf("claim 请求=%v (want 仅 1 次 claim-compensation；已领/inactive 不得发写请求)", claimPaths)
	}
	if len(violations) != 0 {
		t.Fatalf("control plane used host HTTP bridge: %v", violations)
	}
}

// TestBenefitRoutesRegistered 路由表含 POST /workbuddy/benefits 与 /benefits/claim。
func TestBenefitRoutesRegistered(t *testing.T) {
	raw, err := handleMethod("management.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		t.Fatalf("envelope=%s", raw)
	}
	var result struct {
		Routes []struct{ Method, Path string } `json:"routes"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatalf("decode routes: %v", err)
	}
	found := map[string]bool{}
	for _, r := range result.Routes {
		if r.Method == http.MethodPost {
			found[r.Path] = true
		}
	}
	for _, want := range []string{"/workbuddy/benefits", "/workbuddy/benefits/claim"} {
		if !found[want] {
			t.Fatalf("路由缺失 %s（现有 POST 路由：%v）", want, found)
		}
	}
}

// TestWebUIHasBenefitsCard 面板含「待领权益」卡片、两个动作按钮，且下拉随账号列表填充。
func TestWebUIHasBenefitsCard(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{
		"待领权益",
		`onclick="loadBenefits()"`,
		`onclick="claimBenefits()"`,
		"benefitAuthIdx",
		"/benefits",
		"/benefits/claim",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("WebUI 缺 %q", want)
		}
	}
	if !strings.Contains(html, "['quotaAuthIdx','checkinAuthIdx','benefitAuthIdx']") {
		t.Fatal("benefitAuthIdx 未接入 populateSelects——下拉会永远只有「全部账号」")
	}
}
