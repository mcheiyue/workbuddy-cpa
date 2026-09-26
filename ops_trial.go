package main

import (
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// trialPath global 专属一次性「trial 加油包」端点（ref upstream/trial.go：
// POST {billingBase}/billing/ide/trial，Maquer/workbuddy-checkin 实测；CN 无此端点）。
const trialPath = "/billing/ide/trial"

// claimTrial 领取 global trial 加油包。幂等：14051 = 已领过（视为正常）。
// realm 分表由 buildWakes 的 scope 保证（trial 仅 global 调度），与 ref
// ClaimTrial 一样不在叶子重复检查。
func claimTrial(client *http.Client, realm string, cred wbauth.Credential) error {
	_, err := doBillingJSON(client, realm, cred, http.MethodPost, trialPath, nil)
	if err == nil || isTrialAlready(err) {
		return nil
	}
	return err
}

// isTrialAlready 判 14051「已领过」。上游两种指纹：HTTP 200 + envelope
// code!=0 时 upstreamError 输出 "code=14051"；HTTP 4xx 原始 body 含
// `"code":14051`（ref trial.go trialAlreadyMarkers）。
func isTrialAlready(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "code=14051") || strings.Contains(msg, `"code":14051`)
}

// runTrial ops 每日 trial 槽位：重读凭据（槽位距 spawn 可能超 AT 有效期）后领取。
// 每日幂等重试：未领则领（新 global 号自动补上），已领 14051 静默归成功。
func (t *opsTicker) runTrial(acct accountInfo) {
	var trialErr error
	defer func() {
		recover() // ticker never panics
		t.finishTask("trial", acct.authIndex, trialErr)
	}()
	cred, err := t.freshCred(acct)
	if err != nil {
		trialErr = err
		return
	}
	client, err := t.httpClient(acct.callbackID)
	if err != nil {
		trialErr = err
		return
	}
	trialErr = claimTrial(client, acct.realm, cred)
}
