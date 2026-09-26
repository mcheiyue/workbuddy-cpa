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
	// newHTTPClient 控制面出站客户端工厂；nil 时用 newDirectControlClient。
	newHTTPClient func() (*http.Client, error)
}

var defaultManagementService = &managementService{
	hostCall:      callHostJSON,
	newHTTPClient: newDirectControlClient,
}

// controlClient 返回控制面直连客户端（管理面无宿主分发回调，不能走 host HTTP 桥）。
func (s *managementService) controlClient() (*http.Client, error) {
	if s.newHTTPClient != nil {
		return s.newHTTPClient()
	}
	return newDirectControlClient()
}

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
			acct.DeadCount = deadSessions.count(file.AuthIndex)
			if deadSessions.isDisabled(file.AuthIndex) {
				acct.Disabled = true
				acct.DisabledReason = deadSessions.reason(file.AuthIndex)
			}
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
			acct.LastCheckin = lastLedgerTs(file.AuthIndex)
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
		client, cerr := s.controlClient()
		if cerr != nil {
			continue
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		modelsList, merr := wbmodels.FetchAndRegister(context.Background(), client, realm, cred.AccessToken, file.AuthIndex, defaultRegistry)
		if merr != nil {
			continue
		}
		for _, m := range modelsList {
			// FetchAndRegister 返回的 m.ID 已是公开 ID（FilterAndBuild 生成），不再套 PublicModelID。
			id := strings.TrimSpace(m.ID)
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
