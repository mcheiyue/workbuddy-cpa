package wbexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultClientVersion = "5.5.4"
	defaultCliVersion    = "2.137.1"
	chatCompletionsPath  = "/v2/chat/completions"
	originRefererCN      = "https://www.codebuddy.cn"
	originRefererGlobal  = "https://www.workbuddy.ai"
)

// BuildChatHeaders 在 req 上设置 WorkBuddy chat 出站所需的全部请求头。
// ref: workbuddy2api/internal/upstream/headers.go ChatHeaders + CommonHeaders
func BuildChatHeaders(req *http.Request, cred Credential, conversationID, messageID, traceID string) {
	h := req.Header
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/event-stream")
	h.Set("X-Requested-With", "XMLHttpRequest")
	// Origin/Referer 按 realm 切。
	origin := originRefererFor(cred.Realm)
	h.Set("Origin", origin)
	h.Set("Referer", origin+"/")
	// User-Agent: 三段式 WorkBuddy/<ver> <platform>/<ver> CLI/<cliVer>。
	h.Set("User-Agent", workBuddyUA(cred.Realm))
	h.Set("X-CodeBuddy-Request", "1")
	h.Set("Accept-Language", acceptLanguage(cred.Realm))
	// Authorization: Bearer token 或 X-No-Authorization。
	if cred.AccessToken != "" {
		h.Set("Authorization", "Bearer "+cred.AccessToken)
	} else {
		h.Set("X-No-Authorization", "1")
	}
	// X-User-Id。
	if cred.UID != "" {
		h.Set("X-User-Id", cred.UID)
	} else {
		h.Set("X-No-User-Id", "1")
	}
	// Enterprise/Domain 头按 realm 分发。
	if cred.Realm == "global" {
		h.Set("X-No-Enterprise-Id", "1")
		h.Set("X-Domain", "www.workbuddy.ai")
	} else {
		if cred.EnterpriseID != "" {
			h.Set("X-Enterprise-Id", cred.EnterpriseID)
		} else {
			h.Set("X-No-Enterprise-Id", "1")
		}
		if cred.Domain != "" {
			h.Set("X-Domain", cred.Domain)
		} else {
			h.Set("X-No-Department-Info", "1")
		}
	}
	// 用量归属头：伪造 WorkBuddy 桌面端指纹。
	h.Set("X-Agent-Purpose", "conversation")
	h.Set("X-IDE-Name", "WorkBuddy")
	h.Set("X-IDE-Type", "WorkBuddy")
	h.Set("X-IDE-Version", defaultClientVersion)
	h.Set("X-Product", "WorkBuddy")
	// X-Device-Token（有才设）。
	if cred.DeviceToken != "" {
		h.Set("X-Device-Token", cred.DeviceToken)
	}
	// 会话头族。
	if conversationID != "" {
		h.Set("X-Conversation-ID", conversationID)
	}
	if messageID == "" {
		messageID = newMessageID()
	}
	if traceID == "" {
		traceID = messageID
	}
	h.Set("X-Conversation-Request-ID", messageID)
	h.Set("X-Conversation-Message-ID", messageID)
	h.Set("X-Request-ID", messageID)
	h.Set("X-Root-Request-ID", messageID)
	h.Set("X-Trace-ID", traceID)
	// B3 链路头。
	b3Trace := messageID
	if validB3TraceID(b3Trace) {
		h.Set("X-B3-TraceId", b3Trace)
	} else {
		h.Set("X-B3-TraceId", messageID)
	}
	h.Set("X-B3-SpanId", messageID[:16])
	h.Set("X-B3-Sampled", "1")
	// X-Machine-ID / X-Session-ID：按 uid 稳定派生。
	if cred.UID != "" {
		h.Set("X-Machine-ID", deriveAccountStableID(cred.UID, "machine"))
		h.Set("X-Session-ID", deriveAccountStableID(cred.UID, "session"))
	}
}

func originRefererFor(realm string) string {
	if realm == "global" {
		return originRefererGlobal
	}
	return originRefererCN
}

func acceptLanguage(realm string) string {
	if realm == "global" {
		return "en-US"
	}
	return "zh-CN"
}

func workBuddyUA(realm string) string {
	platform := "WorkBuddy"
	if realm == "global" {
		platform = "WorkBuddy AI"
	}
	return "WorkBuddy/" + defaultClientVersion + " " + platform + "/" + defaultClientVersion + " CLI/" + defaultCliVersion
}

// deriveAccountStableID 按 uid + 用途盐稳定派生 36 hex 设备/会话标识。
// ref: headers.go:deriveAccountStableID
func deriveAccountStableID(uid, purpose string) string {
	sum := sha256.Sum256([]byte("wb2a:" + purpose + ":" + uid))
	return hex.EncodeToString(sum[:18])
}

// newMessageID 生成 32 位 hex 消息 ID（对齐 ref: session.NewMessageID 语义）。
func newMessageID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("msg-%d", time.Now().UnixNano())))
	return hex.EncodeToString(sum[:16])
}

// validB3TraceID 判断 B3 TraceId 是否合法：16 或 32 位 hex。
func validB3TraceID(s string) bool {
	if len(s) != 16 && len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ChatPath 返回 chat completions 的上游路径。
func ChatPath() string { return chatCompletionsPath }
