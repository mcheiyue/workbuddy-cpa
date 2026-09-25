package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// --- Ledger ring buffer ---

func TestLedgerAppendAndSnapshot(t *testing.T) {
	l := newCreditsLedger(5)
	l.append(ledgerEntry{AuthID: "a1", Balance: 100, Source: "manual"})
	l.append(ledgerEntry{AuthID: "a2", Balance: 200, Source: "ticker"})
	snap := l.snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len=%d, want 2", len(snap))
	}
	if snap[0].AuthID != "a1" || snap[0].Balance != 100 {
		t.Fatalf("first entry: %+v", snap[0])
	}
	if snap[1].AuthID != "a2" || snap[1].Balance != 200 {
		t.Fatalf("second entry: %+v", snap[1])
	}
}

func TestLedgerCapEviction(t *testing.T) {
	l := newCreditsLedger(3)
	for i := range 10 {
		l.append(ledgerEntry{AuthID: fmt.Sprintf("e%d", i), Balance: int64(i)})
	}
	snap := l.snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len=%d, want 3", len(snap))
	}
	if snap[0].AuthID != "e7" || snap[2].AuthID != "e9" {
		t.Fatalf("eviction order wrong: %+v", snap)
	}
}

func TestLedgerSnapshotByAuth(t *testing.T) {
	l := newCreditsLedger(10)
	l.append(ledgerEntry{AuthID: "a", Balance: 10})
	l.append(ledgerEntry{AuthID: "b", Balance: 20})
	l.append(ledgerEntry{AuthID: "a", Balance: 30})
	snap := l.snapshotByAuth("a")
	if len(snap) != 2 {
		t.Fatalf("filtered len=%d, want 2", len(snap))
	}
	if snap[0].Balance != 10 || snap[1].Balance != 30 {
		t.Fatalf("filtered values: %+v", snap)
	}
}

func TestLedgerEmptySnapshot(t *testing.T) {
	l := newCreditsLedger(5)
	snap := l.snapshot()
	if snap != nil {
		t.Fatalf("expected nil, got %+v", snap)
	}
}

func TestLedgerDiffBetweenEntries(t *testing.T) {
	l := newCreditsLedger(10)
	l.append(ledgerEntry{AuthID: "x", Balance: 1000, Source: "manual"})
	l.append(ledgerEntry{AuthID: "x", Balance: 950, Source: "ticker"})
	snap := l.snapshotByAuth("x")
	if len(snap) != 2 {
		t.Fatalf("len=%d, want 2", len(snap))
	}
	delta := snap[1].Balance - snap[0].Balance
	if delta != -50 {
		t.Fatalf("delta=%d, want -50", delta)
	}
}

// --- Fake clock: tickHook fires on checkin ---

func TestOpsTicker_TickHookFires(t *testing.T) {
	var callCount atomic.Int32
	fakeNow := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	ticker := &opsTicker{
		now: func() time.Time { return fakeNow },
		hostHTTPFn: func(cb string) (*http.Client, error) {
			return nil, nil // runCheckin returns early on nil client
		},
		tickHook: func(id string, err error) {
			callCount.Add(1)
		},
	}
	acct := accountInfo{authIndex: "test1", realm: "cn", cred: wbauth.Credential{AccessToken: "tok"}, callbackID: "test1"}
	ticker.runCheckin(acct)
	if callCount.Load() != 1 {
		t.Fatalf("callCount=%d, want 1", callCount.Load())
	}
}

func TestEnsureOpsStarted_Idempotent(t *testing.T) {
	savedOps := ops
	defer func() { ops = savedOps }()

	ops = &opsTicker{
		now:              time.Now,
		stopCh:           make(chan struct{}),
		started:          true,
		listCNAccountsFn: func() []accountInfo { return nil },
	}
	// Should not panic or double-start.
	ops.start()
	ops.stop()
}

