package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// workbuddyFiles 列出 workbuddy 凭证文件（宿主重复注册的去重由调用方按 auth_index 处理）。
func (s *managementService) workbuddyFiles() ([]pluginapi.HostAuthFileEntry, error) {
	raw, err := s.hostCall(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	files := make([]pluginapi.HostAuthFileEntry, 0, len(result.Files))
	for _, file := range result.Files {
		if strings.EqualFold(file.Provider, wbauth.Provider) || strings.EqualFold(file.Type, wbauth.Provider) {
			files = append(files, file)
		}
	}
	return files, nil
}

// credential 从宿主读取并解析单账号凭证；错误消息是脱敏短语（不含上游原文与 token）。
func (s *managementService) credential(authIndex string) (wbauth.Credential, error) {
	rawAuth, err := s.hostCall(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return wbauth.Credential{}, errors.New("failed to get credential")
	}
	var auth pluginapi.HostAuthGetResponse
	if json.Unmarshal(rawAuth, &auth) != nil {
		return wbauth.Credential{}, errors.New("invalid credential")
	}
	cred, perr := wbauth.Parse(auth.JSON)
	if perr != nil || cred.AccessToken == "" {
		return wbauth.Credential{}, errors.New("invalid credential")
	}
	return cred, nil
}

// benefitsHandler 处理 POST /workbuddy/benefits（auth_index 为空 = 全部账号）。
// 只兑查询端点，不发 claim。
func (s *managementService) benefitsHandler(body []byte) (pluginapi.ManagementResponse, error) {
	var request struct {
		AuthIndex string `json:"auth_index"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonManagementError(http.StatusBadRequest, "invalid JSON body"), nil
	}
	request.AuthIndex = strings.TrimSpace(request.AuthIndex)
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.workbuddyFiles()
	if err != nil {
		return jsonManagementError(http.StatusBadGateway, "failed to list accounts"), nil
	}
	client, cerr := s.controlClient()
	if cerr != nil {
		return jsonManagementError(http.StatusBadGateway, "host unavailable"), nil
	}
	benefits := make([]managementBenefitResp, 0, len(files))
	for _, file := range files {
		if request.AuthIndex != "" && file.AuthIndex != request.AuthIndex {
			continue
		}
		row := managementBenefitResp{AuthIndex: file.AuthIndex}
		cred, credErr := s.credential(file.AuthIndex)
		if credErr != nil {
			row.Error = credErr.Error()
			benefits = append(benefits, row)
			continue
		}
		row.Realm = wbauth.ResolveRealm(cred.Realm, cred.Domain)
		row.UIDTail = uidTail(cred.UID)
		for _, kind := range benefitKinds {
			st, qerr := queryBenefit(client, row.Realm, cred, kind)
			if qerr != nil {
				row.Error = quotaErrorMask(qerr)
				continue
			}
			if kind == benefitGift {
				row.Gift = st
			} else {
				row.Compensation = st
			}
		}
		benefits = append(benefits, row)
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"benefits": benefits})
}

// claimBenefitsHandler 处理 POST /workbuddy/benefits/claim（auth_index 为空 = 全部账号）。
// 只领 active 且未领的权益；领取后再查一次，只有上游 claimed 翻转才报 OK
// ——上游返回 200 不代表积分到账，必须复核（否则漏报未到账的积分）。
func (s *managementService) claimBenefitsHandler(body []byte) (pluginapi.ManagementResponse, error) {
	var request struct {
		AuthIndex string `json:"auth_index"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonManagementError(http.StatusBadRequest, "invalid JSON body"), nil
	}
	request.AuthIndex = strings.TrimSpace(request.AuthIndex)
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.workbuddyFiles()
	if err != nil {
		return jsonManagementError(http.StatusBadGateway, "failed to list accounts"), nil
	}
	client, cerr := s.controlClient()
	if cerr != nil {
		return jsonManagementError(http.StatusBadGateway, "host unavailable"), nil
	}
	results := make([]managementClaimResp, 0, len(files)*len(benefitKinds))
	for _, file := range files {
		if request.AuthIndex != "" && file.AuthIndex != request.AuthIndex {
			continue
		}
		cred, credErr := s.credential(file.AuthIndex)
		if credErr != nil {
			// 凭证不可用时每种权益各留一行，保持面板表格行数稳定。
			for _, kind := range benefitKinds {
				results = append(results, managementClaimResp{
					AuthIndex: file.AuthIndex, Kind: kind, Status: "FAIL", Message: credErr.Error(),
				})
			}
			continue
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		tail := uidTail(cred.UID)
		for _, kind := range benefitKinds {
			row := managementClaimResp{AuthIndex: file.AuthIndex, UIDTail: tail, Realm: realm, Kind: kind}
			st, qerr := queryBenefit(client, realm, cred, kind)
			switch {
			case qerr != nil:
				row.Status, row.Message = "FAIL", quotaErrorMask(qerr)
			case !st.claimable():
				row.Status = "SKIP"
				if st.Claimed {
					row.Status = "ALREADY"
				}
				row.Message, row.EndTime = fmtClaimResult(row.Status, kind), st.EndTime
			default:
				row.CreditNum, row.EndTime = st.CreditNum, st.EndTime
				claimErr := claimBenefit(client, realm, cred, kind)
				if claimErr != nil {
					row.Status, row.Message = "FAIL", fmtClaimResult("FAIL", kind)+"："+quotaErrorMask(claimErr)
					break
				}
				after, aerr := queryBenefit(client, realm, cred, kind)
				if aerr == nil && after.Claimed {
					row.Status, row.Message = "OK", fmtClaimResult("OK", kind)
				} else {
					row.Status, row.Message = "SUBMITTED", fmtClaimResult("SUBMITTED", kind)
				}
			}
			results = append(results, row)
		}
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"results": results})
}

// fmtClaimResult 格式化领取结果消息（面板「信息」列）。
func fmtClaimResult(status, kind string) string {
	name := benefitName(kind)
	switch status {
	case "OK":
		return name + "领取成功"
	case "ALREADY":
		return name + "已领取"
	case "SKIP":
		return name + "当前无进行中的活动（窗口未开启或已过期）"
	case "SUBMITTED":
		return name + "领取请求已发送，上游尚未确认到账"
	}
	return name + "领取失败"
}
