package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// deriveConversationID 返回出站会话 ID，契约是「绝不返回空串」。
// 取值优先级与 deriveStickyKey 保持一致：X-Conversation-ID > X-Session-ID >
// Metadata["conversation_id"]，全缺时生成 UUIDv4。
// 不可省略：2026-10-17 现网探针实证，缺该头上游 100% 返回 11128
// request illegal，补任意非空值即恢复 200。
func deriveConversationID(headers http.Header, metadata map[string]any) string {
	if v := headers.Get("X-Conversation-ID"); v != "" {
		return v
	}
	if v := headers.Get("X-Session-ID"); v != "" {
		return v
	}
	if metadata != nil {
		if v, ok := metadata["conversation_id"].(string); ok && v != "" {
			return v
		}
	}
	return newConversationID()
}

// newConversationID 生成 UUIDv4 形态的 36 字符 ID。
func newConversationID() string {
	var b [16]byte
	// 熵不可用时退化为全零 UUID 形态，仍满足「非空」契约。
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
