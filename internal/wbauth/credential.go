// Package wbauth 解析 WorkBuddy 凭据（嵌套/扁平双形态），提供 realm 解析、
// Device OAuth start/poll 和 refresh rotation。
package wbauth

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Provider 是 WorkBuddy 在 CPA 中的 provider 标识。
const Provider = "workbuddy"

// Credential 是归一化后的 WorkBuddy 账号凭据。
type Credential struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64  // Unix 秒
	Domain       string
	Realm        string // "cn" 或 "global"
	UID          string
	EnterpriseID string
	Nickname     string
	DeviceToken  string
}

// toStorageJSON 返回嵌套形 JSON（StorageJSON 格式）。
type storageJSON struct {
	Auth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
		Domain       string `json:"domain"`
		Realm        string `json:"realm"`
	} `json:"auth"`
	Account struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	} `json:"account"`
	DeviceToken string `json:"device_token,omitempty"`
}

func (c *Credential) marshalStorage() []byte {
	var s storageJSON
	s.Auth.AccessToken = c.AccessToken
	s.Auth.RefreshToken = c.RefreshToken
	s.Auth.ExpiresAt = c.ExpiresAt
	s.Auth.Domain = c.Domain
	s.Auth.Realm = c.Realm
	s.Account.UID = c.UID
	s.Account.EnterpriseID = c.EnterpriseID
	s.Account.Nickname = c.Nickname
	s.DeviceToken = c.DeviceToken
	raw, _ := json.Marshal(s)
	return raw
}

// AuthData 转换为 CPA AuthData。fileName 为空时由调用方填充。
func (c *Credential) AuthData(fileName string) pluginapi.AuthData {
	label := c.Nickname
	if label == "" {
		label = c.UID
	}
	realm := ResolveRealm(c.Realm, c.Domain)
	return pluginapi.AuthData{
		Provider:    Provider,
		ID:          Provider + "-" + c.UID,
		FileName:    fileName,
		Label:       label,
		StorageJSON: c.marshalStorage(),
		Attributes:  map[string]string{"realm": realm},
		// ponytail: 过期时间直接取 ExpiresAt；若上游缺省则设为远未来避免立即触发刷新
		NextRefreshAfter: time.Unix(c.ExpiresAt, 0),
	}
}

// Parse 兼容嵌套形 {"auth":{...},"account":{...}} 和扁平形 {"accessToken":...}。
func Parse(raw []byte) (Credential, error) {
	if len(raw) == 0 {
		return Credential{}, fmt.Errorf("empty auth storage")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Credential{}, fmt.Errorf("storage_parse_error: %w", err)
	}
	if _, nested := probe["auth"]; nested {
		return parseNested(raw)
	}
	return parseFlat(raw)
}

func parseNested(raw []byte) (Credential, error) {
	var n struct {
		Auth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
			Domain       string `json:"domain"`
			Realm        string `json:"realm"`
		} `json:"auth"`
		Account struct {
			UID          string `json:"uid"`
			EnterpriseID string `json:"enterpriseId"`
			Nickname     string `json:"nickname"`
		} `json:"account"`
		DeviceToken string `json:"device_token"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return Credential{}, fmt.Errorf("storage_parse_error: %w", err)
	}
	c := Credential{
		AccessToken:  n.Auth.AccessToken,
		RefreshToken: n.Auth.RefreshToken,
		ExpiresAt:    n.Auth.ExpiresAt,
		Domain:       n.Auth.Domain,
		Realm:        n.Auth.Realm,
		UID:          n.Account.UID,
		EnterpriseID: n.Account.EnterpriseID,
		Nickname:     n.Account.Nickname,
		DeviceToken:  n.DeviceToken,
	}
	if strings.TrimSpace(c.AccessToken) == "" {
		return Credential{}, fmt.Errorf("parse_error: missing accessToken")
	}
	return c, nil
}

func parseFlat(raw []byte) (Credential, error) {
	var f struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
		Domain       string `json:"domain"`
		Realm        string `json:"realm"`
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
		DeviceToken  string `json:"device_token"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return Credential{}, fmt.Errorf("storage_parse_error: %w", err)
	}
	c := Credential{
		AccessToken:  f.AccessToken,
		RefreshToken: f.RefreshToken,
		ExpiresAt:    f.ExpiresAt,
		Domain:       f.Domain,
		Realm:        f.Realm,
		UID:          f.UID,
		EnterpriseID: f.EnterpriseID,
		Nickname:     f.Nickname,
		DeviceToken:  f.DeviceToken,
	}
	if strings.TrimSpace(c.AccessToken) == "" {
		return Credential{}, fmt.Errorf("parse_error: missing accessToken")
	}
	return c, nil
}
