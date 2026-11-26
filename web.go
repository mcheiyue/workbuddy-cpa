package main

import (
	"embed"
	"io/fs"
	"path"
)

//go:embed web
var webFiles embed.FS

// webAssetTypes 是管理面允许发布的前端资源类型。
var webAssetTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
}

type webAsset struct {
	body        []byte
	contentType string
}

// workbuddyWebAssets 按相对路径（如 "index.html"）索引 web/ 下全部可发布资源。
var workbuddyWebAssets = loadWorkbuddyWebAssets()

// workbuddyWebUI 保留为首页字节视图，兼容既有测试与宿主首页读取。
var workbuddyWebUI = workbuddyWebAssets["index.html"].body

// loadWorkbuddyWebAssets 枚举嵌入的 web/ 顶层文件；新增脚本或样式无需再改路由。
func loadWorkbuddyWebAssets() map[string]webAsset {
	root, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic("workbuddy: 嵌入资源缺少 web 目录: " + err.Error())
	}
	assets := map[string]webAsset{}
	err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		contentType, allowed := webAssetTypes[path.Ext(name)]
		if entry.IsDir() || path.Dir(name) != "." || !allowed {
			return nil
		}
		body, readErr := fs.ReadFile(root, name)
		if readErr != nil {
			return readErr
		}
		assets[name] = webAsset{body: body, contentType: contentType}
		return nil
	})
	if err != nil {
		panic("workbuddy: 读取嵌入资源失败: " + err.Error())
	}
	if _, ok := assets["index.html"]; !ok {
		panic("workbuddy: 嵌入资源缺少 index.html")
	}
	return assets
}
