package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/mcheiyue/workbuddy-cpa/internal/wbmodels"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// managementService 管理服务状态（可注入依赖供测试）。
type managementService struct {
	mu       sync.Mutex
	hostCall func(string, any) (json.RawMessage, error)
}

var defaultManagementService = &managementService{hostCall: callHostJSON}

// accountsHandler 处理 GET /workbuddy/accounts。
func (s *managementService) accountsHandler() (pluginapi.ManagementResponse, error) {
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
	accounts := make([]managementAccount, 0, len(result.Files))
	for _, file := range result.Files {
		if !strings.EqualFold(file.Provider, wbauth.Provider) && !strings.EqualFold(file.Type, wbauth.Provider) {
			continue
		}
		acct := managementAccount{
			AuthIndex: file.AuthIndex,
			Nickname:  file.Label,
		}
		if file.AuthIndex != "" {
			if rawAuth, getErr := s.hostCall(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: file.AuthIndex}); getErr == nil {
				var auth pluginapi.HostAuthGetResponse
				if json.Unmarshal(rawAuth, &auth) == nil {
					cred, perr := wbauth.Parse(auth.JSON)
					if perr == nil {
						acct.UIDTail = uidTail(cred.UID)
						acct.Realm = wbauth.ResolveRealm(cred.Realm, cred.Domain)
						if cred.ExpiresAt > 0 {
							acct.ExpiresAt = time.Unix(cred.ExpiresAt, 0).Format("2006-01-02 15:04:05")
						}
					}
				}
			}
		}
		accounts = append(accounts, acct)
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"accounts": accounts})
}

// modelsHandler 处理 GET /workbuddy/models（模型目录快照）。
func (s *managementService) modelsHandler() (pluginapi.ManagementResponse, error) {
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
	seen := make(map[string]bool)
	type modelEntry struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Source      string `json:"source"`
	}
	models := make([]modelEntry, 0)
	for _, file := range result.Files {
		if !strings.EqualFold(file.Provider, wbauth.Provider) && !strings.EqualFold(file.Type, wbauth.Provider) {
			continue
		}
		if file.AuthIndex == "" {
			continue
		}
		rawAuth, getErr := s.hostCall(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: file.AuthIndex})
		if getErr != nil {
			continue
		}
		var auth pluginapi.HostAuthGetResponse
		if json.Unmarshal(rawAuth, &auth) != nil {
			continue
		}
		cred, perr := wbauth.Parse(auth.JSON)
		if perr != nil || cred.AccessToken == "" {
			continue
		}
		client, cerr := newHostHTTPClientWithCall(file.AuthIndex, s.hostCall)
		if cerr != nil {
			continue
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		modelsList, merr := wbmodels.FetchAndRegister(context.Background(), client, realm, cred.AccessToken, file.AuthIndex, defaultRegistry)
		if merr != nil {
			continue
		}
		for _, m := range modelsList {
			id := wbmodels.PublicModelID(m.ID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, modelEntry{
				ID:          id,
				DisplayName: wbmodels.DisplayNameForModel(m.ID, m.Name),
				Source:      file.AuthIndex,
			})
		}
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"models": models})
}

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
	client, cerr := newHostHTTPClientWithCall(request.AuthIndex, s.hostCall)
	if cerr != nil {
		return jsonManagementError(http.StatusBadGateway, "host unavailable"), nil
	}
	realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
	remain, used, size, packs, fetchErr := resourceSummary(client, realm, cred.AccessToken)
	resp := managementQuotaResp{AuthIndex: request.AuthIndex}
	if fetchErr != nil {
		resp.Error = quotaErrorMask(fetchErr)
	} else {
		resp.Remain, resp.Used, resp.Size, resp.Packages = remain, used, size, packs
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
		client, cerr := newHostHTTPClientWithCall(file.AuthIndex, s.hostCall)
		if cerr != nil {
			results = append(results, managementCheckinResp{UID: file.AuthIndex, Status: "FAIL", Message: "host unavailable"})
			continue
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		checkErr := doCheckin(client, realm, cred.AccessToken)
		status := checkinStatus(checkErr)
		resp := managementCheckinResp{
			UID:     uidTail(cred.UID),
			Status:  status,
			Message: fmtCheckinResult(status),
		}
		if checkErr == nil || isAlreadyCheckin(checkErr) {
			if remain, _, _, _, qerr := resourceSummary(client, realm, cred.AccessToken); qerr == nil {
				resp.Remain = &remain
			}
		}
		results = append(results, resp)
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"results": results})
}
