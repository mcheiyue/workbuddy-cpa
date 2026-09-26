package main

const (
	pluginID = "workbuddy"
	version  = "0.1.11"
	author   = "mcheiyue"
	repoURL  = "https://github.com/mcheiyue/workbuddy-cpa"
)

// registration 返回 Schema 6 注册元数据与能力声明。
func registration() map[string]any {
	return map[string]any{
		"schema_version": uint32(6),
		"metadata": map[string]any{
			"Name":             pluginID,
			"Version":          version,
			"Author":           author,
			"GitHubRepository": repoURL,
			"ConfigFields":     []map[string]any{},
		},
		"capabilities": map[string]any{
			"auth_provider":            true,
			"model_provider":           true,
			"executor":                 true,
			"management_api":           true,
			"quota_provider":           true,
			"usage_plugin":             true,
			"request_lifecycle_plugin": true,
			"scheduler":                true,
			"executor_model_scope":     "oauth",
			"executor_input_formats":   []string{"chat-completions"},
			"executor_output_formats":  []string{"chat-completions"},
		},
	}
}
