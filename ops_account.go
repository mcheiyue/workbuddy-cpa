package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// authRefreshMutex 以账号为键合并并发 refresh（singleflight：同 key 只打一次上游，
// 后到者复用结果），防止并发用同一 refresh_token 轮换导致账号废止。
var authRefreshMutex = wbauth.NewRefreshMutex()

// opsDailyTask 每日运营任务槽（本地时间小时锚 + 0-30min 抖动，每个任务每天一次）。
// scope 为 realm 分表："" = 两 realm 都跑；wbauth.RealmCN / RealmGlobal = 仅该 realm。
type opsDailyTask struct {
	name  string
	hour  int
	scope string
	run   func(*opsTicker, accountInfo)
}

var opsDailyTasks = []opsDailyTask{
	{name: "checkin", hour: 9, scope: wbauth.RealmCN, run: (*opsTicker).runCheckin},     // ref D4：global 无签到体系，自动跳过防风控
	{name: "checkin", hour: 21, scope: wbauth.RealmCN, run: (*opsTicker).runCheckin},    // ref CheckinHours=[9,21] 两时点无条件都发，21 点是 9 点失败的第二次机会
	{name: "activity", hour: 10, run: (*opsTicker).runActivity},                         // ref PR #45：CN 与 global 都上报
	{name: "claim", hour: 11, scope: wbauth.RealmCN, run: (*opsTicker).runClaimCredits}, // ref：global growth 500，CN-only
	{name: "trial", hour: 12, scope: wbauth.RealmGlobal, run: (*opsTicker).runTrial},    // ref trial.go：global 专属一次性加油包
	{name: "keepalive", hour: 22, run: (*opsTicker).runKeepalive},                       // token 保活 realm 无关
}

// appliesTo 判断任务是否作用于该 realm 账号。
func (t opsDailyTask) appliesTo(realm string) bool {
	return t.scope == "" || t.scope == realm
}

// opsWake 任务未来触发时刻。
type opsWake struct {
	task opsDailyTask
	at   time.Time
}

// buildWakes 为该 realm 账号生成最近一次未来触发时刻；今天已过点则滚到明日。
// scope 过滤在构造时执行：被排除的任务零唤醒、零上游调用（结构性门控）。
// 每任务一次抖动抽签（0-30min），槽位间互不挤占。
func buildWakes(now time.Time, realm string) []opsWake {
	out := make([]opsWake, 0, len(opsDailyTasks))
	for _, task := range opsDailyTasks {
		if !task.appliesTo(realm) {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), task.hour, rand.Intn(30), 0, 0, now.Location())
		if !at.After(now) {
			at = at.Add(24 * time.Hour)
		}
		out = append(out, opsWake{task: task, at: at})
	}
	return out
}

// accountLoop 单账号运营循环：初始抖动后立即首轮签到，随后按槽位跑 checkin/activity/keepalive。
// 待办清空后按明日槽重建；stopCh 关闭即退出。
func (t *opsTicker) accountLoop(acct accountInfo) {
	defer t.wg.Done()
	jitter := time.Duration(rand.Intn(30)) * time.Minute
	timer := time.NewTimer(jitter)
	select {
	case <-t.stopCh:
		timer.Stop()
		return
	case <-timer.C:
	}
	// 首轮立即签到仅对 checkin 任务适用的 realm（global 跳过防风控，ref D4；
	// 按名查表而非硬编码下标，任务顺序变更不破坏门控）。
	for _, task := range opsDailyTasks {
		if task.name == "checkin" && task.appliesTo(acct.realm) {
			t.runCheckin(acct) // 立即首轮（10001 幂等兜底）
			break
		}
	}
	pending := buildWakes(t.now(), acct.realm)
	for {
		if len(pending) == 0 {
			pending = buildWakes(t.now(), acct.realm)
		}
		idx := 0
		for i, w := range pending {
			if w.at.Before(pending[idx].at) {
				idx = i
			}
		}
		wait := pending[idx].at.Sub(t.now())
		if wait < 0 {
			wait = 0
		}
		timer.Reset(wait)
		select {
		case <-t.stopCh:
			timer.Stop()
			return
		case <-timer.C:
		}
		task := pending[idx].task
		pending = append(pending[:idx], pending[idx+1:]...)
		if deadSessions.isDisabled(acct.authIndex) {
			return // 账号已禁用：停调度；重登换新 authIndex 后 spawn 重建
		}
		task.run(t, acct)
	}
}

