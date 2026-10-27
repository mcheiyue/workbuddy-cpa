package wbexecutor

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 锁：11128 无法用任何请求形状复现（2026-10-27 十一组探测全 200），只能靠记录
// 真实被拦请求的脱敏特征定位。摘要必须能区分客户端指纹、工具、上下文规模，
// 但绝不能落任何正文内容——日志会被多人查看。
func TestSummarizeRequestKeepsShapeDropsContent(t *testing.T) {
	payload := []byte(`{"model":"m","stream":true,"reasoning_effort":"max",` +
		`"tools":[{"type":"function","function":{"name":"read"}}],` +
		`"messages":[{"role":"system","content":"You are Claude Code, Anthropic's official CLI."},` +
		`{"role":"user","content":"SECRET-USER-TEXT"}]}`)

	got := summarizeRequest(ExecuteRequest{PublicModelID: "m", Payload: payload, ConversationID: "conv-123456789"})

	for _, want := range []string{"tools=1", "msgs=2", "markers=claude-code", "effort=max", "stream=true", "sys_sha=", "sha="} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
	for _, banned := range []string{"SECRET-USER-TEXT", "Anthropic", "official CLI"} {
		if strings.Contains(got, banned) {
			t.Fatalf("summary leaked content %q: %s", banned, got)
		}
	}
}

// 锁：payload 不是 JSON 时也必须给出可用摘要，不能 panic、不能返回空串。
func TestSummarizeRequestUnparsable(t *testing.T) {
	got := summarizeRequest(ExecuteRequest{Payload: []byte("not-json")})
	if !strings.Contains(got, "unparsable") || !strings.Contains(got, "bytes=8") {
		t.Fatalf("unexpected: %s", got)
	}
}

// 锁：只有 11128 才记录。11101（非流不支持）等其它 ErrBadParams 也记录的话，
// 正常流量的日志会被噪声淹没，还会把无关请求的指纹写进日志。
func TestNoteChannelBlockOnlyFor11128(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	req := ExecuteRequest{PublicModelID: "m", Payload: []byte(`{"messages":[]}`)}
	noteChannelBlock(req, &ExecError{Kind: ErrBadParams, Msg: `{"code":11101}`})
	if buf.Len() != 0 {
		t.Fatalf("11101 must not be logged, got: %s", buf.String())
	}
	noteChannelBlock(req, &ExecError{Kind: ErrBadParams, Msg: `{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`})
	if !strings.Contains(buf.String(), "channel-blocked") || !strings.Contains(buf.String(), "11128") {
		t.Fatalf("11128 must be logged, got: %s", buf.String())
	}
}

// 锁：转储必须受哨兵文件控制（默认不落盘），且同一 payload 只落一份——
// 一次事故会对 8 个账号各触发一次，不去重就是 8 份同样的 658KB。
func TestDumpBlockedPayloadGatedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	oldEnable, oldDir := dumpEnablePath, dumpDirPath
	defer func() { dumpEnablePath, dumpDirPath = oldEnable, oldDir }()
	dumpEnablePath = filepath.Join(root, "enable")
	dumpDirPath = filepath.Join(root, "dump")

	req := ExecuteRequest{Payload: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)}

	dumpBlockedPayload(req)
	if _, err := os.Stat(dumpDirPath); !os.IsNotExist(err) {
		t.Fatalf("dump must stay off without sentinel")
	}

	if err := os.WriteFile(dumpEnablePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dumpBlockedPayload(req)
	entries, err := os.ReadDir(dumpDirPath)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly 1 dump, got %d (err=%v)", len(entries), err)
	}
	got, err := os.ReadFile(filepath.Join(dumpDirPath, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(req.Payload) {
		t.Fatalf("content mismatch: %q", got)
	}

	dumpBlockedPayload(req)
	entries, _ = os.ReadDir(dumpDirPath)
	if len(entries) != 1 {
		t.Fatalf("duplicate dump for same payload: %d files", len(entries))
	}
}
