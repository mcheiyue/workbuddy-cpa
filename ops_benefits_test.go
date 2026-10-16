package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// benefitTestCred 构造指定 realm 的测试凭证。
// 走真实 wbauth.Parse，保证 queryBenefit/claimBenefit 看到的字段形态与生产链路一致。
func benefitTestCred(t *testing.T, realm, domain string) wbauth.Credential {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"auth":    map[string]any{"accessToken": "cp-token", "refreshToken": "cp-refresh", "realm": realm, "domain": domain},
		"account": map[string]any{"uid": "uid-control-1", "nickname": "ctl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := wbauth.Parse(raw)
	if err != nil {
		t.Fatalf("parse cred: %v", err)
	}
	return cred
}

// TestQueryBenefit_RealmPathAndParse 权益查询必须打到 /billing/meter/*（无 /v2 回落），
// 空对象 body，并解析 claimed/active/credit_num/end_time 四字段。
// 跨 realm 同路径是实测事实（/v2/billing/meter/* 对 cn/global 均 404），
// 因此不能套 billingMeterPaths 的 realm 分支。
func TestQueryBenefit_RealmPathAndParse(t *testing.T) {
	var gotHost, gotPath, gotMethod, gotBody string
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotPath, gotMethod = r.URL.Host, r.URL.Path, r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":false,"active":true,"credit_num":500,"end_time":"2026-10-22 23:59:59"}}`))
	}}}
	cred := benefitTestCred(t, wbauth.RealmCN, "www.workbuddy.cn")
	status, err := queryBenefit(client, wbauth.RealmCN, cred, benefitCompensation)
	if err != nil {
		t.Fatalf("queryBenefit: %v", err)
	}
	if gotHost != "www.workbuddy.cn" || gotPath != "/billing/meter/compensation-status" || gotMethod != http.MethodPost {
		t.Fatalf("request host=%s path=%s method=%s", gotHost, gotPath, gotMethod)
	}
	if gotBody != "{}" {
		t.Fatalf("body=%q, want {}（上游两端点均要求空对象 body）", gotBody)
	}
	if status.Claimed || !status.Active || status.CreditNum != 500 || status.EndTime != "2026-10-22 23:59:59" {
		t.Fatalf("status=%+v", status)
	}
}

// TestBenefitStatus_Claimable claimable 语义：只有 active 且未领才可领。
// 已领再领会被上游拒、inactive 领是空请求——两者都不该发 claim。
func TestBenefitStatus_Claimable(t *testing.T) {
	if !(benefitStatus{Active: true}).claimable() {
		t.Fatal("active 且未领必须判定为可领")
	}
	if (benefitStatus{Claimed: true, Active: true}).claimable() {
		t.Fatal("已领取不可再领")
	}
	if (benefitStatus{Active: false}).claimable() {
		t.Fatal("活动未开启不可领（global 窗口 4 月已关）")
	}
}

// TestQueryBenefit_GlobalSamePath global 用 workbuddy.ai 但路径与 cn 相同。
func TestQueryBenefit_GlobalSamePath(t *testing.T) {
	var gotHost, gotPath string
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotPath = r.URL.Host, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"msg":"","data":{"claimed":true,"active":true,"credit_num":1500,"end_time":"2026-10-31 23:59:59"}}`))
	}}}
	cred := benefitTestCred(t, wbauth.RealmGlobal, "www.workbuddy.ai")
	if _, err := queryBenefit(client, wbauth.RealmGlobal, cred, benefitGift); err != nil {
		t.Fatalf("queryBenefit: %v", err)
	}
	if gotHost != "www.workbuddy.ai" || gotPath != "/billing/meter/check-gift-claimed" {
		t.Fatalf("request host=%s path=%s（global 同路径，无 /v2 回落）", gotHost, gotPath)
	}
}

// TestClaimBenefit_DuplicateIsIdempotent 上游 409 duplicate（重复领取）视为已领，不冒泡为失败。
func TestClaimBenefit_DuplicateIsIdempotent(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"code":409,"msg":"duplicate claim"}`))
	}}}
	cred := benefitTestCred(t, wbauth.RealmCN, "www.workbuddy.cn")
	if err := claimBenefit(client, wbauth.RealmCN, cred, benefitCompensation); err != nil {
		t.Fatalf("重复领取应幂等返回 nil，实得 %v", err)
	}
}

// TestClaimBenefit_ErrorPropagates 非幂等错误必须冒泡——静默当成功会漏报未到账的积分。
func TestClaimBenefit_ErrorPropagates(t *testing.T) {
	client := &http.Client{Transport: &mockTransport{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"code":500,"msg":"boom"}`))
	}}}
	cred := benefitTestCred(t, wbauth.RealmCN, "www.workbuddy.cn")
	if err := claimBenefit(client, wbauth.RealmCN, cred, benefitGift); err == nil {
		t.Fatal("500 必须冒泡为错误")
	}
}
