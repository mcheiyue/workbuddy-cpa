package main

import (
	"encoding/json"
	"fmt"
	"net/http"

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
