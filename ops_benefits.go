package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// 权益端点（CN usercenter bundle index-D7kQlGMt.js 逐字对照）。
// 实测：/billing/meter/* 对 cn 与 global 均 200，/v2/billing/meter/* 均 404
// ——两个 realm 同路径，故不复用 billingMeterPaths 的 /v2 回落。
const (
	benefitGiftCheckPath  = "/billing/meter/check-gift-claimed"
	benefitGiftClaimPath  = "/billing/meter/claim-gift"
	benefitCompStatusPath = "/billing/meter/compensation-status"
	benefitCompClaimPath  = "/billing/meter/claim-compensation"
	// benefitCheckinStatusPath 是上游权威签到状态（只读）。
	// 实测 2026-10-16：两 realm 同路径可读，且 /v2 前缀在此端点也通（与上面两条不同）。
	benefitCheckinStatusPath = "/billing/meter/checkin-activity-status"
)

// 权益种类。
const (
	benefitGift         = "gift"
	benefitCompensation = "compensation"
)

// benefitKinds 查询/领取顺序（面板与 handler 共用，保证行序稳定）。
var benefitKinds = []string{benefitGift, benefitCompensation}

// benefitPaths 返回某类权益的「状态查询, 领取」路径。
func benefitPaths(kind string) (status, claim string) {
	if kind == benefitCompensation {
		return benefitCompStatusPath, benefitCompClaimPath
	}
	return benefitGiftCheckPath, benefitGiftClaimPath
}

// benefitName 返回权益中文名（面板与结果消息共用）。
func benefitName(kind string) string {
	if kind == benefitCompensation {
		return "补偿积分"
	}
	return "赠送积分"
}

// benefitStatus 上游权益状态（两端点响应同构：data.claimed/active/credit_num/end_time）。
// global 号窗口已关时 active=false（实测 2026-04-15/04-30 到期），不是错误。
type benefitStatus struct {
	Claimed   bool   `json:"claimed"`
	Active    bool   `json:"active"`
	CreditNum int64  `json:"credit_num"`
	EndTime   string `json:"end_time,omitempty"`
}

// claimable 报告是否可领：active 且未领。
// 已领再领会被上游拒（409 duplicate）、inactive 领是空写请求——两者都该在上游前拦掉。
func (b benefitStatus) claimable() bool { return b.Active && !b.Claimed }

// checkinActivity 上游权威签到状态（data 字段）。
// 与本地 ledger 的区别：ledger 只记「插件何时打过签到」，此结构是上游真实签到口径
// （today_checked_in 由服务端判定），面板「今日已签到」以本结构为准。
type checkinActivity struct {
	Active          bool   `json:"active"`
	TodayCheckedIn  bool   `json:"today_checked_in"`
	StreakDays      int    `json:"streak_days"`
	DailyCredit     int64  `json:"daily_credit"`
	TotalCredits    int64  `json:"total_credits"`
	WeekCheckinDays int    `json:"week_checkin_days"`
	EndTime         string `json:"end_time,omitempty"`
	ActivityName    string `json:"activity_name,omitempty"`
}

// queryCheckinActivity 查询单账号上游签到状态（只读端点，空对象 body）。
func queryCheckinActivity(client *http.Client, realm string, cred wbauth.Credential) (*checkinActivity, error) {
	data, err := doBillingJSON(client, realm, cred, http.MethodPost, benefitCheckinStatusPath, map[string]any{})
	if err != nil {
		return nil, err
	}
	var out checkinActivity
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("checkin activity parse: %w", err)
		}
	}
	return &out, nil
}

// queryBenefit 查询单账号单类权益状态（只读端点，空对象 body）。
func queryBenefit(client *http.Client, realm string, cred wbauth.Credential, kind string) (*benefitStatus, error) {
	statusPath, _ := benefitPaths(kind)
	data, err := doBillingJSON(client, realm, cred, http.MethodPost, statusPath, map[string]any{})
	if err != nil {
		return nil, err
	}
	var out benefitStatus
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("benefit status parse: %w", err)
		}
	}
	return &out, nil
}

// claimBenefit 领取单账号单类权益；上游 409 duplicate（已领）幂等视为成功。
func claimBenefit(client *http.Client, realm string, cred wbauth.Credential, kind string) error {
	_, claimPath := benefitPaths(kind)
	if _, err := doBillingJSON(client, realm, cred, http.MethodPost, claimPath, map[string]any{}); err != nil {
		if isClaimAlreadyDone(err) {
			return nil
		}
		return err
	}
	return nil
}

// claimAllBenefits 领取全部「可领」权益，返回结果串供任务日志。
// 只对 claimable() 的项发写请求：global 两侧窗口已关（active=false），
// 无条件领会退化成每天每号 2 次空写；已领项再领会被上游拒（409）。
func claimAllBenefits(client *http.Client, realm string, cred wbauth.Credential) (string, error) {
	claimed := make([]string, 0, len(benefitKinds))
	for _, kind := range benefitKinds {
		st, err := queryBenefit(client, realm, cred, kind)
		if err != nil {
			return "", err
		}
		if !st.claimable() {
			continue
		}
		if err := claimBenefit(client, realm, cred, kind); err != nil {
			return "", err
		}
		claimed = append(claimed, benefitName(kind))
	}
	if len(claimed) == 0 {
		return "none-claimable", nil
	}
	return "claimed:" + strings.Join(claimed, ","), nil
}

// runClaimBenefits 自动领取有窗口的权益（挂 opsDailyTasks hour 11）。
// 补偿窗口有硬到期（CN 500 于 10-22 23:59），手动领易漏。
func (t *opsTicker) runClaimBenefits(acct accountInfo) {
	var claimErr error
	defer func() {
		recover()
		t.finishTask("benefits", acct.authIndex, claimErr)
	}()
	client, err := t.httpClient(acct.callbackID)
	if err != nil {
		claimErr = err
		return
	}
	_, claimErr = claimAllBenefits(client, acct.realm, acct.cred)
}
