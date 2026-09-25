package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// globalSchedulerState is the package-level state instance.
// Initialized once; all goroutine access is safe.
var globalSchedulerState = newSchedulerState()

// handleSchedulerMethod dispatches scheduler.pick RPC.
func handleSchedulerMethod(_ string, raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorEnvelopeStatus("invalid_request", "invalid scheduler pick request", http.StatusBadRequest), nil
	}
	resp := schedulerPick(req)
	return okEnvelope(resp)
}

// schedulerPick implements credits-weighted auth selection with session sticky
// and model-level cooldown exemption. Priority order:
//  1. Empty/no-workbuddy candidates → delegate round-robin.
//  2. All candidates in cooldown for requested model → delegate.
//  3. Session sticky: if a derivable sticky key maps to a usable candidate → pick it.
//  4. Credits weighting: higher credits = higher selection probability.
//  5. Priority fallback: highest-Priority usable candidate.
func schedulerPick(req pluginapi.SchedulerPickRequest) pluginapi.SchedulerPickResponse {
	candidates := usableCandidates(req.Candidates)
	if len(candidates) == 0 {
		return delegate()
	}

	model := req.Model

	// Step 2: if every usable candidate is in cooldown → delegate.
	allCooling := true
	for _, c := range candidates {
		if !globalSchedulerState.isCoolingDown(c.ID, model) {
			allCooling = false
			break
		}
	}
	if allCooling {
		return delegate()
	}

	// Step 3: session sticky.
	if key, ok := deriveStickyKey(req); ok {
		if authID, found := globalSchedulerState.stickyGet(key); found {
			if c := findCandidate(candidates, authID); c != nil && !globalSchedulerState.isCoolingDown(c.ID, model) {
				globalSchedulerState.stickySet(key, c.ID)
				return pick(c.ID)
			}
		}
	}

	// Step 4: credits weighting.
	nonCooling := filterCooling(candidates, model)
	if len(nonCooling) == 0 {
		return delegate()
	}
	if chosen := creditsWeightedPick(nonCooling); chosen != "" {
		if key, ok := deriveStickyKey(req); ok {
			globalSchedulerState.stickySet(key, chosen)
		}
		return pick(chosen)
	}

	// Step 5: highest priority (lowest numeric value = highest priority).
	sort.Slice(nonCooling, func(i, j int) bool {
		return nonCooling[i].Priority < nonCooling[j].Priority
	})
	chosen := nonCooling[0].ID
	if key, ok := deriveStickyKey(req); ok {
		globalSchedulerState.stickySet(key, chosen)
	}
	return pick(chosen)
}

// usableCandidates filters to non-empty-ID candidates with usable status.
// Status "active", "", or any unknown status is treated as usable (host owns
// status management; we only skip clearly disabled if we can identify one).
// Per host code, the host already filters disabled records before sending.
func usableCandidates(all []pluginapi.SchedulerAuthCandidate) []pluginapi.SchedulerAuthCandidate {
	var out []pluginapi.SchedulerAuthCandidate
	for _, c := range all {
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// filterCooling removes candidates currently in cooldown for the model.
func filterCooling(candidates []pluginapi.SchedulerAuthCandidate, model string) []pluginapi.SchedulerAuthCandidate {
	var out []pluginapi.SchedulerAuthCandidate
	for _, c := range candidates {
		if !globalSchedulerState.isCoolingDown(c.ID, model) {
			out = append(out, c)
		}
	}
	return out
}

// findCandidate returns the candidate with the given authID, or nil.
func findCandidate(candidates []pluginapi.SchedulerAuthCandidate, authID string) *pluginapi.SchedulerAuthCandidate {
	for i := range candidates {
		if candidates[i].ID == authID {
			return &candidates[i]
		}
	}
	return nil
}

// deriveStickyKey extracts a sticky key from request headers or metadata.
// Priority: X-Conversation-ID header > x-session-id header > "conversation_id"
// metadata field. Returns ("", false) if no key derivable.
func deriveStickyKey(req pluginapi.SchedulerPickRequest) (string, bool) {
	if vals := req.Options.Headers["X-Conversation-ID"]; len(vals) > 0 && vals[0] != "" {
		return "conv:" + vals[0], true
	}
	if vals := req.Options.Headers["x-session-id"]; len(vals) > 0 && vals[0] != "" {
		return "sess:" + vals[0], true
	}
	if v, ok := req.Options.Metadata["conversation_id"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return "conv:" + s, true
		}
	}
	return "", false
}

// creditsWeightedPick selects a candidate weighted by credits.
// Returns "" if no candidate has a known credits snapshot.
func creditsWeightedPick(candidates []pluginapi.SchedulerAuthCandidate) string {
	type weighted struct {
		id     string
		weight float64
	}
	var items []weighted
	total := 0.0
	const epsilon = 0.1 // ensures candidates with 0 credits still get a chance.
	for _, c := range candidates {
		cr, ok := globalSchedulerState.getCredits(c.ID)
		if !ok {
			continue // no snapshot → skip this layer.
		}
		w := math.Max(cr, 0) + epsilon
		items = append(items, weighted{id: c.ID, weight: w})
		total += w
	}
	if len(items) == 0 || total == 0 {
		return "" // no credits data → fall through to priority.
	}
	r := globalSchedulerState.float64() * total
	for _, item := range items {
		r -= item.weight
		if r <= 0 {
			return item.id
		}
	}
	// Floating-point tail.
	return items[len(items)-1].id
}

func delegate() pluginapi.SchedulerPickResponse {
	return pluginapi.SchedulerPickResponse{
		DelegateBuiltin: pluginapi.SchedulerBuiltinRoundRobin,
		Handled:         false,
	}
}

func pick(authID string) pluginapi.SchedulerPickResponse {
	return pluginapi.SchedulerPickResponse{
		AuthID:  authID,
		Handled: true,
	}
}
