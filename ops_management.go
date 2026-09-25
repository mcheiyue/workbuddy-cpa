package main

import (
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// opsManagementService handles ops-related management endpoints.
type opsManagementService struct{}

var defaultOpsManagementService = &opsManagementService{}

// lastCheckinHandler 处理 GET /workbuddy/last-checkin。
func (s *opsManagementService) lastCheckinHandler() (pluginapi.ManagementResponse, error) {
	entries := globalLedger.snapshot()
	type checkinRecord struct {
		AuthID string `json:"auth_id"`
		Ts     string `json:"ts"`
		Source string `json:"source"`
		Status string `json:"status"`
	}
	seen := make(map[string]checkinRecord)
	for _, e := range entries {
		status := "OK"
		if e.Source == "ticker" {
			status = "TICKER"
		}
		seen[e.AuthID] = checkinRecord{
			AuthID: e.AuthID,
			Ts:     e.Ts.Format("2006-01-02 15:04:05"),
			Source: e.Source,
			Status: status,
		}
	}
	result := make([]checkinRecord, 0, len(seen))
	for _, r := range seen {
		result = append(result, r)
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{"last_checkins": result})
}

// creditsLedgerHandler 处理 GET /workbuddy/credits-ledger。
func (s *opsManagementService) creditsLedgerHandler() (pluginapi.ManagementResponse, error) {
	entries := globalLedger.snapshot()
	result := make([]ledgerEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, e)
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{
		"entries": result,
		"note":    "本地观测非上游权威",
	})
}
