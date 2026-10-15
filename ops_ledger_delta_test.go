package main

import "testing"

// --- ledger append 自动计算 delta（2026-10-15 修 WebUI「变化」死列） ---

// TestLedgerAppendComputesDelta 同 auth 相邻两条的差值即 delta；首条无基线为 0。
func TestLedgerAppendComputesDelta(t *testing.T) {
	l := newCreditsLedger(10)
	l.append(ledgerEntry{AuthID: "a1", Balance: 100})
	l.append(ledgerEntry{AuthID: "a1", Balance: 130})
	l.append(ledgerEntry{AuthID: "a1", Balance: 110})
	got := l.snapshot()
	want := []int64{0, 30, -20}
	if len(got) != 3 {
		t.Fatalf("entries=%d, want 3", len(got))
	}
	for i, w := range want {
		if got[i].Delta != w {
			t.Fatalf("entry[%d].Delta=%d, want %d", i, got[i].Delta, w)
		}
	}
}

// TestLedgerAppendDeltaPerAuthIsolated 不同 auth 交错时 delta 只看同 auth 上一条。
func TestLedgerAppendDeltaPerAuthIsolated(t *testing.T) {
	l := newCreditsLedger(10)
	l.append(ledgerEntry{AuthID: "a1", Balance: 100})
	l.append(ledgerEntry{AuthID: "a2", Balance: 50})
	l.append(ledgerEntry{AuthID: "a1", Balance: 140})
	l.append(ledgerEntry{AuthID: "a2", Balance: 60})
	got := l.snapshot()
	if got[1].Delta != 0 {
		t.Fatalf("a2 first Delta=%d, want 0", got[1].Delta)
	}
	if got[2].Delta != 40 {
		t.Fatalf("a1 second Delta=%d, want 40 (vs a1=100, not a2=50)", got[2].Delta)
	}
	if got[3].Delta != 10 {
		t.Fatalf("a2 second Delta=%d, want 10", got[3].Delta)
	}
}