func TestStopOpsTicker_NeverStarted(t *testing.T) {
	savedOps := ops
	defer func() { ops = savedOps }()

	ops = &opsTicker{now: time.Now}
	ops.stop() // must not panic
}

// --- CN-only gating ---

func TestDiscoverCNAccounts_GlobalExcluded(t *testing.T) {
	ticker := &opsTicker{
		callHostFn: mockCallHost(t),
	}
	accounts := ticker.discoverCNAccounts()
	for _, acct := range accounts {
		if acct.realm == wbauth.RealmGlobal {
			t.Fatalf("global account should be excluded: %+v", acct)
		}
	}
	// Should have exactly 1 CN account.
	if len(accounts) != 1 {
		t.Fatalf("accounts len=%d, want 1 (only CN)", len(accounts))
	}
	if accounts[0].authIndex != "cn1" {
		t.Fatalf("expected cn1, got %s", accounts[0].authIndex)
	}
}

func mockCallHost(t *testing.T) func(string, any) (json.RawMessage, error) {
	t.Helper()
	return func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case "host.auth.list":
			files := []pluginapi.HostAuthFileEntry{
				{AuthIndex: "cn1", Provider: "workbuddy", Label: "CN User"},
				{AuthIndex: "global1", Provider: "workbuddy", Label: "Global User"},
			}
			return json.Marshal(map[string]any{"files": files})
		case "host.auth.get":
			// Extract auth_index from payload.
			var req pluginapi.HostAuthGetRequest
			raw, _ := json.Marshal(payload)
			if err := json.Unmarshal(raw, &req); err != nil {
				// Fallback: try to extract from raw bytes.
				return nil, fmt.Errorf("parse request: %w", err)
			}
			switch req.AuthIndex {
			case "cn1":
				cred := map[string]any{
					"auth": map[string]any{
						"accessToken":  "cn_token",
						"refreshToken": "cn_refresh",
						"expiresAt":    time.Now().Add(time.Hour).Unix(),
						"domain":       "codebuddy.cn",
						"realm":        "cn",
					},
					"account": map[string]any{
						"uid":          "cn_uid_123",
						"enterpriseId": "ent1",
						"nickname":     "CN User",
					},
				}
				credJSON, _ := json.Marshal(cred)
				return json.Marshal(pluginapi.HostAuthGetResponse{
					AuthIndex: "cn1",
					JSON:      credJSON,
				})
			case "global1":
				cred := map[string]any{
					"auth": map[string]any{
						"accessToken":  "global_token",
						"refreshToken": "global_refresh",
						"expiresAt":    time.Now().Add(time.Hour).Unix(),
						"domain":       "workbuddy.ai",
						"realm":        "global",
					},
					"account": map[string]any{
						"uid":          "global_uid_456",
						"enterpriseId": "ent2",
						"nickname":     "Global User",
					},
				}
				credJSON, _ := json.Marshal(cred)
				return json.Marshal(pluginapi.HostAuthGetResponse{
					AuthIndex: "global1",
					JSON:      credJSON,
				})
			}
		}
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

// --- Masked error on upstream failure ---

func TestCheckinErrorMask_TokenAbsent(t *testing.T) {
	err := fmt.Errorf("upstream 401: Bearer secret_leaked_token123 was rejected")
	masked := checkinErrorMask(err)
	if strings.Contains(masked, "secret_leaked_token123") {
		t.Fatalf("token leaked in masked error: %q", masked)
	}
	if !strings.Contains(masked, "[REDACTED]") {
		t.Fatalf("expected [REDACTED], got: %q", masked)
	}
}

func TestCheckinErrorMask_Truncation(t *testing.T) {
	longMsg := "x" + strings.Repeat("y", 200)
	err := fmt.Errorf("%s", longMsg)
	masked := checkinErrorMask(err)
	if len(masked) > 120 {
		t.Fatalf("masked error too long: %d chars", len(masked))
	}
}
