package main

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// authOwnedKeys 是插件 storageJSON 自有的顶层键：写回时以新载荷为准，
// 绝不从旧文件回填（防止把已轮换的 device_token 等复活）。
var authOwnedKeys = map[string]bool{"type": true, "auth": true, "account": true, "device_token": true}

// mergeForeignAuthMetadata 把旧物理文件中非自有的顶层键合并进新写回载荷。
// 宿主 saveAuthFile 是裸写（os.WriteFile，零合并），storageJSON 只含自有键——
// 不合并会抹掉宿主侧 priority/disabled 等键（Phase B marker 实证：保活写回把
// 注入的 priority=7 抹除）。自有键以新载荷为准，旧文件只贡献外来键。
func mergeForeignAuthMetadata(oldJSON, freshJSON []byte) ([]byte, error) {
	var old map[string]json.RawMessage
	if len(oldJSON) > 0 {
		if err := json.Unmarshal(oldJSON, &old); err != nil {
			return nil, fmt.Errorf("decode old auth metadata: %w", err)
		}
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(freshJSON, &merged); err != nil {
		return nil, fmt.Errorf("decode fresh auth payload: %w", err)
	}
	for k, v := range old {
		if authOwnedKeys[k] {
			continue
		}
		merged[k] = v
	}
	return json.Marshal(merged)
}

// authPhysicalJSON 取写回前的物理文件内容（宿主 auth.get 按 index 读盘，
// 含 priority/disabled 等宿主侧顶层键）。
func (t *opsTicker) authPhysicalJSON(authIndex string) ([]byte, error) {
	raw, err := t.callHost(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return nil, err
	}
	var resp pluginapi.HostAuthGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if len(resp.JSON) == 0 {
		return nil, fmt.Errorf("empty auth json for %s", authIndex)
	}
	return resp.JSON, nil
}
