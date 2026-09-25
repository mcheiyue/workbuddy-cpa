package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// opsTicker manages per-account daily check-in tickers.
type opsTicker struct {
	mu               sync.Mutex
	started          bool
	spawned          map[string]bool // authIndex → already-spawned loop
	stopCh           chan struct{}
	wg               sync.WaitGroup
	now              func() time.Time
	tickHook         func(authID string, err error)
	hostHTTPFn       func(callbackID string) (*http.Client, error)
	callHostFn       func(string, any) (json.RawMessage, error)
	listCNAccountsFn func() []accountInfo
}

// EnsureOpsStarted starts per-account tickers for all known accounts.
func EnsureOpsStarted() { ops.start() }

// StopOpsTicker stops all tickers and waits for goroutine exit.
func StopOpsTicker() { ops.stop() }

var ops = &opsTicker{
	now:        time.Now,
	hostHTTPFn: newHostHTTPClient,
	callHostFn: callHostJSON,
}

func (t *opsTicker) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return
	}
	t.started = true
	t.spawned = make(map[string]bool)
	t.stopCh = make(chan struct{})
	t.wg.Add(1)
	go t.run()
}

func (t *opsTicker) stop() {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return
	}
	close(t.stopCh)
	t.started = false
	t.mu.Unlock()
	t.wg.Wait()
}

func (t *opsTicker) run() {
	defer t.wg.Done()
	select {
	case <-t.stopCh:
		return
	case <-time.After(2 * time.Second):
	}
	t.spawnTickers()
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.spawnTickers()
		}
	}
}

// spawnTickers starts one loop per not-yet-spawned account. Discovery runs
// outside the lock; spawning is deduped so the hourly re-scan only picks up
// NEW accounts instead of stacking duplicate loops per existing account.
func (t *opsTicker) spawnTickers() {
	accounts := t.discoverCNAccounts()
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started || t.spawned == nil {
		return
	}
	for _, acct := range accounts {
		if t.spawned[acct.authIndex] {
			continue
		}
		t.spawned[acct.authIndex] = true
		t.wg.Add(1)
		go t.accountLoop(acct)
	}
}

type accountInfo struct {
	authIndex  string
	realm      string
	cred       wbauth.Credential
	callbackID string
	fileName   string // 物理 auth 文件名（.json），保活写回用；运行时注入为 ""
	authID     string // 宿主 auth.ID，refresh 互斥键
}

func (t *opsTicker) discoverCNAccounts() []accountInfo {
	if t.listCNAccountsFn != nil {
		return t.listCNAccountsFn()
	}
	callFn := t.callHostFn
	if callFn == nil {
		callFn = callHostJSON
	}
	raw, err := callFn(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return nil
	}
	var result struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	var out []accountInfo
	for _, f := range result.Files {
		if !isWorkbuddyProvider(f) || f.AuthIndex == "" {
			continue
		}
		rawAuth, getErr := callFn(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: f.AuthIndex})
		if getErr != nil {
			continue
		}
		var auth pluginapi.HostAuthGetResponse
		if json.Unmarshal(rawAuth, &auth) != nil {
			continue
		}
		cred, perr := wbauth.Parse(auth.JSON)
		if perr != nil || cred.AccessToken == "" {
			continue
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		if realm == wbauth.RealmGlobal {
			continue
		}
		out = append(out, accountInfo{
			authIndex:  f.AuthIndex,
			realm:      realm,
			cred:       cred,
			callbackID: f.AuthIndex,
			fileName:   f.Name,
			authID:     f.ID,
		})
	}
	return out
}

func (t *opsTicker) runCheckin(acct accountInfo) {
	var checkErr error
	defer func() {
		recover() // ticker never panics
		if t.tickHook != nil {
			t.tickHook(acct.authIndex, checkErr)
		}
	}()
	hostFn := t.hostHTTPFn
	if hostFn == nil {
		hostFn = newHostHTTPClient
	}
	client, err := hostFn(acct.callbackID)
	if err != nil {
		checkErr = err
		return
	}
	checkErr = doCheckin(client, acct.realm, acct.cred)
	if checkErr == nil || isAlreadyCheckin(checkErr) {
		remain, _, _, _, qerr := resourceSummary(client, acct.realm, acct.cred)
		if qerr == nil {
			globalLedger.append(ledgerEntry{
				Ts:      t.now(),
				AuthID:  acct.authIndex,
				Balance: remain,
				Source:  "ticker",
			})
		}
	}
}

func isWorkbuddyProvider(f pluginapi.HostAuthFileEntry) bool {
	return f.Provider == wbauth.Provider || f.Type == wbauth.Provider
}

// RecordManualLedger records a credits observation from a manual quota fetch.
func RecordManualLedger(authID string, balance int64) {
	globalLedger.append(ledgerEntry{
		Ts:      time.Now(),
		AuthID:  authID,
		Balance: balance,
		Source:  "manual",
	})
}
