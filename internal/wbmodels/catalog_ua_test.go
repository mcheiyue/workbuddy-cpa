package wbmodels

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// doerFunc 让测试按 UA 返回不同目录（真上游按 UA 段门控目录规模）。
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResp(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}
}

// 锁：目录必须用两种 UA 各拉一次并取并集。
// 实测依据（2026-10-16 真上游）：WorkBuddy/ 前缀 UA 返回 global 27 / cn 60，
// 纯 CLI UA 返回 global 20 / cn 33，且纯 CLI 独有 default/glm-5.0/glm-4.7。
// 只换任一种都会丢模型——所以是并集，不是替换。
func TestFetchAndParse_DualUAMerge(t *testing.T) {
	var uas []string
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		ua := req.Header.Get("User-Agent")
		uas = append(uas, ua)
		if strings.HasPrefix(ua, "WorkBuddy/") {
			return jsonResp(req, `{"code":0,"data":{"models":[`+
				`{"id":"grok-4.7","name":"Grok 4.7"},`+
				`{"id":"step-5-preview","name":"Step 5 Preview"}]}}`), nil
		}
		return jsonResp(req, `{"code":0,"data":{"models":[`+
			`{"id":"glm-5.0","name":"GLM 5.0"},`+
			`{"id":"grok-4.7","name":"Grok 4.7"}]}}`), nil
	})

	models, err := FetchAndParse(context.Background(), doer, "global", "tok")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(models))
	for _, m := range models {
		got = append(got, m.ID)
	}
	want := []string{"glm-5.0", "grok-4.7", "step-5-preview"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("merged ids=%v, want %v（纯 CLI 独有项不得丢、WorkBuddy 独有项必须并入）", got, want)
	}
	if len(uas) != 2 {
		t.Fatalf("requests=%d uas=%v, want 2（两种 UA 各一次）", len(uas), uas)
	}
	if !strings.HasPrefix(uas[0], "CLI/") || !strings.HasPrefix(uas[1], "WorkBuddy/") {
		t.Fatalf("uas=%v, want [CLI/... WorkBuddy/...]（CLI 先拉以保住既有模型顺序与公开 ID）", uas)
	}
}

// 锁：单一 UA 失败不得让整个目录为空——另一 UA 的结果必须仍然返回。
// 上游按 UA 门控，任一门控抖动都会误伤注册面。
func TestFetchAndParse_DualUAPartialFailure(t *testing.T) {
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.Header.Get("User-Agent"), "CLI/") {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("boom")),
				Header:     make(http.Header), Request: req,
			}, nil
		}
		return jsonResp(req, `{"code":0,"data":{"models":[{"id":"grok-4.7","name":"Grok 4.7"}]}}`), nil
	})

	models, err := FetchAndParse(context.Background(), doer, "global", "tok")
	if err != nil {
		t.Fatalf("single-UA failure must not fail the whole fetch: %v", err)
	}
	if len(models) != 1 || models[0].ID != "grok-4.7" {
		t.Fatalf("models=%v, want [grok-4.7]", models)
	}
}

// 锁：两 UA 全失败才报错（且错误必须冒泡，不能静默返回空目录）。
func TestFetchAndParse_AllUAFail(t *testing.T) {
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("boom")),
			Header:     make(http.Header), Request: req,
		}, nil
	})
	if _, err := FetchAndParse(context.Background(), doer, "global", "tok"); err == nil {
		t.Fatal("want error when every UA fetch fails")
	}
}

// 锁：cn 的 WorkBuddy UA 平台段必须是 WorkBuddy（global 是 WorkBuddy AI）——
// 平台段按 realm 分叉是上游既有约定，写死会让 cn 拿不到门控后的目录。
func TestCatalogUserAgents_RealmAwarePlatform(t *testing.T) {
	global := catalogUserAgentsFor("global")
	cn := catalogUserAgentsFor("cn")
	if len(global) != 2 || len(cn) != 2 {
		t.Fatalf("global=%v cn=%v, want 2 each", global, cn)
	}
	if !strings.Contains(global[1], "WorkBuddy AI/") {
		t.Fatalf("global[1]=%q, want platform WorkBuddy AI", global[1])
	}
	if !strings.Contains(cn[1], "WorkBuddy/") || strings.Contains(cn[1], "WorkBuddy AI/") {
		t.Fatalf("cn[1]=%q, want platform WorkBuddy", cn[1])
	}
}
