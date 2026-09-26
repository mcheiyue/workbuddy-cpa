package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func assertAsyncStreamChunks(t *testing.T, chunks []string) {
	t.Helper()
	if len(chunks) == 0 {
		t.Fatal("expected at least one emitted chunk")
	}
	for i, chunk := range chunks {
		if len(chunk) == 0 {
			t.Fatalf("chunk[%d] is empty", i)
		}
		if chunk[0] != '{' || !json.Valid([]byte(chunk)) {
			t.Fatalf("chunk[%d] is not bare JSON: %q", i, chunk)
		}
	}
	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(chunks[len(chunks)-1]), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Model != "workbuddy/stream-model" {
		t.Fatalf("last chunk model=%q, want workbuddy/stream-model", parsed.Model)
	}
}

func assertExecutorStreamReturnsBeforeCompletion(t *testing.T, rawReq []byte, finished <-chan struct{}) {
	t.Helper()
	resultCh := make(chan struct {
		raw []byte
		err error
	}, 1)
	go func() {
		raw, err := handleMethod(pluginabi.MethodExecutorExecuteStream, rawReq)
		resultCh <- struct {
			raw []byte
			err error
		}{raw: raw, err: err}
	}()
	var result struct {
		raw []byte
		err error
	}
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("executor.execute_stream did not return before stream completion")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(result.raw, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, envelope: %s", result.raw)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("background stream did not close")
	}
	var resp struct {
		Headers http.Header `json:"Headers"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if got := resp.Headers.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type=%q, want text/event-stream", got)
	}
}
