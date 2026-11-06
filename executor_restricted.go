package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var authFileWriteMu sync.Mutex

const restrictedAccountReason = "WorkBuddy 11140：上游明确限制账号异常行为；申诉确认后手动启用"

func recordExecutorFailure(req rpcExecutorRequest, hit *wbexecutor.ExecError) error {
	if hit == nil {
		return nil
	}
	if !hit.Restricted {
		return hit
	}
	if err := disableRestrictedAccount(req.AuthID); err != nil {
		log.Printf("[workbuddy] restricted account persistence failed auth=%q error=%v", req.AuthID, err)
		return fmt.Errorf("%w; restricted account could not be saved as disabled", hit)
	}
	return hit
}

func disableRestrictedAccount(authID string) error {
	authFileWriteMu.Lock()
	defer authFileWriteMu.Unlock()
	raw, err := callHostJSON(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return fmt.Errorf("auth list unavailable")
	}
	var list struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err = json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("invalid auth list")
	}
	var index string
	for _, entry := range list.Files {
		if entry.ID == authID && strings.EqualFold(entry.Provider, "workbuddy") && !entry.RuntimeOnly {
			index = entry.AuthIndex
			break
		}
	}
	if index == "" {
		return fmt.Errorf("selected physical workbuddy account not found")
	}
	raw, err = callHostJSON(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: index})
	if err != nil {
		return fmt.Errorf("auth read unavailable")
	}
	var saved pluginapi.HostAuthGetResponse
	if err = json.Unmarshal(raw, &saved); err != nil || saved.Name == "" {
		return fmt.Errorf("invalid auth response")
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(saved.JSON, &fields); err != nil || fields == nil {
		return fmt.Errorf("invalid auth JSON")
	}
	var provider string
	if err = json.Unmarshal(fields["type"], &provider); err != nil || provider != "workbuddy" {
		return fmt.Errorf("credential provider mismatch")
	}
	fields["disabled"] = json.RawMessage("true")
	var note string
	if rawNote, ok := fields["note"]; ok {
		if err = json.Unmarshal(rawNote, &note); err != nil {
			return fmt.Errorf("invalid credential note")
		}
	}
	if !strings.Contains(note, restrictedAccountReason) {
		note = strings.TrimSpace(note + "\n" + restrictedAccountReason)
	}
	fields["note"], err = json.Marshal(note)
	if err != nil {
		return fmt.Errorf("encode restriction note: %w", err)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode restricted account: %w", err)
	}
	if _, err = callHostJSON(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{Name: saved.Name, JSON: data}); err != nil {
		return fmt.Errorf("auth save failed")
	}
	log.Printf("[workbuddy] account disabled auth=%q reason=11140_account_restricted", authID)
	return nil
}
