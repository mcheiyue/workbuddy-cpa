package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// activityReportPath 对照 reference report.go（billing 域每日活跃上报）。
const activityReportPath = "/v2/report"

// chatRequestEvent 逐字对照 reference internal/upstream/report.go。
// 服务端不校验 conversationID/requestID，非真实会话。
type chatRequestEvent struct {
	EventCode             string `json:"eventCode"`
	Timestamp             int64  `json:"timestamp"`
	ReportDelay           int    `json:"reportDelay"`
	Mode                  string `json:"mode"`
	ConversationID        string `json:"conversationId"`
	RequestID             string `json:"requestId"`
	InputLength           int    `json:"inputLength"`
	RequestModelID        string `json:"requestModelId"`
	RequestModelName      string `json:"requestModelName"`
	IsPlan                bool   `json:"isPlan"`
	IsAutoExecuteTerminal bool   `json:"isAutoExecuteTerminal"`
	IsAutoModify          bool   `json:"isAutoModify"`
	CodebaseEnable        bool   `json:"codebaseEnable"`
	MaxToken              int    `json:"maxToken"`
	MaxSteps              int    `json:"maxSteps"`
	Temperature           int    `json:"temperature"`
	MaxRetries            int    `json:"maxRetries"`
	MentionContexts       []any  `json:"mentionContexts"`
	KnowledgeID           []any  `json:"knowledgeId"`
	KnowledgeName         []any  `json:"knowledgeName"`
	CodebaseID            string `json:"codebaseId"`
	MentionContextCount   int    `json:"mentionContextCount"`
	Command               string `json:"command"`
	ExpertID              string `json:"expertId"`
	RecommendID           string `json:"recommendId"`
	SkillID               string `json:"skillId"`
	SkillCount            int    `json:"skillCount"`
	TotalCount            int    `json:"totalCount"`
	FileURI               string `json:"fileUri"`
	PresentAt             int64  `json:"presentAt"`
	TraceID               string `json:"traceId"`
	RootRequestID         string `json:"rootRequestId"`
	ParentConversationID  string `json:"parentConversationId"`
	AgentName             string `json:"agentName"`
	AgentType             string `json:"agentType"`
	UserID                string `json:"userId"`
}

// buildActivityEvent 构造每日一条 chat_request_send 活跃事件（默认值对照 reference）。
func buildActivityEvent(cred wbauth.Credential) chatRequestEvent {
	now := time.Now().UnixMilli()
	conversationID := fmt.Sprintf("wbcpa-%d", now)
	return chatRequestEvent{
		EventCode:             "chat_request_send",
		Timestamp:             now,
		Mode:                  "craft",
		ConversationID:        conversationID,
		RequestID:             conversationID,
		InputLength:           12,
		RequestModelID:        "deepseek-v4-flash",
		RequestModelName:      "DeepSeek V4 Flash",
		MentionContexts:       []any{},
		KnowledgeID:           []any{},
		KnowledgeName:         []any{},
		PresentAt:             now,
		RootRequestID:         conversationID,
		ParentConversationID:  conversationID,
		AgentName:             "default",
		AgentType:             "conversation",
		UserID:                cred.UID,
	}
}

// sendActivityReport 上报一次对话活跃（body 为 JSON 数组，billing 域共用 BillingHeaders）。
func sendActivityReport(client *http.Client, realm string, cred wbauth.Credential) error {
	if cred.UID == "" {
		return fmt.Errorf("activity_missing_uid")
	}
	raw, err := json.Marshal([]chatRequestEvent{buildActivityEvent(cred)})
	if err != nil {
		return err
	}
	_, err = doBillingJSON(client, realm, cred, http.MethodPost, activityReportPath, json.RawMessage(raw))
	return err
}
