package main

import (
	"encoding/json"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func testState(seed int64) *schedulerState {
	s := newSchedulerState()
	s.now = fixedClock(time.Now())
	s.rng = rand.New(rand.NewSource(seed))
	globalSchedulerState = s
	return s
}

func restoreState() { globalSchedulerState = newSchedulerState() }

// (a) delegate-on-empty-candidates
func TestSchedulerPick_DelegateOnEmpty(t *testing.T) {
	resp := schedulerPick(pluginapi.SchedulerPickRequest{})
	if resp.Handled {
		t.Fatal("expected Handled=false")
	}
	if resp.DelegateBuiltin != pluginapi.SchedulerBuiltinRoundRobin {
		t.Fatalf("delegate=%q, want round-robin", resp.DelegateBuiltin)
	}
}

// (b) sticky binding honored when header present
func TestSchedulerPick_StickyHonored(t *testing.T) {
	testState(42)
	defer restoreState()

	req := pluginapi.SchedulerPickRequest{
		Model:   "gpt-5",
		Options: pluginapi.SchedulerOptions{Headers: map[string][]string{"X-Conversation-ID": {"abc"}}},
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "auth-1", Priority: 10}, {ID: "auth-2", Priority: 20},
		},
	}
	resp := schedulerPick(req)
	if !resp.Handled {
		t.Fatal("first pick should be handled")
	}
	first := resp.AuthID
	resp2 := schedulerPick(req)
	if !resp2.Handled || resp2.AuthID != first {
		t.Fatalf("sticky not honored: got %q, want %q", resp2.AuthID, first)
	}
}

// (c) sticky skipped without key
func TestSchedulerPick_StickySkippedWithoutKey(t *testing.T) {
	testState(42)
	defer restoreState()

	resp := schedulerPick(pluginapi.SchedulerPickRequest{
		Model:      "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "auth-1", Priority: 10}},
	})
	if !resp.Handled || resp.AuthID != "auth-1" {
		t.Fatalf("expected auth-1 handled, got %+v", resp)
	}
}

// (d) cooldown excludes candidate
func TestSchedulerPick_CooldownExcludes(t *testing.T) {
	s := testState(42)
	defer restoreState()
	s.setCooldown("auth-1", "gpt-5")

	resp := schedulerPick(pluginapi.SchedulerPickRequest{
		Model: "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "auth-1", Priority: 10}, {ID: "auth-2", Priority: 20},
		},
	})
	if !resp.Handled || resp.AuthID != "auth-2" {
		t.Fatalf("expected auth-2, got %+v", resp)
	}
}

// all-cooldown delegates
func TestSchedulerPick_AllCooldownDelegates(t *testing.T) {
	s := testState(42)
	defer restoreState()
	s.setCooldown("auth-1", "gpt-5")
	s.setCooldown("auth-2", "gpt-5")

	resp := schedulerPick(pluginapi.SchedulerPickRequest{
		Model: "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "auth-1", Priority: 10}, {ID: "auth-2", Priority: 20},
		},
	})
	if resp.Handled {
		t.Fatal("expected delegation")
	}
	if resp.DelegateBuiltin != pluginapi.SchedulerBuiltinRoundRobin {
		t.Fatalf("delegate=%q, want round-robin", resp.DelegateBuiltin)
	}
}

// (e) credits weighting picks the credited candidate with seeded randomness
func TestSchedulerPick_CreditsWeighted(t *testing.T) {
	s := testState(42)
	defer restoreState()
	s.setCredits("auth-1", 100)
	s.setCredits("auth-2", 10)

	req := pluginapi.SchedulerPickRequest{
		Model: "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "auth-1", Priority: 10}, {ID: "auth-2", Priority: 20},
		},
	}
	counts := map[string]int{}
	for i := range 200 {
		resp := schedulerPick(req)
		if !resp.Handled {
			t.Fatalf("iteration %d: expected handled", i)
		}
		counts[resp.AuthID]++
	}
	if counts["auth-1"] < 150 {
		t.Fatalf("auth-1 should dominate, got %v", counts)
	}
}

// (f) concurrent Pick smoke — 100 goroutines, no data race
func TestSchedulerPick_Concurrent(t *testing.T) {
	s := testState(1)
	defer restoreState()
	s.setCooldown("auth-1", "gpt-5")
	s.setCredits("auth-2", 50)

	req := pluginapi.SchedulerPickRequest{
		Model:   "gpt-5",
		Options: pluginapi.SchedulerOptions{Headers: map[string][]string{"X-Conversation-ID": {"conv-1"}}},
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "auth-1", Priority: 10}, {ID: "auth-2", Priority: 20}, {ID: "auth-3", Priority: 30},
		},
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := schedulerPick(req)
			if !resp.Handled && resp.DelegateBuiltin == "" {
				t.Errorf("expected handled or delegate")
			}
		}()
	}
	wg.Wait()
}

// (g) registration: scheduler must be declared, model_router/request_interceptor forbidden
func TestSchedulerCapabilityDeclared(t *testing.T) {
	raw, err := json.Marshal(registration())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"scheduler":`) {
		t.Fatal("capabilities must contain scheduler")
	}
	for _, forbidden := range []string{"model_router", "request_interceptor"} {
		if strings.Contains(string(raw), `"`+forbidden+`":`) {
			t.Fatalf("capabilities must not contain %q", forbidden)
		}
	}
}

// (h) RPC round-trip
func TestHandleSchedulerMethod_RoundTrip(t *testing.T) {
	s := testState(42)
	defer restoreState()
	s.setCredits("auth-2", 100)

	req := pluginapi.SchedulerPickRequest{
		Provider:  "workbuddy",
		Providers: []string{"workbuddy"},
		Model:     "gpt-5",
		Stream:    true,
		Options:   pluginapi.SchedulerOptions{Headers: map[string][]string{"X-Conversation-ID": {"test-conv"}}},
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "auth-1", Provider: "workbuddy", Priority: 10, Status: "active"},
			{ID: "auth-2", Provider: "workbuddy", Priority: 20, Status: "active"},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := handleSchedulerMethod("scheduler.pick", raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true, envelope=%s", envelope)
	}
	var resp pluginapi.SchedulerPickResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Handled || resp.AuthID == "" {
		t.Fatalf("expected handled with AuthID, got %+v", resp)
	}
}

func TestHandleSchedulerMethod_InvalidJSON(t *testing.T) {
	envelope, err := handleSchedulerMethod("scheduler.pick", []byte(`{invalid`))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(envelope, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "invalid_request" {
		t.Fatalf("expected invalid_request error, got ok=%v err=%v", env.OK, env.Error)
	}
}