// callHost 统一走 ticker 的 host RPC（测试经 callHostFn 注入，缺省 callHostJSON）。
func (t *opsTicker) callHost(method string, request any) (json.RawMessage, error) {
	callFn := t.callHostFn
	if callFn == nil {
		callFn = callHostJSON
	}
	return callFn(method, request)
}

// httpClient 解析账号出站 HTTP client（测试经 hostHTTPFn 注入；缺省控制面直连——后台无宿主分发回调）。
func (t *opsTicker) httpClient(callbackID string) (*http.Client, error) {
	hostFn := t.hostHTTPFn
	if hostFn == nil {
		hostFn = func(string) (*http.Client, error) { return newDirectControlClient() }
	}
	return hostFn(callbackID)
}

// freshCred 从宿主重读并解析最新凭据（活动/保活不使用 discover 时的陈旧快照）。
func (t *opsTicker) freshCred(acct accountInfo) (wbauth.Credential, error) {
	raw, err := t.callHost(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: acct.authIndex})
	if err != nil {
		return wbauth.Credential{}, err
	}
	var got pluginapi.HostAuthGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		return wbauth.Credential{}, fmt.Errorf("auth_get_unmarshal")
	}
	return wbauth.Parse(got.JSON)
}

// runActivity 每日对话活跃上报（CN+Global；ref PR #45 实测国际版 /v2/report 可用）。
// 上报成功后回读连登自检（ref checkActivityStreak）：200 ≠ streak 计分，发现静默丢弃。
func (t *opsTicker) runActivity(acct accountInfo) {
	var actErr error
	defer func() {
		recover() // ticker never panics
		t.finishTask("activity", acct.authIndex, actErr)
	}()
	cred, err := t.freshCred(acct)
	if err != nil {
		actErr = err
		return
	}
	client, err := t.httpClient(acct.callbackID)
	if err != nil {
		actErr = err
		return
	}
	actErr = sendActivityReport(client, acct.realm, cred)
	if actErr != nil {
		return // 单条失败已停发，回读无意义
	}
	t.checkActivityStreak(acct, client, cred)
}

// runKeepalive 每日 token 保活：刷新轮换后写回宿主。
// 失败不写回（旧 refresh_token 保持有效）；写回重试 3 次；无物理文件时交宿主懒刷新。
func (t *opsTicker) runKeepalive(acct accountInfo) {
	var kaErr error
	defer func() {
		recover() // ticker never panics
		t.finishTask("keepalive", acct.authIndex, kaErr)
	}()
	if acct.fileName == "" || !strings.HasSuffix(strings.ToLower(acct.fileName), ".json") {
		return // 运行时注入 auth：无物理文件可写，宿主懒刷新兜底
	}
	// E6① singleflight：同账号并发保活合并为一次上游刷新，后到者复用结果，
	// 避免多路各自轮换同一 refresh_token。
	_, kaErr = authRefreshMutex.Do(acct.authID, func() (wbauth.RefreshResult, error) {
		// 执行方重读：并发保活必须见到最新 refresh_token，防止复用已轮换的 RT（ref refresh_race 同款约束）。
		cred, err := t.freshCred(acct)
		if err != nil {
			return wbauth.RefreshResult{}, err
		}
		if cred.RefreshToken == "" {
			return wbauth.RefreshResult{}, nil
		}
		client, err := t.httpClient(acct.callbackID)
		if err != nil {
			return wbauth.RefreshResult{}, err
		}
		realm := wbauth.ResolveRealm(cred.Realm, cred.Domain)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		refreshed, err := wbauth.Refresh(ctx, client, wbauth.RealmBase(realm), wbauth.RealmOrigin(realm), cred)
		if err != nil {
			if isSessionDead(err) {
				deadSessions.note(acct.authIndex) // 连续 3 次 12153 → 禁用（B1/挂起#4）
			}
			return wbauth.RefreshResult{}, fmt.Errorf("%s", checkinErrorMask(err))
		}
		deadSessions.clear(acct.authIndex) // 刷新成功清误判计数（ref scheduler keepalive 语义）
		data := refreshed.Credential.AuthData(acct.fileName)
		var saveErr error
		for range 3 {
			_, saveErr = t.callHost(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{
				Name: acct.fileName,
				JSON: data.StorageJSON,
			})
			if saveErr == nil {
				return refreshed, nil // 宿主 upsert 内存记录，懒刷新路径同步看到新 token
			}
			time.Sleep(time.Second)
		}
		return refreshed, saveErr // 写回失败：不覆盖，下轮保活基于新视图重试
	})
}
