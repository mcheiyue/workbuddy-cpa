package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// activityReportPath 对照 reference report.go（billing 域每日活跃上报）。
const activityReportPath = "/v2/report"

// activityReportCount 每号每次活跃上报的条数。对照 reference schedule.go
// DefaultSchedule().ActivityReportCount=5：一次 10 点档内 5 条同 conversationId
// 的多轮对话，刷满 chat_5 门槛（领猫前置）。我们不做领猫，取值对齐 ref 只为
// 上报形状与真实多轮对话一致。
const activityReportCount = 5

// activityReportGap 同账号相邻两条上报的间隔（ref scheduler.go activityReportGap=1.5s，
// 防秒发风控）。var 以便测试置 0。
var activityReportGap = 1500 * time.Millisecond

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

// buildActivityEvent 构造 chat_request_send 活跃事件（默认值对照 reference）。
// conversationID 由调用方按会话生成，requestID 每条独立（ref 同会话多轮语义）。
func buildActivityEvent(cred wbauth.Credential, conversationID, requestID string) chatRequestEvent {
	now := time.Now().UnixMilli()
	return chatRequestEvent{
		EventCode:            "chat_request_send",
		Timestamp:            now,
		Mode:                 "craft",
		ConversationID:       conversationID,
		RequestID:            requestID,
		InputLength:          12,
		RequestModelID:       "deepseek-v4-flash",
		RequestModelName:     "DeepSeek V4 Flash",
		MentionContexts:      []any{},
		KnowledgeID:          []any{},
		KnowledgeName:        []any{},
		PresentAt:            now,
		RootRequestID:        conversationID,
		ParentConversationID: conversationID,
		AgentName:            "default",
		AgentType:            "conversation",
		UserID:               cred.UID,
	}
}

// sendActivityReport 上报一轮对话活跃：同 conversationId 连发 activityReportCount
// 条独立请求，requestId 各自独立，条间隔 activityReportGap（防秒发风控）。
// 单条失败即停（ref runActivity 同口径：不再续发，回读无意义）。
// body 为 JSON 数组，billing 域共用 BillingHeaders。
func sendActivityReport(client *http.Client, realm string, cred wbauth.Credential) error {
	if cred.UID == "" {
		return fmt.Errorf("activity_missing_uid")
	}
	conversationID := fmt.Sprintf("wbcpa-%d", time.Now().UnixMilli())
	for i := 1; i <= activityReportCount; i++ {
		requestID := fmt.Sprintf("%s-r%d", conversationID, i)
		raw, err := json.Marshal([]chatRequestEvent{buildActivityEvent(cred, conversationID, requestID)})
		if err != nil {
			return err
		}
		if _, err = doBillingJSON(client, realm, cred, http.MethodPost, activityReportPath, json.RawMessage(raw)); err != nil {
			return fmt.Errorf("report %d/%d: %w", i, activityReportCount, err)
		}
		if i < activityReportCount && activityReportGap > 0 {
			time.Sleep(activityReportGap)
		}
	}
	return nil
}

// checkActivityStreak 上报后回读连登天数（ref scheduler.go checkActivityStreak）。
// 背景：ref 实测「report 200 但静默丢弃」（缺 userId 时 progress 不动），
// 上报 200 ≠ streak 计分，需要回读验证闭环。
// 只记日志不改任务结果：回读失败/天数为 0 打 WARN，正常打一行便于对账。
func (t *opsTicker) checkActivityStreak(acct accountInfo, client *http.Client, cred wbauth.Credential) {
	st, err := growthStreak(client, acct.realm, cred)
	if err != nil {
		log.Printf("[wbops] activity auth=%s WARN streak check failed: %v", acct.authIndex, err)
		return
	}
	if st.Streak.Days == 0 {
		log.Printf("[wbops] activity auth=%s WARN report ok but streak days=0 (silent drop?)", acct.authIndex)
		return
	}
	log.Printf("[wbops] activity auth=%s streak days=%d", acct.authIndex, st.Streak.Days)
}
