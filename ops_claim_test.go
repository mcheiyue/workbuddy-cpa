package main

import (
	"testing"
	"time"
)

func TestIsClaimAlreadyDone_409Duplicate(t *testing.T) {
	err := &upstreamError{status: 409, msg: "duplicate: already claimed this tier this month"}
	if !isClaimAlreadyDone(err) {
		t.Fatal("want true for 409 duplicate")
	}
}

func TestIsClaimAlreadyDone_409Chinese(t *testing.T) {
	err := &upstreamError{status: 409, msg: "重复领取"}
	if !isClaimAlreadyDone(err) {
		t.Fatal("want true for 409 重复领取")
	}
}

func TestIsClaimAlreadyDone_Nil(t *testing.T) {
	if isClaimAlreadyDone(nil) {
		t.Fatal("nil error should return false")
	}
}

func TestIsClaimAlreadyDone_403(t *testing.T) {
	err := &upstreamError{status: 403, msg: "记录天数不够"}
	if isClaimAlreadyDone(err) {
		t.Fatal("403 should not be claim-already-done")
	}
}

func TestIsClaimAlreadyDone_500(t *testing.T) {
	err := &upstreamError{status: 500, msg: "internal error"}
	if isClaimAlreadyDone(err) {
		t.Fatal("500 should not be claim-already-done")
	}
}

// --- isClaimNotEnoughDays ---

func TestIsClaimNotEnoughDays_403(t *testing.T) {
	err := &upstreamError{status: 403, msg: "记录天数不够，无法领取该奖励"}
	if !isClaimNotEnoughDays(err) {
		t.Fatal("want true for 403 记录天数不够")
	}
}

func TestIsClaimNotEnoughDays_Nil(t *testing.T) {
	if isClaimNotEnoughDays(nil) {
		t.Fatal("nil should return false")
	}
}

func TestIsClaimNotEnoughDays_409(t *testing.T) {
	err := &upstreamError{status: 409, msg: "duplicate"}
	if isClaimNotEnoughDays(err) {
		t.Fatal("409 should not be not-enough-days")
	}
}

// --- tierStatus ---

func TestTierStatus(t *testing.T) {
	redemption := growthRedemptionStatus{
		Tier7dStatus:  "claimed",
		Tier14dStatus: "available",
		Tier28dStatus: "locked",
	}
	tests := []struct {
		tier, want string
	}{
		{"7d", "claimed"},
		{"14d", "available"},
		{"28d", "locked"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := tierStatus(redemption, tt.tier); got != tt.want {
			t.Errorf("tierStatus(%s) = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

// --- Path constants ---

func TestGrowthPaths(t *testing.T) {
	if growthStreakPath != "/activity/growth/streak" {
		t.Errorf("streakPath = %q", growthStreakPath)
	}
	if growthRedeemPath != "/activity/growth/redeem" {
		t.Errorf("redeemPath = %q", growthRedeemPath)
	}
}

// --- OpsDailyTasks schedule contains claim at slot 3 ---

func TestOpsDailyTasksHasClaimSlot(t *testing.T) {
	if len(opsDailyTasks) != 4 {
		t.Fatalf("opsDailyTasks len=%d, want 4", len(opsDailyTasks))
	}
	found := false
	for _, task := range opsDailyTasks {
		if task.name == "claim" && task.hour == 11 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("opsDailyTasks missing 'claim' at hour 11")
	}
}

// --- buildWakes returns 4 entries ---

func TestBuildWakesReturns4Entries(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)
	wakes := buildWakes(now)
	if len(wakes) != 4 {
		t.Fatalf("wakes=%d, want 4", len(wakes))
	}
	names := make(map[string]bool)
	for _, w := range wakes {
		names[w.task.name] = true
	}
	if !names["claim"] {
		t.Fatal("wakes missing claim task")
	}
}

// --- claimCredits integration via mock ---
