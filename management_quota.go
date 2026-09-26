package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// quotaRefreshHandler 处理 POST /workbuddy/quota。
func (s *managementService) quotaRefreshHandler(body []byte) (pluginapi.ManagementResponse, error) {
	var request struct {
		AuthIndex string `json:"auth_index"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonManagementError(http.StatusBadRequest, "invalid JSON body"), nil
	}
	request.AuthIndex = strings.TrimSpace(request.AuthIndex)
	if request.AuthIndex == "" {
		return jsonManagementError(http.StatusBadRequest, "auth_index is required"), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rawAuth, err := s.hostCall(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: request.AuthIndex})
	if err != nil {
		return jsonManagementError(http.StatusNotFound, "account not found"), nil
	}
	var auth pluginapi.HostAuthGetResponse
	if err := json.Unmarshal(rawAuth, &auth); err != nil {
		return jsonManagementError(http.StatusNotFound, "account not found"), nil
	}
	cred, perr := wbauth.Parse(auth.JSON)
	if perr != nil {
		return jsonManagementError(http.StatusBadRequest, "invalid credential"), nil
	}
	client, cerr := s.controlClient()
	if cerr != nil {
		return jsonManagementError(http.StatusBadGateway, "host unavailable"), nil
	}
	realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
	remain, used, size, packs, fetchErr := resourceSummary(client, realm, cred)
	resp := managementQuotaResp{AuthIndex: request.AuthIndex}
	if fetchErr != nil {
		resp.Error = quotaErrorMask(fetchErr)
	} else {
		resp.Remain, resp.Used, resp.Size, resp.Packages = remain, used, size, packs
		RecordManualLedger(request.AuthIndex, remain)
	}
	return jsonManagementResponse(http.StatusOK, resp)
}

// checkinHandler 处理 POST /workbuddy/checkin。
func (s *managementService) checkinHandler(body []byte) (pluginapi.ManagementResponse, error) {
	var request struct {
		AuthIndex string `json:"auth_index"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonManagementError(http.StatusBadRequest, "invalid JSON body"), nil
	}
	request.AuthIndex = strings.TrimSpace(request.AuthIndex)
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.hostCall(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return jsonManagementError(http.StatusBadGateway, "failed to list accounts"), nil
	}
	var result struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return jsonManagementError(http.StatusBadGateway, "decode auth list failed"), nil
	}
	results := make([]managementCheckinResp, 0)
	for _, file := range result.Files {
		if !strings.EqualFold(file.Provider, wbauth.Provider) && !strings.EqualFold(file.Type, wbauth.Provider) {
			continue
		}
		if request.AuthIndex != "" && file.AuthIndex != request.AuthIndex {
			continue
		}
		rawAuth, getErr := s.hostCall(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: file.AuthIndex})
		if getErr != nil {
			results = append(results, managementCheckinResp{UID: file.AuthIndex, Status: "FAIL", Message: "failed to get credential"})
			continue
		}
		var auth pluginapi.HostAuthGetResponse
		if json.Unmarshal(rawAuth, &auth) != nil {
			results = append(results, managementCheckinResp{UID: file.AuthIndex, Status: "FAIL", Message: "invalid credential"})
			continue
		}
		cred, perr := wbauth.Parse(auth.JSON)
		if perr != nil || cred.AccessToken == "" {
			results = append(results, managementCheckinResp{UID: file.AuthIndex, Status: "FAIL", Message: "invalid credential"})
			continue
		}
		client, cerr := s.controlClient()
		if cerr != nil {
			results = append(results, managementCheckinResp{UID: file.AuthIndex, Status: "FAIL", Message: "host unavailable"})
			continue
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		checkErr := doCheckin(client, realm, cred)
		status := checkinStatus(checkErr)
		resp := managementCheckinResp{
			UID:     uidTail(cred.UID),
			Status:  status,
			Message: fmtCheckinResult(status),
		}
		if checkErr == nil || isAlreadyCheckin(checkErr) {
			if remain, _, _, _, qerr := resourceSummary(client, realm, cred); qerr == nil {
				resp.Remain = &remain
				RecordManualLedger(file.AuthIndex, remain)
			}
		}
		results = append(results, resp)
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"results": results})
}
