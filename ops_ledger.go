package main

import (
	"sync"
	"time"
)

// ledgerCap is the ring buffer capacity for credits ledger entries.
const ledgerCap = 200

// ledgerEntry records a single credits balance observation.
type ledgerEntry struct {
	Ts      time.Time `json:"ts"`
	AuthID  string    `json:"auth_id"`
	Delta   int64     `json:"delta"`
	Balance int64     `json:"balance"`
	Source  string    `json:"source"` // "ticker" | "manual"
}

// creditsLedger is a thread-safe ring buffer of ledger entries.
type creditsLedger struct {
	mu      sync.Mutex
	entries []ledgerEntry
	head    int
	size    int
}

// newCreditsLedger creates a ledger with the given capacity.
func newCreditsLedger(cap int) *creditsLedger {
	if cap <= 0 {
		cap = ledgerCap
	}
	return &creditsLedger{entries: make([]ledgerEntry, cap)}
}

// append adds an entry to the ring buffer.
func (l *creditsLedger) append(e ledgerEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[l.head] = e
	l.head = (l.head + 1) % len(l.entries)
	if l.size < len(l.entries) {
		l.size++
	}
}

// snapshot returns a copy of all entries in chronological order.
func (l *creditsLedger) snapshot() []ledgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size == 0 {
		return nil
	}
	out := make([]ledgerEntry, l.size)
	start := 0
	if l.size == len(l.entries) {
		start = l.head
	}
	for i := range out {
		out[i] = l.entries[(start+i)%len(l.entries)]
	}
	return out
}

// snapshotByAuth returns entries filtered by authID.
func (l *creditsLedger) snapshotByAuth(authID string) []ledgerEntry {
	all := l.snapshot()
	out := make([]ledgerEntry, 0, len(all))
	for _, e := range all {
		if e.AuthID == authID {
			out = append(out, e)
		}
	}
	return out
}

// globalLedger is the package-level ledger instance.
var globalLedger = newCreditsLedger(ledgerCap)

// lastLedgerTs returns the latest ledger observation timestamp for authID, or "".
func lastLedgerTs(authID string) string {
	entries := globalLedger.snapshotByAuth(authID)
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].Ts.Format("2006-01-02 15:04:05")
}
