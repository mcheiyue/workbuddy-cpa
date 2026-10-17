package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// 锁：非本插件 provider 的候选必须交回宿主。宿主全进程只认一个插件调度器，
// 本 handler 可能收到 openai/gemini 等其它 provider 的候选；不设守卫就会替它们
// 选号，并让宿主的 session-affinity 失效（亲和只在插件返回 Handled=false 时才跑）。
func TestSchedulerPick_ForeignCandidatesDelegate(t *testing.T) {
	testState(42)
	defer restoreState()

	resp := schedulerPick(pluginapi.SchedulerPickRequest{
		Model: "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "ext-1", Provider: "openai-compatibility", Priority: 99},
			{ID: "ext-2", Provider: "gemini", Priority: 99},
		},
	})
	if resp.Handled {
		t.Fatalf("foreign candidates must delegate, got %+v", resp)
	}
	if resp.DelegateBuiltin != pluginapi.SchedulerBuiltinRoundRobin {
		t.Fatalf("delegate=%q, want round-robin", resp.DelegateBuiltin)
	}
}

// 锁：混合候选只在本插件账号里选——外来账号余额给到 1e6 也不得被选中。
func TestSchedulerPick_MixedCandidatesPickOwnOnly(t *testing.T) {
	s := testState(42)
	defer restoreState()
	s.setCredits("ext-1", 1_000_000)
	s.setCredits("own-1", 1)

	req := pluginapi.SchedulerPickRequest{
		Model: "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "ext-1", Provider: "openai-compatibility", Priority: 99},
			{ID: "own-1", Provider: pluginID, Priority: 10},
		},
	}
	for i := range 50 {
		resp := schedulerPick(req)
		if !resp.Handled {
			t.Fatalf("iteration %d: expected handled", i)
		}
		if resp.AuthID != "own-1" {
			t.Fatalf("iteration %d: picked %q, want own-1", i, resp.AuthID)
		}
	}
}

// 锁：优先级方向与宿主一致——数值大的优先（宿主 highestPriorityAuths 用
// `priority > bestPriority`）；按升序取最小会恰好选中宿主最不想要的桶。
func TestSchedulerPick_PriorityDescending(t *testing.T) {
	testState(42)
	defer restoreState()

	resp := schedulerPick(pluginapi.SchedulerPickRequest{
		Model: "gpt-5",
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "cn-1", Provider: pluginID, Priority: 0},
			{ID: "global-1", Provider: pluginID, Priority: 10},
		},
	})
	if !resp.Handled || resp.AuthID != "global-1" {
		t.Fatalf("want highest-priority global-1, got %+v", resp)
	}
}
