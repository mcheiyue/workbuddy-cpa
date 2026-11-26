package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"sort"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func managementGet(t *testing.T, requestPath string) (int, http.Header, []byte) {
	t.Helper()
	return managementRequest(t, http.MethodGet, requestPath)
}

func managementRequest(t *testing.T, method, requestPath string) (int, http.Header, []byte) {
	t.Helper()
	raw, err := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: requestPath})
	if err != nil {
		t.Fatal(err)
	}
	out, err := managementHandle(raw)
	if err != nil {
		t.Fatal(err)
	}
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatalf("envelope decode: %v (%s)", err, out)
	}
	if !envelope.OK {
		if envelope.Error == nil {
			t.Fatalf("error envelope missing detail: %s", out)
		}
		return envelope.Error.HTTPStatus, nil, nil
	}
	var response pluginapi.ManagementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatalf("management response decode: %v", err)
	}
	return response.StatusCode, response.Headers, response.Body
}

func TestWebAssetsServeEveryEmbeddedFile(t *testing.T) {
	if len(workbuddyWebAssets) < 3 {
		t.Fatalf("资源数量=%d，至少应含 index.html/style.css/app.js", len(workbuddyWebAssets))
	}
	names := make([]string, 0, len(workbuddyWebAssets))
	for name := range workbuddyWebAssets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		asset := workbuddyWebAssets[name]
		status, headers, body := managementGet(t, webResourcePrefix+name)
		if status != http.StatusOK {
			t.Fatalf("资源 %q status=%d", name, status)
		}
		if got := headers.Get("Content-Type"); got != asset.contentType {
			t.Fatalf("资源 %q Content-Type=%q want %q", name, got, asset.contentType)
		}
		if got := headers.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("资源 %q Cache-Control=%q want no-store", name, got)
		}
		if !bytes.Equal(body, asset.body) {
			t.Fatalf("资源 %q 字节与嵌入内容不一致", name)
		}
	}
}

func TestWebAssetsLegacyIndexPath(t *testing.T) {
	status, headers, body := managementGet(t, "/index.html")
	if status != http.StatusOK {
		t.Fatalf("legacy index status=%d", status)
	}
	if got := headers.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("legacy index Content-Type=%q", got)
	}
	if !bytes.Equal(body, workbuddyWebUI) {
		t.Fatal("legacy index body mismatch")
	}
}

func TestWebAssetsIndexReferencesRegisteredAndServed(t *testing.T) {
	status, headers, body := managementGet(t, "/index.html")
	if status != http.StatusOK {
		t.Fatalf("index status=%d", status)
	}
	if got := headers.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("index Content-Type=%q", got)
	}
	index := string(body)
	references := regexp.MustCompile(`(?:src|href)=["'](?:\./)?([^"'#?]+)["']`).FindAllStringSubmatch(index, -1)
	if len(references) == 0 {
		t.Fatal("index.html 未发现脚本或样式引用")
	}
	registered := registeredResourcePaths(t)
	for _, reference := range references {
		name := path.Base(reference[1])
		if name == "index.html" {
			continue
		}
		if !registered[webResourcePrefix+name] {
			t.Fatalf("index.html 引用的资源 %q 未注册", name)
		}
		status, _, body := managementGet(t, webResourcePrefix+name)
		if status != http.StatusOK || len(body) == 0 {
			t.Fatalf("引用资源 %q status=%d body=%d", name, status, len(body))
		}
	}
}

func registeredResourcePaths(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := handleMethod(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("register envelope=%s", raw)
	}
	var result struct {
		Resources []struct {
			Path string `json:"path"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool, len(result.Resources))
	for _, resource := range result.Resources {
		registered[resource.Path] = true
	}
	return registered
}

func TestWebAssetsRejectUnknownTraversalAndForeignPrefix(t *testing.T) {
	paths := []string{
		webResourcePrefix + "missing.js",
		webResourcePrefix + "../web.go",
		webResourcePrefix + "%2e%2e/web.go",
		webResourcePrefix + "nested/app.js",
		webResourcePrefix + "app.js/extra",
		webResourcePrefix + "README.md",
		"/v0/resource/plugins/other/workbuddy/index.html",
		"/v0/resource/plugins/workbuddy-extra/index.html",
		"/v0/management/style.css",
		"/style.css",
		"/app.js",
		"/index.html/extra",
	}
	for _, requestPath := range paths {
		status, _, body := managementGet(t, requestPath)
		if status != http.StatusNotFound {
			t.Fatalf("路径 %q status=%d body=%s，期望 404", requestPath, status, body)
		}
	}
}

func TestWebAssetsOnlyServedForGet(t *testing.T) {
	for _, requestPath := range []string{"/index.html", webResourcePrefix + "app.js"} {
		status, _, _ := managementRequest(t, http.MethodPost, requestPath)
		if status != http.StatusNotFound {
			t.Fatalf("POST %q status=%d，静态资源只应响应 GET", requestPath, status)
		}
	}
}

func TestWebAssetsLimitedToPublishableExtensions(t *testing.T) {
	if _, ok := workbuddyWebAssets["index.html"]; !ok {
		t.Fatal("缺少 index.html")
	}
	for name := range workbuddyWebAssets {
		if path.Dir(name) != "." {
			t.Fatalf("发布了非顶层资源: %q", name)
		}
		if _, allowed := webAssetTypes[path.Ext(name)]; !allowed {
			t.Fatalf("发布了非白名单扩展名: %q", name)
		}
	}
	for _, extension := range []string{".png", ".json", ".map", ".md"} {
		if _, allowed := webAssetTypes[extension]; allowed {
			t.Fatalf("%s 不应发布", extension)
		}
	}
}
