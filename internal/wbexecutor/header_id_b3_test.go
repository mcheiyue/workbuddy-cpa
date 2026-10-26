package wbexecutor

import (
	"net/http"
	"regexp"
	"testing"
)

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$`)

// 锁：X-Machine-ID / X-Session-ID 必须是「带连字符的 UUID」形态。官方客户端的
// machineId/sessionId 都是带连字符的 UUID，36 位无连字符 hex 与真实客户端形态不符，
// 本身就是一处网关特征（ref wb_prompt/wb_identity 抓包结论）。同时必须按账号隔离：
// 所有账号共用一个值会反过来把它们关联到同一台设备上。
func TestBuildChatHeadersDeviceIDsAreUUIDShaped(t *testing.T) {
	cred := Credential{Realm: "global", UID: "uid-1", AccessToken: "t"}
	req, _ := http.NewRequest(http.MethodPost, "https://www.workbuddy.ai/chat", nil)
	BuildChatHeaders(req, cred, "conv", "", "")
	machine := req.Header.Get("X-Machine-ID")
	session := req.Header.Get("X-Session-ID")
	if !uuidShape.MatchString(machine) {
		t.Fatalf("X-Machine-ID=%q want a hyphenated UUID", machine)
	}
	if !uuidShape.MatchString(session) {
		t.Fatalf("X-Session-ID=%q want a hyphenated UUID", session)
	}
	if machine == session {
		t.Fatal("machine and session ids must differ")
	}

	again, _ := http.NewRequest(http.MethodPost, "https://www.workbuddy.ai/chat", nil)
	BuildChatHeaders(again, cred, "conv", "", "")
	if got := again.Header.Get("X-Machine-ID"); got != machine {
		t.Fatalf("machine id must be stable for one uid: %q vs %q", got, machine)
	}

	other, _ := http.NewRequest(http.MethodPost, "https://www.workbuddy.ai/chat", nil)
	BuildChatHeaders(other, Credential{Realm: "global", UID: "uid-2", AccessToken: "t"}, "conv", "", "")
	if other.Header.Get("X-Machine-ID") == machine {
		t.Fatal("different uids must not share one machine id")
	}
}
