package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// Growth reward paths (reference: internal/upstream/growth_reward.go:4-7, travel.go:18-23)
const (
	growthStreakPath = "/activity/growth/streak"
	growthRedeemPath = "/activity/growth/redeem"
)

// growthTierSpec is the per-tier config from streak.redemption_status.tiers.
type growthTierSpec struct {
	Tier    string `json:"tier"`
	Days    int    `json:"days"`
	Credit  int    `json:"credit"`
	Energy  int    `json:"energy"`
	Cards   int    `json:"cards"`
	Chances int    `json:"chances"`
}

// growthRedemptionStatus holds per-tier claim status (reference: growth_reward.go:47-55)
type growthRedemptionStatus struct {
	Tier7dStatus  string `json:"tier_7d_status"`
	Tier14dStatus string `json:"tier_14d_status"`
	Tier28dStatus string `json:"tier_28d_status"`
}

// growthStreakResp is the GET /activity/growth/streak response data shape.
type growthStreakResp struct {
	Streak struct {
		Days int `json:"days"`
	} `json:"streak"`
	Redemption growthRedemptionStatus `json:"redemption_status"`
}

// growthRedeemResult is the POST /activity/growth/redeem response data shape
// (reference: growth_reward.go:96-103).
type growthRedeemResult struct {
	CreditGranted  int `json:"credit_granted"`
	EnergyGranted  int `json:"energy_granted"`
	CardsGranted   int `json:"cards_granted"`
	ChancesGranted int `json:"chances_granted"`
}

// doGrowthJSON sends a request to the growth API (chatBase + BillingHeaders).
// Growth endpoints live on chatBase (copilot.tencent.com), not billingBase.
// Same header set as doBillingJSON (reference: travel.go:44 growthJSON → BillingHeaders).
func doGrowthJSON(client *http.Client, realm string, cred wbauth.Credential, method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, wbauth.RealmBase(realm)+path, bodyReader)
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

// tierStatus returns the claimed/available/locked status for a given tier.
func tierStatus(redemption growthRedemptionStatus, tier string) string {
	switch tier {
	case "7d":
		return redemption.Tier7dStatus
	case "14d":
		return redemption.Tier14dStatus
	case "28d":
		return redemption.Tier28dStatus
	}
	return ""
}

// isClaimAlreadyDone detects HTTP 409 "duplicate" (reference: growth_reward.go:197-200).
func isClaimAlreadyDone(err error) bool {
	if err == nil {
		return false
	}
	var ue *upstreamError
	if !errors.As(err, &ue) || ue.status != http.StatusConflict {
		return false
	}
	lower := strings.ToLower(ue.msg)
	return strings.Contains(lower, "duplicate") || strings.Contains(lower, "\u91cd\u590d\u9886\u53d6")
}

// isClaimNotEnoughDays detects HTTP 403 "记录天数不够" (reference: growth_reward.go:203-204).
func isClaimNotEnoughDays(err error) bool {
	if err == nil {
		return false
	}
	var ue *upstreamError
	if !errors.As(err, &ue) || ue.status != http.StatusForbidden {
		return false
	}
	lower := strings.ToLower(ue.msg)
	return strings.Contains(lower, "\u8bb0\u5f55\u5929\u6570\u4e0d\u591f")
}

// claimTierReward claims a single tier's streak reward (reference: growth_reward.go:109-118).
func claimTierReward(client *http.Client, realm string, cred wbauth.Credential, tier string) (*growthRedeemResult, error) {
	token := fmt.Sprintf("redeem-%s-%d", tier, len(tier))
	data, err := doGrowthJSON(client, realm, cred, http.MethodPost, growthRedeemPath,
		map[string]any{"tier": tier, "client_token": token})
	if err != nil {
		return nil, err
	}
	var res growthRedeemResult
	if len(data) > 0 {
		_ = json.Unmarshal(data, &res)
	}
	return &res, nil
}

// growthStreak fetches current streak + redemption status.
func growthStreak(client *http.Client, realm string, cred wbauth.Credential) (*growthStreakResp, error) {
	data, err := doGrowthJSON(client, realm, cred, http.MethodGet, growthStreakPath, nil)
	if err != nil {
		return nil, err
	}
	var st growthStreakResp
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// runClaimCredits attempts to claim streak-based growth rewards (7d/14d/28d tier).
// Wired into opsDailyTasks as the 4th slot (hour 11, independent of checkin/activity/keepalive).
func (t *opsTicker) runClaimCredits(acct accountInfo) {
	var claimErr error
	defer func() {
		recover()
		t.finishTask("claim", acct.authIndex, claimErr)
	}()
	client, err := t.httpClient(acct.callbackID)
	if err != nil {
		claimErr = err
		return
	}
	_, claimErr = claimCredits(client, acct.realm, acct.cred)
}

// claimCredits tries to claim all available tiers and returns results.
func claimCredits(client *http.Client, realm string, cred wbauth.Credential) (string, error) {
	st, err := growthStreak(client, realm, cred)
	if err != nil {
		return "", err
	}
	tiers := []string{"7d", "14d", "28d"}
	for _, tier := range tiers {
		status := tierStatus(st.Redemption, tier)
		if status != "available" {
			continue
		}
		_, cerr := claimTierReward(client, realm, cred, tier)
		if cerr != nil {
			if isClaimAlreadyDone(cerr) || isClaimNotEnoughDays(cerr) {
				continue
			}
			return "", cerr
		}
		return fmt.Sprintf("claimed:%s", tier), nil
	}
	return "none-available", nil
}
