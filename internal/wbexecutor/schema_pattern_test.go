package wbexecutor_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
)

func TestExecute_PreparesUnicodeToolPatternForOutgoingRequest(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []map[string]string{{"role": "user", "content": `keep \u0041 literal`}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       "accept_unicode",
				"parameters": map[string]any{"type": "string", "pattern": `^\uD83D\uDE00$`},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var outgoing []byte
	cfg := wbexecutor.Config{Doer: func(req *http.Request) (*http.Response, error) {
		outgoing, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"c1","choices":[]}`))}, nil
	}}
	if _, execErr := wbexecutor.Execute(context.Background(), cfg, wbexecutor.ExecuteRequest{PublicModelID: "m", Payload: payload}); execErr != nil {
		t.Fatal(execErr)
	}

	var body map[string]any
	if err := json.Unmarshal(outgoing, &body); err != nil {
		t.Fatal(err)
	}
	pattern := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)["pattern"].(string)
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("outgoing tool pattern is rejected by RE2: %q: %v", pattern, err)
	}
	if !compiled.MatchString("😀") {
		t.Errorf("normalized pattern must accept the decoded surrogate pair")
	}
	if compiled.MatchString("plain text") {
		t.Errorf("normalized pattern must reject other text")
	}
	messages := body["messages"].([]any)
	for _, rawMessage := range messages {
		message := rawMessage.(map[string]any)
		if message["role"] == "user" && message["content"] != `keep \u0041 literal` {
			t.Fatalf("ordinary message content was rewritten: %#v", message["content"])
		}
	}
}

func TestPreparePayload_NormalizesUnicodeEscapesOnlyInSchemaPatterns(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		match []string
		miss  []string
	}{
		{name: "scalar", in: `^\u0041$`, want: `^\x{0041}$`, match: []string{"A"}, miss: []string{`\u0041`, "B"}},
		{name: "braced scalar", in: `^\u{1F600}$`, want: `^\x{1F600}$`, match: []string{"😀"}, miss: []string{"x"}},
		{name: "surrogate range", in: `^[^\uD800-\uDFFF]*$`, want: `^[^\x{D800}-\x{DFFF}]*$`, match: []string{"ASCII", "😀"}},
		{name: "even escaped slash parity", in: `^\\u0041$`, want: `^\\u0041$`, match: []string{`\u0041`}},
		{name: "odd escaped slash parity", in: `^\\\u0041$`, want: `^\\\x{0041}$`},
		{name: "adjacent surrogate pair combined", in: `^\uD83D\uDE00$`, want: `^\x{1F600}$`, match: []string{"😀"}, miss: []string{"😁"}},
		{name: "surrogate pair in class retained", in: `^[\uD83D\uDE00]$`, want: `^[\uD83D\uDE00]$`},
		{name: "isolated surrogate retained", in: `^\uD83D$`, want: `^\uD83D$`},
		{name: "malformed retained", in: `^\u12G4$`, want: `^\u12G4$`},
		{name: "too large retained", in: `^\u{110000}$`, want: `^\u{110000}$`},
		{name: "unsupported RE2 retained", in: `^(?=x)\u0041$`, want: `^(?=x)\u0041$`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"tools": []any{map[string]any{"function": map[string]any{"parameters": map[string]any{"pattern": tc.in}}}}})
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(wbexecutor.PreparePayload(payload), &got); err != nil {
				t.Fatal(err)
			}
			pattern := got["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)["pattern"].(string)
			if pattern != tc.want {
				t.Fatalf("pattern = %q, want %q", pattern, tc.want)
			}
			if len(tc.match) == 0 && len(tc.miss) == 0 {
				return
			}
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				t.Fatalf("pattern compile: %v", err)
			}
			for _, value := range tc.match {
				if !compiled.MatchString(value) {
					t.Errorf("pattern %q should match %q", pattern, value)
				}
			}
			for _, value := range tc.miss {
				if compiled.MatchString(value) {
					t.Errorf("pattern %q should reject %q", pattern, value)
				}
			}
		})
	}
}

func TestPreparePayload_TraversesOnlySchemaValuedKeywords(t *testing.T) {
	pattern := `^\u0041$`
	schema := map[string]any{
		"properties":        map[string]any{"pattern": map[string]any{"pattern": pattern}},
		"patternProperties": map[string]any{`^\u0042$`: map[string]any{"pattern": pattern}},
		"$defs":             map[string]any{"d": map[string]any{"pattern": pattern}},
		"definitions":       map[string]any{"d": map[string]any{"pattern": pattern}},
		"dependentSchemas":  map[string]any{"x": map[string]any{"pattern": pattern}},
		"items":             []any{map[string]any{"pattern": pattern}},
		"additionalItems":   map[string]any{"pattern": pattern}, "additionalProperties": map[string]any{"pattern": pattern},
		"contains": map[string]any{"pattern": pattern}, "propertyNames": map[string]any{"pattern": pattern},
		"not": map[string]any{"pattern": pattern}, "if": map[string]any{"pattern": pattern},
		"then": map[string]any{"pattern": pattern}, "else": map[string]any{"pattern": pattern},
		"unevaluatedItems": map[string]any{"pattern": pattern}, "unevaluatedProperties": map[string]any{"pattern": pattern},
		"allOf": []any{map[string]any{"pattern": pattern}}, "anyOf": []any{map[string]any{"pattern": pattern}},
		"oneOf": []any{map[string]any{"pattern": pattern}}, "prefixItems": []any{map[string]any{"pattern": pattern}},
		"dependencies": map[string]any{"schema": map[string]any{"pattern": pattern}, "required": []any{"x"}},
		"default":      map[string]any{"pattern": pattern}, "examples": []any{map[string]any{"pattern": pattern}},
		"enum": []any{map[string]any{"pattern": pattern}}, "const": map[string]any{"pattern": pattern},
		"description": pattern, "x-extension": map[string]any{"pattern": pattern},
	}
	payload, err := json.Marshal(map[string]any{"tools": []any{map[string]any{"function": map[string]any{"parameters": schema}}}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(wbexecutor.PreparePayload(payload), &body); err != nil {
		t.Fatal(err)
	}
	got := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	want := `^\x{0041}$`
	if got["properties"].(map[string]any)["pattern"].(map[string]any)["pattern"] != want {
		t.Fatal("schema property named pattern was not traversed")
	}
	if got["patternProperties"].(map[string]any)[`^\u0042$`] == nil {
		t.Fatal("patternProperties key was rewritten")
	}
	for _, key := range []string{"default", "examples", "enum", "const", "description", "x-extension"} {
		data, err := json.Marshal(got[key])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `\u0041`) {
			t.Errorf("non-schema value %q was rewritten", key)
		}
	}
	if got["dependencies"].(map[string]any)["required"].([]any)[0] != "x" {
		t.Fatal("string-array dependencies were rewritten")
	}
	if got["items"].([]any)[0].(map[string]any)["pattern"] != want || got["allOf"].([]any)[0].(map[string]any)["pattern"] != want {
		t.Fatal("nested schema patterns were not traversed")
	}
}
