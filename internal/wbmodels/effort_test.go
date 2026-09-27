package wbmodels

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRegistry_EffortsPerAuthAndPrefix(t *testing.T) {
	reg := NewRegistry()
	reg.StoreWithMeta("auth-1", map[string]string{"workbuddy/Model A": "model-a"}, map[string][]string{
		"workbuddy/Model A": {"low", "high"},
	})
	if got := reg.Efforts("auth-1", "Model A"); len(got) != 2 || got[1] != "high" {
		t.Fatalf("efforts=%v", got)
	}
	if got := reg.Efforts("auth-2", "workbuddy/Model A"); len(got) != 0 {
		t.Fatalf("cross-auth efforts=%v", got)
	}
}

func TestFetchAndRegister_StoresReasoningEfforts(t *testing.T) {
	raw := `{"code":0,"data":{"models":[` +
		`{"id":"model-a","name":"Model A","maxOutputTokens":4096,"reasoning":{"supportedEfforts":["low","high"]}},` +
		`{"id":"model-b","name":"Model B","maxOutputTokens":4096,"reasoning":{"effort":"high"}},` +
		`{"id":"model-c","name":"Model C","maxOutputTokens":4096}` +
		`]}}`
	reg := NewRegistry()
	_, err := FetchAndRegister(context.Background(), staticDoer{body: raw}, "cn", "token", "auth", reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Efforts("auth", "workbuddy/Model A"); len(got) != 2 || got[0] != "low" {
		t.Fatalf("model-a efforts=%v", got)
	}
	if got := reg.Efforts("auth", "workbuddy/Model B"); len(got) != 1 || got[0] != "high" {
		t.Fatalf("model-b efforts=%v", got)
	}
	if got := reg.Efforts("auth", "workbuddy/Model C"); len(got) != 0 {
		t.Fatalf("model-c efforts=%v", got)
	}
}

type staticDoer struct{ body string }

func (d staticDoer) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}
