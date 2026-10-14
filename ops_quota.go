package main

// runQuotaSnapshot 每日余额快照（global-only；CN 的快照挂在 checkin 成功路径）。
// 写入 credits-ledger ticker 行，让 global 的每日 +30 活跃奖（官方限时每日活跃奖励，
// 次日发放）在 WebUI 流水表留下可观察的日阶梯；上游失败记 FAIL、不写脏快照。
func (t *opsTicker) runQuotaSnapshot(acct accountInfo) {
	var qErr error
	defer func() {
		recover() // ticker never panics
		t.finishTask("quota", acct.authIndex, qErr)
	}()
	client, err := t.httpClient(acct.callbackID)
	if err != nil {
		qErr = err
		return
	}
	remain, _, _, _, qerr := resourceSummary(client, acct.realm, acct.cred)
	if qerr != nil {
		qErr = qerr
		return
	}
	globalLedger.append(ledgerEntry{
		Ts:      t.now(),
		AuthID:  acct.authIndex,
		Balance: remain,
		Source:  "ticker",
	})
}
