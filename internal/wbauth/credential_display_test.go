package wbauth

import (
	"encoding/json"
	"testing"
)

// --- storageJSON 顶层显示元数据（2026-10-15 修宿主凭证管理页 email/account 全空） ---
// 宿主凭证管理页从 auth 文件顶层 metadata 键读显示值：metadata["email"] →
// email 列 + AccountInfo() 的 account 列（filestore.go:350 / types.go:583），
// metadata["label"] → label 列（filestore labelFor）。storageJSON 缺这两键 →
// 现网 workbuddy 条目 email=null/account=null（原生 provider 均有值）。

// TestMarshalStorageDisplayMetadata 顶层 email/label 必须在；nickname 为空时回退 uid。
func TestMarshalStorageDisplayMetadata(t *testing.T) {
	cred := Credential{AccessToken: "at", UID: "u1", Nickname: "nick"}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(cred.marshalStorage(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var email, label string
	if err, ok := raw["email"]; !ok {
		t.Fatal("storageJSON missing top-level email key")
	} else if err := json.Unmarshal(err, &email); err != nil {
		t.Fatalf("email decode: %v", err)
	}
	if email != "nick" {
		t.Fatalf("email=%q, want nickname", email)
	}
	if _, ok := raw["label"]; !ok {
		t.Fatal("storageJSON missing top-level label key")
	} else if err := json.Unmarshal(raw["label"], &label); err != nil {
		t.Fatalf("label decode: %v", err)
	}
	if label != "nick" {
		t.Fatalf("label=%q, want nickname", label)
	}

	// nickname 为空：email/label 回退 uid（宿主列不空）。
	bare := Credential{AccessToken: "at", UID: "u9"}
	var raw2 map[string]json.RawMessage
	if err := json.Unmarshal(bare.marshalStorage(), &raw2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var email2 string
	if err := json.Unmarshal(raw2["email"], &email2); err != nil || email2 != "u9" {
		t.Fatalf("email fallback=%q err=%v, want uid", email2, err)
	}
}

// TestParseIgnoresDisplayKeys 旧文件（无 email/label）与新文件（有）解析均不破——
// 显示键是纯附加，Parse 的嵌套/扁平路径都不读它们。
func TestParseIgnoresDisplayKeys(t *testing.T) {
	old := []byte(`{"type":"workbuddy","auth":{"accessToken":"at","refreshToken":"rt","expiresAt":1,"domain":"www.workbuddy.cn","realm":"cn"},"account":{"uid":"u1","nickname":"n"}}`)
	if _, err := Parse(old); err != nil {
		t.Fatalf("old-shape parse: %v", err)
	}
	neu := []byte(`{"type":"workbuddy","email":"n","label":"n","auth":{"accessToken":"at","refreshToken":"rt","expiresAt":1,"domain":"www.workbuddy.cn","realm":"cn"},"account":{"uid":"u1","nickname":"n"}}`)
	c, err := Parse(neu)
	if err != nil {
		t.Fatalf("new-shape parse: %v", err)
	}
	if c.UID != "u1" || c.AccessToken != "at" {
		t.Fatalf("new-shape cred=%+v", c)
	}
}
