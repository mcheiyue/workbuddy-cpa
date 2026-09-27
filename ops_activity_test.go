package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- 活跃上报契约 ---

func TestRunActivity_PostsChatRequestSend(t *testing.T) {
	prevGap := activityReportGap
	activityReportGap = 0 // 测试不等 1.5s 条间隔
	defer func() { activityReportGap = prevGap }()

	var reportReqs []*http.Request
	var reportBodies [][]byte
	var streakReqs []*http.Request
	var hookErrs []error
	var hookIDs []string
	ticker := &opsTicker{
		callHostFn: mockCallHost(t),
		hostHTTPFn: func(string) (*http.Client, error) {
			return &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
				var body []byte
				if r.Body != nil { // GET 无 body：nil 会 panic，被 runActivity 的 recover 吞掉
					body, _ = io.ReadAll(r.Body)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v2/report":
					reportReqs = append(reportReqs, r)
					reportBodies = append(reportBodies, body)
					w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
				case growthStreakPath:
					streakReqs = append(streakReqs, r)
					w.Write([]byte(`{"code":0,"msg":"ok","data":{"streak":{"days":6},"redemption_status":{}}}`))
				default:
					t.Errorf("unexpected upstream path %q", r.URL.Path)
					w.Write([]byte(`{"code":0,"msg":"","data":{}}`))
				}
			}}}, nil
		},
		tickHook: func(id string, err error) {
			hookIDs = append(hookIDs, id)
			hookErrs = append(hookErrs, err)
		},
	}
	acct := accountInfo{authIndex: "cn1", callbackID: "cn1", realm: wbauth.RealmCN}
	ticker.runActivity(acct)

	if len(hookIDs) != 1 || hookIDs[0] != "cn1" || hookErrs[0] != nil {
		t.Fatalf("hook: ids=%v errs=%v", hookIDs, hookErrs)
	}
	// C2b：一次 10 点档连发 activityReportCount 条独立 report。
	if len(reportReqs) != activityReportCount {
		t.Fatalf("report requests=%d, want %d", len(reportReqs), activityReportCount)
	}
	gotReq := reportReqs[0]
	body := reportBodies[0]
	if gotReq.URL.Host != "www.workbuddy.cn" {
		t.Fatalf("host=%q, want www.workbuddy.cn", gotReq.URL.Host)
	}
	if gotReq.Header.Get("X-User-Id") != "cn_uid_123" {
		t.Fatalf("x-user-id=%q", gotReq.Header.Get("X-User-Id"))
	}
	var events []map[string]any
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("body not JSON array: %v (%s)", err, body)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1 (每条请求一条事件)", len(events))
	}
	ev := events[0]
	if ev["eventCode"] != "chat_request_send" {
		t.Fatalf("eventCode=%v", ev["eventCode"])
	}
	if ev["mode"] != "craft" {
		t.Fatalf("mode=%v", ev["mode"])
	}
	if ev["userId"] != "cn_uid_123" {
		t.Fatalf("userId=%v", ev["userId"])
	}
	if ev["agentName"] != "default" {
		t.Fatalf("agentName=%v", ev["agentName"])
	}
	conv, _ := ev["conversationId"].(string)
	if !strings.HasPrefix(conv, "wbcpa-") {
		t.Fatalf("conversationId=%q", conv)
	}
	if ev["rootRequestId"] != ev["conversationId"] {
		t.Fatalf("rootRequestId=%v conversationId=%v", ev["rootRequestId"], ev["conversationId"])
	}
	// 同会话多轮：5 条共用 conversationId，requestId 各自独立。
	seenConv := map[string]bool{}
	seenReq := map[string]bool{}
	for i, b := range reportBodies {
		var es []map[string]any
		if err := json.Unmarshal(b, &es); err != nil || len(es) != 1 {
			t.Fatalf("report %d bad body: %v %s", i, err, b)
		}
		c, _ := es[0]["conversationId"].(string)
		rid, _ := es[0]["requestId"].(string)
		seenConv[c] = true
		if seenReq[rid] {
			t.Fatalf("duplicate requestId %q", rid)
		}
		seenReq[rid] = true
	}
	if len(seenConv) != 1 {
		t.Fatalf("conversationIds=%d, want 1 (同会话)", len(seenConv))
	}
	// C2a：上报成功后回读 streak 自检。
	if len(streakReqs) != 1 {
		t.Fatalf("streak requests=%d, want 1", len(streakReqs))
	}
	if streakReqs[0].URL.Host != "www.workbuddy.cn" {
		t.Fatalf("streak host=%q", streakReqs[0].URL.Host)
	}
}
