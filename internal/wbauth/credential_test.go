package wbauth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseNestedValid(t *testing.T) {
	// Given: valid nested JSON.
	raw := []byte(`{"auth":{"accessToken":"at123","refreshToken":"rt456","expiresAt":999,"domain":"www.workbuddy.ai","realm":"global"},"account":{"uid":"u1","enterpriseId":"e1","nickname":"nick"},"device_token":"dt"}`)

	// When: parse.
	c, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}

	// Then: all fields preserved.
	if c.AccessToken != "at123" {
		t.Errorf("accessToken=%q", c.AccessToken)
	}
	if c.RefreshToken != "rt456" {
		t.Errorf("refreshToken=%q", c.RefreshToken)
	}
	if c.ExpiresAt != 999 {
		t.Errorf("expiresAt=%d", c.ExpiresAt)
	}
	if c.Domain != "www.workbuddy.ai" {
		t.Errorf("domain=%q", c.Domain)
	}
	if c.Realm != "global" {
		t.Errorf("realm=%q", c.Realm)
	}
	if c.UID != "u1" {
		t.Errorf("uid=%q", c.UID)
	}
	if c.EnterpriseID != "e1" {
		t.Errorf("enterpriseId=%q", c.EnterpriseID)
	}
	if c.Nickname != "nick" {
		t.Errorf("nickname=%q", c.Nickname)
	}
	if c.DeviceToken != "dt" {
		t.Errorf("deviceToken=%q", c.DeviceToken)
	}
}

func TestParseNestedMissingAccessToken(t *testing.T) {
	// Given: nested JSON with empty accessToken.
	raw := []byte(`{"auth":{"accessToken":"","refreshToken":"rt"},"account":{"uid":"u1"}}`)

	// When/Then: must fail.
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("expected error for missing accessToken")
	}
	if !strings.Contains(err.Error(), "missing accessToken") {
		t.Errorf("error=%q, want 'missing accessToken'", err.Error())
	}
}

func TestParseFlatValid(t *testing.T) {
	// Given: flat JSON.
	raw := []byte(`{"accessToken":"at","refreshToken":"rt","expiresAt":100,"uid":"u2","realm":"cn"}`)

	// When: parse.
	c, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}

	// Then: fields correct.
	if c.AccessToken != "at" || c.UID != "u2" || c.Realm != "cn" {
		t.Errorf("unexpected: %+v", c)
	}
}

func TestParseEmpty(t *testing.T) {
	_, err := Parse(nil)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "storage_parse_error") {
		t.Errorf("error=%q, want storage_parse_error", err.Error())
	}
}

func TestAuthDataRoundtrip(t *testing.T) {
	// Given: a credential.
	cred := Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 1000,
		Domain: "www.workbuddy.ai", Realm: "global",
		UID: "u1", EnterpriseID: "e1", Nickname: "nick",
	}

	// When: convert to AuthData.
	ad := cred.AuthData("test.json")

	// Then: AuthData fields correct.
	if ad.Provider != Provider {
		t.Errorf("provider=%q", ad.Provider)
	}
	if ad.ID != "workbuddy-u1" {
		t.Errorf("id=%q", ad.ID)
	}
	if ad.Label != "nick" {
		t.Errorf("label=%q", ad.Label)
	}
	if ad.FileName != "test.json" {
		t.Errorf("fileName=%q", ad.FileName)
	}
	if ad.Attributes["realm"] != "global" {
		t.Errorf("realm attr=%q", ad.Attributes["realm"])
	}

	// Then: StorageJSON roundtrips correctly.
	var s storageJSON
	if err := json.Unmarshal(ad.StorageJSON, &s); err != nil {
		t.Fatalf("storageJSON unmarshal: %v", err)
	}
	if s.Auth.AccessToken != "at" || s.Auth.Realm != "global" || s.Account.UID != "u1" {
		t.Errorf("storageJSON fields: %+v", s)
	}
}

func TestAuthDataEmptyFileNameFallsBackToIDJSON(t *testing.T) {
	// Given: a credential whose FileName is empty (login poll passes AuthData("")).
	cred := Credential{UID: "u9", Nickname: "n", ExpiresAt: 1000}

	// When: converted without a file name.
	ad := cred.AuthData("")

	// Then: file name falls back to ID+.json so host disk scan keeps the account after restart.
	if ad.FileName != "workbuddy-u9.json" {
		t.Fatalf("fileName=%q, want workbuddy-u9.json", ad.FileName)
	}
	if ad.ID != "workbuddy-u9" {
		t.Errorf("id=%q", ad.ID)
	}
}

func TestAuthDataLabelFallbackToUID(t *testing.T) {
	// Given: credential with no nickname.
	cred := Credential{AccessToken: "at", UID: "u1"}

	// When: AuthData.
	ad := cred.AuthData("")

	// Then: label falls back to UID.
	if ad.Label != "u1" {
		t.Errorf("label=%q, want uid fallback", ad.Label)
	}
}
