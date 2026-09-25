package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// --- isAlreadyCheckin 幂等判定 ---

func TestIsAlreadyCheckin_BusinessCodes(t *testing.T) {
	for _, code := range []string{"10001", "14001"} {
		err := fmt.Errorf("upstream 200 code=%s: 今天已签到", code)
		if !isAlreadyCheckin(err) {
			t.Fatalf("code=%s: expected already", code)
		}
	}
}

func TestIsAlreadyCheckin_BareChineseMarkers(t *testing.T) {
	for _, marker := range []string{"今天已签到", "今日已签到", "已签到", "未开启", "未开放", "已过期"} {
		err := fmt.Errorf("%s", marker)
		if !isAlreadyCheckin(err) {
			t.Fatalf("bare %q: expected already", marker)
		}
	}
}

func TestIsAlreadyCheckin_StructuredEnglishMarkers(t *testing.T) {
	// 结构化错误（含 upstream + code=）里，英文短词也应匹配。
	for _, marker := range []string{"already", "inactive"} {
		err := fmt.Errorf("upstream 200 code=10001: %s today", marker)
		if !isAlreadyCheckin(err) {
			t.Fatalf("structured %q: expected already", marker)
		}
	}
}

func TestIsAlreadyCheckin_BareEnglishNotMatched(t *testing.T) {
	// 裸错误（无 upstream + code=）不应匹配英文短词。
	err := fmt.Errorf("address already in use: connection refused")
	if isAlreadyCheckin(err) {
		t.Fatal("bare 'already' should not match")
	}
}

func TestIsAlreadyCheckin_NilError(t *testing.T) {
	if isAlreadyCheckin(nil) {
		t.Fatal("nil should not be already")
	}
}

func TestIsAlreadyCheckin_UnrelatedError(t *testing.T) {
	err := fmt.Errorf("network timeout")
	if isAlreadyCheckin(err) {
		t.Fatal("timeout should not be already")
	}
}

// --- isSessionDead 12153 ---

func TestIsSessionDead_12153(t *testing.T) {
	err := fmt.Errorf("upstream 200 code=12153: session expired")
	if !isSessionDead(err) {
		t.Fatal("expected session dead for 12153")
	}
}

func TestIsSessionDead_OtherCode(t *testing.T) {
	err := fmt.Errorf("upstream 200 code=10001: today checked")
	if isSessionDead(err) {
		t.Fatal("10001 should not be session dead")
	}
}

func TestIsSessionDead_Nil(t *testing.T) {
	if isSessionDead(nil) {
		t.Fatal("nil should not be session dead")
	}
}

// --- checkinStatus 归一化 ---

func TestCheckinStatus_OK(t *testing.T) {
	if s := checkinStatus(nil); s != "OK" {
		t.Fatalf("status=%q, want OK", s)
	}
}

func TestCheckinStatus_Already(t *testing.T) {
	err := fmt.Errorf("今天已签到")
	if s := checkinStatus(err); s != "ALREADY" {
		t.Fatalf("status=%q, want ALREADY", s)
	}
}

func TestCheckinStatus_AuthInvalid(t *testing.T) {
	err := fmt.Errorf("upstream 200 code=12153")
	if s := checkinStatus(err); s != "AUTH_INVALID" {
		t.Fatalf("status=%q, want AUTH_INVALID", s)
	}
}

func TestCheckinStatus_Fail(t *testing.T) {
	err := fmt.Errorf("network timeout")
	if s := checkinStatus(err); s != "FAIL" {
		t.Fatalf("status=%q, want FAIL", s)
	}
}

// --- billing 路径 realm 路由 ---

func TestBillingMeterPaths_Global(t *testing.T) {
	paths := billingMeterPaths(wbauth.RealmGlobal)
	if len(paths) != 2 {
		t.Fatalf("global paths=%d, want 2", len(paths))
	}
	if paths[0] != "/billing/meter/get-user-resource" {
		t.Fatalf("first path=%q", paths[0])
	}
	if paths[1] != "/v2/billing/meter/get-user-resource" {
		t.Fatalf("second path=%q", paths[1])
	}
}

func TestBillingMeterPaths_CN(t *testing.T) {
	paths := billingMeterPaths(wbauth.RealmCN)
	if len(paths) != 1 {
		t.Fatalf("cn paths=%d, want 1", len(paths))
	}
	if paths[0] != "/v2/billing/meter/get-user-resource" {
		t.Fatalf("cn path=%q", paths[0])
	}
}

func TestCheckinMeterPaths_Global(t *testing.T) {
	paths := checkinMeterPaths(wbauth.RealmGlobal)
	if len(paths) != 2 {
		t.Fatalf("global checkin paths=%d, want 2", len(paths))
	}
	if paths[0] != "/billing/meter/daily-checkin" {
		t.Fatalf("first path=%q", paths[0])
	}
	if paths[1] != "/v2/billing/meter/daily-checkin" {
		t.Fatalf("second path=%q", paths[1])
	}
}

func TestCheckinMeterPaths_CN(t *testing.T) {
	paths := checkinMeterPaths(wbauth.RealmCN)
	if len(paths) != 1 {
		t.Fatalf("cn checkin paths=%d, want 1", len(paths))
	}
	if paths[0] != "/v2/billing/meter/daily-checkin" {
		t.Fatalf("cn path=%q", paths[0])
	}
}

// --- billingMeterJSON 404 回落 ---

func TestBillingMeterJSONFallbackOn404(t *testing.T) {
	callCount := 0
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if strings.Contains(r.URL.Path, "/billing/meter/") && !strings.Contains(r.URL.Path, "/v2/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "", "data": json.RawMessage(`{"ok":true}`)})
	})

	client := &http.Client{Transport: &mockTransport{handler: mockHandler}}
	paths := []string{"/billing/meter/get-user-resource", "/v2/billing/meter/get-user-resource"}
	_, err := billingMeterJSON(client, "global", "tok", http.MethodPost, paths, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("callCount=%d, want 2 (fallback from /billing → /v2/billing)", callCount)
	}
}

// --- uidTail 脱敏 ---

func TestUidTail(t *testing.T) {
	if got := uidTail("12345678"); got != "...5678" {
		t.Fatalf("uidTail=%q, want ...5678", got)
	}
	if got := uidTail("123"); got != "123" {
		t.Fatalf("uidTail short=%q, want 123", got)
	}
	if got := uidTail(""); got != "" {
		t.Fatalf("uidTail empty=%q", got)
	}
}
