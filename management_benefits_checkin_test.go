package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestBenefitsHandler_IncludesCheckinActivity 权益查询同时带回上游权威签到状态：
// /billing/meter/checkin-activity-status 两 realm 同路径可读（实测 2026-10-16），
// 提供 today_checked_in/streak_days/total_credits——本地 ledger 非上游权威，
// 「今日已签到」必须以上游口径为准。该端点查询失败不得影响权益行输出。
func TestBenefitsHandler_IncludesCheckinActivity(t *testing.T) {
	var violations []string
	creds := map[string][]byte{
		"cn1": controlTestCredential(),
	}
	svc := &managementService{
		hostCall: benefitHostCall(&violations, benefitTestFiles()[:1], creds),
		newHTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "checkin-activity-status") {
					w.Write([]byte(`{"code":0,"msg":"","data":{"active":true,"today_checked_in":true,"streak_days":17,"daily_credit":100,"total_credits":1700,"end_time":"2026-10-31 23:59:59","activity_name":"Buddy应用"}}`))
					return
				}
				w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":false,"active":false,"credit_num":0}}`))
			}}}, nil
		},
	}
	resp, err := svc.benefitsHandler([]byte(`{"auth_index":""}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := benefitParseResp(t, resp)["benefits"].([]any)
	if len(rows) != 1 {
		t.Fatalf("benefits=%s (want 1 row)", resp.Body)
	}
	row := rows[0].(map[string]any)
	ck, ok := row["checkin"].(map[string]any)
	if !ok {
		t.Fatalf("row 缺 checkin 字段: %v", row)
	}
	if ck["today_checked_in"] != true || ck["streak_days"].(float64) != 17 || ck["total_credits"].(float64) != 1700 {
		t.Fatalf("checkin=%v (want today_checked_in=true streak_days=17 total_credits=1700)", ck)
	}
	if strings.Contains(string(resp.Body), "cp-token") {
		t.Fatalf("响应泄漏 token: %s", resp.Body)
	}
}

// TestWebUIHasCheckinStatusColumn 面板「权益与签到」表含上游签到状态列，
// 卡片标题需覆盖签到语义（不再只是权益）。
func TestWebUIHasCheckinStatusColumn(t *testing.T) {
	html := string(workbuddyWebUI)
	for _, want := range []string{"权益与签到", "<th>签到</th>", "checkinCell"} {
		if !strings.Contains(html, want) {
			t.Fatalf("WebUI 缺 %q", want)
		}
	}
}
