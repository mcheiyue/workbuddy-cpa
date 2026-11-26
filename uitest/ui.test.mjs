/*
 * WorkBuddy 管理面板：本地 mock 浏览器验收。
 *
 * 运行（在仓库根目录）：
 *   $env:PLAYWRIGHT_MODULE="C:/Users/admin/AppData/Roaming/npm/node_modules/@playwright/cli/node_modules/playwright"
 *   $env:CHROMIUM_PATH="C:/Users/admin/AppData/Local/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-win64/chrome-headless-shell.exe"
 *   node uitest/ui.test.mjs
 *
 * 只连接本机 mock，不访问生产、不写入真实账号。ONLY=layout 可只跑布局用例。
 */
import http from 'node:http';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { EventEmitter } from 'node:events';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const webDir = path.join(root, 'web');
const evidenceDir = path.join(here, 'evidence');
const PREFIX = '/v0/resource/plugins/workbuddy/';
const API = '/v0/management/workbuddy';
const AUTH = '/v0/management';
const ONLY = process.env.ONLY || '';

const pwSpecifier = process.env.PLAYWRIGHT_MODULE || 'playwright';
let pwImport = pwSpecifier;
if (!/^[a-zA-Z]+$/.test(pwSpecifier)) {
  const stat = await fs.stat(pwSpecifier);
  pwImport = pathToFileURL(stat.isDirectory() ? path.join(pwSpecifier, 'index.js') : pwSpecifier).href;
}
const playwrightModule = await import(pwImport);
const chromium = playwrightModule.chromium || (playwrightModule.default && playwrightModule.default.chromium);
if (!chromium) throw new Error('无法从 ' + pwSpecifier + ' 解析 chromium');

const events = new EventEmitter();
const mock = {};
let today = '';

function localToday() {
  const now = new Date();
  return now.getFullYear() + '-' + String(now.getMonth() + 1).padStart(2, '0') + '-' + String(now.getDate()).padStart(2, '0');
}

function freshAccounts() {
  return [
    { auth_index: 'p1', nickname: '13800138000', note: '主力手机号', uid_tail: '8041', realm: 'cn', expires_at: '2026-12-31 23:59:59', last_checkin: today + ' 08:12:00', priority: 10, file: 'workbuddy-p1.json', dead_count: 0 },
    { auth_index: 'p2', nickname: 'moqutu', note: '', uid_tail: '5520', realm: 'global', expires_at: '2026-12-15 00:00:00', last_checkin: '', priority: 0, file: 'workbuddy-p2.json', dead_count: 2 },
    { auth_index: 'p3', nickname: 'limited@example.com', note: '受限账号', uid_tail: '1199', realm: 'global', expires_at: '2026-11-30 00:00:00', last_checkin: '', priority: 5, file: 'workbuddy-p3.json', disabled: true, disabled_reason: '11140_account_restricted' }
  ];
}

function resetMock() {
  today = localToday();
  mock.key = 'test-key';
  mock.accounts = freshAccounts();
  mock.models = [
    { id: 'workbuddy/deepseek-v4.1-flash', internal_id: 'deepseek-v4.1-flash', display_name: 'DeepSeek V4.1 Flash', realm: 'global', credits: 'x1.00', source: 'global' },
    { id: 'workbuddy/deepseek-v4.1-flash', internal_id: 'deepseek-v4.1-flash', display_name: 'DeepSeek V4.1 Flash', realm: 'cn', credits: 'x0.50', source: 'cn' },
    { id: 'workbuddy/o3', internal_id: 'o3', display_name: '同名模型', realm: 'global', credits: 'x2.00', source: 'global' },
    { id: 'workbuddy/o4', internal_id: 'o4', display_name: '同名模型', realm: 'cn', credits: 'x0.00', source: 'cn' }
  ];
  mock.quota = {
    p1: { auth_index: 'p1', remain: 111, used: 39, size: 150, packages: 2 },
    p2: { auth_index: 'p2', remain: 222, used: 78, size: 300, packages: 1 },
    p3: { auth_index: 'p3', remain: 333, used: 17, size: 350, packages: 3 }
  };
  mock.ledger = {
    entries: [
      { ts: '2026-11-24T01:00:00Z', auth_id: 'p1', delta: 0, balance: 700, source: 'ticker' },
      { ts: '2026-11-25T01:00:00Z', auth_id: 'p1', delta: 30, balance: 730, source: 'ticker' },
      { ts: '2026-11-25T02:00:00Z', auth_id: 'p2', delta: -5, balance: 222, source: 'manual' }
    ],
    model_costs: { p1: { 'deepseek-v4.1-flash': { cost_per_1k: 0.25, samples: 4, last_seen: '2026-11-25T02:10:00Z' } } },
    note: '本地观测非上游权威'
  };
  mock.tasks = [
    { ts: '2026-11-25T01:00:00Z', task: 'keepalive', auth_id: 'p1', ok: true },
    { ts: '2026-11-25T02:00:00Z', task: 'checkin', auth_id: 'p2', ok: false, err: 'network' }
  ];
  mock.lastCheckins = [
    { auth_id: 'p1', ts: '2026-11-25 08:12:00', source: 'manual', status: 'OK' },
    { auth_id: 'p2', ts: '2026-11-25 09:00:00', source: 'ticker', status: 'TICKER' }
  ];
  mock.streaks = [
    { auth_index: 'p1', days: 12, tier_7d: 'available', tier_14d: 'claimed', tier_28d: 'unavailable' },
    { auth_index: 'p2', days: 3, error: 'unsupported realm' }
  ];
  mock.delays = { accounts: 0, accountsByKey: {}, quotaByIdx: {} };
  mock.failFields = {};
  mock.failAccounts = false;
  mock.failLedger = false;
  mock.failCheckin = false;
  mock.failStreak = false;
  mock.patchDelay = 0;
  mock.deleteDelay = 0;
  mock.auth = { url: 'https://auth.example.com/authorize?state=s1', state: 's1', status: 'pending', error: '', pollDelay: 0, urlDelay: 0 };
  mock.patches = [];
  mock.deletes = [];
  mock.claims = [];
  mock.lastCheckinBody = null;
  mock.calls = [];
  mock.completed = { accounts: 0, quota: 0, oauthPolls: 0 };
  events.removeAllListeners();
}

function sendJSON(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
  res.end(body);
}

function readBody(req) {
  return new Promise((resolve) => {
    let raw = '';
    req.on('data', (chunk) => { raw += chunk; });
    req.on('end', () => {
      try { resolve(raw ? JSON.parse(raw) : {}); } catch { resolve({}); }
    });
  });
}

function bearer(req) {
  return String(req.headers.authorization || '').replace(/^Bearer\s+/, '');
}

async function serveAsset(res, rawName) {
  const name = rawName || 'index.html';
  if (!/^[a-z0-9._-]+$/i.test(name) || name.includes('..')) return sendJSON(res, 404, { error: 'not found' });
  const types = { '.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8' };
  const type = types[path.extname(name)];
  if (!type) return sendJSON(res, 404, { error: 'not found' });
  try {
    const body = await fs.readFile(path.join(webDir, name));
    res.writeHead(200, { 'Content-Type': type, 'Cache-Control': 'no-store' });
    res.end(body);
  } catch {
    sendJSON(res, 404, { error: 'not found' });
  }
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const p = url.pathname;

  if (req.method === 'GET' && (p === '/' || p === PREFIX)) return serveAsset(res, 'index.html');
  if (req.method === 'GET' && p.startsWith(PREFIX)) return serveAsset(res, p.slice(PREFIX.length));
  if (!p.startsWith('/v0/management/')) return sendJSON(res, 404, { error: 'not found' });

  const key = bearer(req);
  mock.calls.push({ method: req.method, path: p, key });
  if (key !== mock.key) return sendJSON(res, 401, { error: 'unauthorized' });

  if (req.method === 'GET' && p === API + '/accounts') {
    const snapshot = structuredClone(mock.accounts);
    const delay = mock.delays.accountsByKey[key] != null ? mock.delays.accountsByKey[key] : mock.delays.accounts;
    return setTimeout(() => {
      if (mock.failAccounts) {
        sendJSON(res, 502, { error: 'accounts unavailable' });
      } else {
        sendJSON(res, 200, { accounts: snapshot });
      }
      mock.completed.accounts++;
      events.emit('reply', { type: 'accounts', key, count: mock.completed.accounts });
    }, delay);
  }
  if (req.method === 'GET' && p === API + '/models') {
    sendJSON(res, 200, { models: mock.models });
    events.emit('reply', { type: 'models' });
    return;
  }
  if (req.method === 'GET' && p === API + '/credits-ledger') {
    if (mock.failLedger) return sendJSON(res, 502, { error: 'ledger unavailable' });
    return sendJSON(res, 200, mock.ledger);
  }
  if (req.method === 'GET' && p === API + '/last-checkin') {
    if (mock.failCheckin) return sendJSON(res, 502, { error: 'checkin unavailable' });
    return sendJSON(res, 200, { last_checkins: mock.lastCheckins });
  }
  if (req.method === 'GET' && p === API + '/tasks') return sendJSON(res, 200, { tasks: mock.tasks });

  if (req.method === 'POST' && p === API + '/streak') {
    if (mock.failStreak) return sendJSON(res, 502, { error: 'streak unavailable' });
    return sendJSON(res, 200, { streaks: mock.streaks });
  }

  if (req.method === 'POST' && p === API + '/quota') {
    const body = await readBody(req);
    const idx = body.auth_index || '';
    const payload = idx ? (mock.quota[idx] || { error: 'unknown account' }) : { error: 'auth_index required' };
    const snapshot = structuredClone(payload);
    const delay = mock.delays.quotaByIdx[idx] || 0;
    return setTimeout(() => {
      sendJSON(res, 200, snapshot);
      mock.completed.quota++;
      events.emit('reply', { type: 'quota', idx });
    }, delay);
  }

  if (req.method === 'POST' && p === API + '/checkin') {
    const body = await readBody(req);
    mock.lastCheckinBody = structuredClone(body);
    const targets = body.auth_index ? mock.accounts.filter((a) => a.auth_index === body.auth_index) : mock.accounts;
    return sendJSON(res, 200, { results: targets.map((a) => ({ uid: a.uid_tail, status: a.realm === 'global' ? 'SKIP' : 'OK', message: a.realm === 'global' ? 'global 无签到体系' : '签到成功', remain: 120 })) });
  }

  if (req.method === 'POST' && p === API + '/benefits') {
    const body = await readBody(req);
    const targets = body.auth_index ? mock.accounts.filter((a) => a.auth_index === body.auth_index) : mock.accounts;
    return sendJSON(res, 200, {
      benefits: targets.map((a) => ({
        auth_index: a.auth_index, uid_tail: a.uid_tail, realm: a.realm,
        checkin: { active: a.realm !== 'global', today_checked_in: false, streak_days: 4, total_credits: 120 },
        gift: { active: true, claimed: false, credit_num: 50, end_time: '2026-12-01 00:00:00' },
        compensation: { active: false, claimed: false }
      }))
    });
  }

  if (req.method === 'POST' && p === API + '/benefits/claim') {
    const body = await readBody(req);
    const targets = body.auth_index ? mock.accounts.filter((a) => a.auth_index === body.auth_index) : mock.accounts;
    mock.claims.push(structuredClone(body));
    return sendJSON(res, 200, {
      results: targets.flatMap((a) => [
        { auth_index: a.auth_index, uid_tail: a.uid_tail, realm: a.realm, kind: 'gift', status: 'OK', credit_num: 50, message: '已领取' },
        { auth_index: a.auth_index, uid_tail: a.uid_tail, realm: a.realm, kind: 'compensation', status: 'SKIP', message: '不可领取' }
      ])
    });
  }

  if (req.method === 'GET' && p === AUTH + '/workbuddy-auth-url') {
    return setTimeout(() => {
      sendJSON(res, 200, { url: mock.auth.url, state: mock.auth.state });
      events.emit('reply', { type: 'auth-url' });
    }, mock.auth.urlDelay || 0);
  }
  if (req.method === 'GET' && p === AUTH + '/get-auth-status') {
    const count = ++mock.completed.oauthPolls;
    events.emit('oauth:poll', { count });
    const payload = { status: mock.auth.status };
    if (mock.auth.error) payload.error = mock.auth.error;
    return setTimeout(() => {
      sendJSON(res, 200, payload);
      events.emit('oauth:replied', { count });
    }, mock.auth.pollDelay);
  }

  if (p === AUTH + '/auth-files/fields' && req.method === 'PATCH') {
    const body = await readBody(req);
    mock.patches.push(structuredClone(body));
    const respond = () => {
      events.emit('reply', { type: 'patch', body });
      const failed = Object.keys(mock.failFields).find((field) => Object.prototype.hasOwnProperty.call(body, field));
      if (failed) {
        const spec = mock.failFields[failed];
        sendJSON(res, spec.status || 500, { error: spec.message || 'field write failed' });
        return;
      }
      const target = mock.accounts.find((a) => a.file === body.name);
      if (!target) {
        sendJSON(res, 404, { error: 'account not found' });
        return;
      }
      if (Object.prototype.hasOwnProperty.call(body, 'priority')) target.priority = Number(body.priority);
      if (Object.prototype.hasOwnProperty.call(body, 'note')) target.note = String(body.note);
      sendJSON(res, 200, { ok: true });
    };
    if (mock.patchDelay) setTimeout(respond, mock.patchDelay); else respond();
    return;
  }

  if (p === AUTH + '/auth-files' && req.method === 'DELETE') {
    const name = url.searchParams.get('name') || '';
    mock.deletes.push(name);
    const removed = mock.accounts.some((a) => a.file === name);
    const respond = () => {
      if (!removed) { sendJSON(res, 404, { error: 'account not found' }); return; }
      mock.accounts = mock.accounts.filter((a) => a.file !== name);
      sendJSON(res, 200, { ok: true });
      events.emit('reply', { type: 'delete', name });
    };
    if (mock.deleteDelay) setTimeout(respond, mock.deleteDelay); else respond();
    return;
  }

  return sendJSON(res, 404, { error: 'not found: ' + req.method + ' ' + p });
});

let browser;
let base = '';

const failures = [];
const tests = [];
function test(name, fn, options = {}) { tests.push({ name, fn, options }); }
function assert(condition, message) { if (!condition) throw new Error(message); }
async function must(page, selector, timeout = 4000) { await page.waitForSelector(selector, { timeout }); }

function waitReply(predicate, label, timeout = 6000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off('reply', onReply); reject(new Error('等待超时: ' + label)); }, timeout);
    function onReply(data) {
      if (!predicate(data)) return;
      clearTimeout(timer);
      events.off('reply', onReply);
      resolve(data);
    }
    events.on('reply', onReply);
  });
}

function waitOAuthPoll(min, timeout = 6000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off('oauth:poll', onPoll); reject(new Error('等待 OAuth 轮询超时')); }, timeout);
    function onPoll(data) {
      if (data.count < min) return;
      clearTimeout(timer);
      events.off('oauth:poll', onPoll);
      resolve(data);
    }
    events.on('oauth:poll', onPoll);
  });
}

function waitOAuthReply(min, timeout = 6000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { events.off('oauth:replied', onReply); reject(new Error('等待 OAuth 轮询响应超时')); }, timeout);
    function onReply(data) {
      if (data.count < min) return;
      clearTimeout(timer);
      events.off('oauth:replied', onReply);
      resolve(data);
    }
    events.on('oauth:replied', onReply);
  });
}

async function newPage(options = {}) {
  const context = await browser.newContext({ viewport: options.viewport || { width: 1230, height: 900 } });
  if (options.key !== null) {
    await context.addInitScript((value) => { localStorage.setItem('wb_mgmt_key', value); }, options.key || 'test-key');
  }
  await context.addInitScript(() => {
    window.__opened = [];
    window.open = (target) => {
      window.__opened.push(String(target));
      return { closed: false, close() {}, focus() {} };
    };
  });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  page.__errors = [];
  page.on('pageerror', (error) => page.__errors.push(String(error)));
  if (options.clock) await page.clock.install();
  await page.goto(base + PREFIX + 'index.html', { waitUntil: 'domcontentloaded' });
  return { context, page };
}

async function openLoginPanel(page) {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await page.locator('#loginOpen').click();
  await must(page, '#loginDialog[open]');
}

async function valueOf(page, selector) {
  return page.locator(selector).first().inputValue();
}
async function textOf(page, selector) {
  return ((await page.locator(selector).first().textContent()) || '').trim();
}

async function withTest(name, fn, options) {
  if (ONLY && !name.includes(ONLY)) return;
  resetMock();
  if (options.prepare) options.prepare();
  const { context, page } = await newPage(options);
  try {
    await fn(page);
    assert(page.__errors.length === 0, '页面脚本错误: ' + page.__errors.join(' | '));
    failures.push({ name, ok: true });
    console.log('PASS ' + name);
  } catch (error) {
    failures.push({ name, ok: false, error });
    console.log('FAIL ' + name + '\n  ' + (error && error.stack ? error.stack : error));
  } finally {
    await context.close();
  }
}

/* ------------------------------------------------------------------ */
/* 用例                                                               */
/* ------------------------------------------------------------------ */

test('layout: 六项任务导航、分区切换与宿主宽度适配', async (page) => {
  await must(page, '#tasknav [data-view="accounts"]');
  assert(await page.locator('#tasknav [data-view]').count() === 6, '期望 6 个任务导航项');
  const views = ['overview', 'accounts', 'quota', 'models', 'records', 'settings'];
  for (const view of views) {
    await page.locator('#tasknav [data-view="' + view + '"]').click();
    assert(await page.locator('[data-view-panel="' + view + '"]').isVisible(), '未显示视图 ' + view);
    assert(await page.locator('[data-view-panel="' + view + '"]:not([hidden])').count() === 1, '视图 ' + view + ' 未唯一显示');
  }
  const font = await page.evaluate(() => getComputedStyle(document.body).fontSize);
  assert(font === '15px', '桌面正文应为 15px，实际 ' + font);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert(overflow <= 1, '桌面出现页面级横向溢出: ' + overflow);
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#accountsList .account-row');
  await page.locator('#themeSelect').selectOption('light');
  await page.screenshot({ path: path.join(evidenceDir, 'accounts-1230-light.png'), fullPage: true });
  await page.locator('#themeSelect').selectOption('dark');
  await page.screenshot({ path: path.join(evidenceDir, 'accounts-1230-dark.png'), fullPage: true });
  await page.locator('#themeSelect').selectOption('light');
  await page.locator('#tasknav [data-view="quota"]').click();
  await page.screenshot({ path: path.join(evidenceDir, 'quota-1230-light.png'), fullPage: true });
  await page.locator('#tasknav [data-view="models"]').click();
  await must(page, '#modelsBody tr[data-model]');
  await page.screenshot({ path: path.join(evidenceDir, 'models-1230-light.png'), fullPage: true });
  await page.locator('#tasknav [data-view="records"]').click();
  await must(page, '#ledgerBody tr[data-ledger]');
  await page.screenshot({ path: path.join(evidenceDir, 'records-1230-light.png'), fullPage: true });
  await page.locator('#tasknav [data-view="settings"]').click();
  await page.screenshot({ path: path.join(evidenceDir, 'settings-1230-light.png'), fullPage: true });
  await page.locator('#tasknav [data-view="accounts"]').click();
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  await page.screenshot({ path: path.join(evidenceDir, 'editor-1230-light.png') });
  await page.locator('#accountCancel').click();
});

test('layout: 手机 390px 导航完整、正文 16px、控件 44px', async (page) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await must(page, '#tasknav [data-view="settings"]');
  for (const view of ['overview', 'accounts', 'quota', 'models', 'records', 'settings']) {
    const box = await page.locator('#tasknav [data-view="' + view + '"]').boundingBox();
    assert(box && box.x >= 0 && box.x + box.width <= 391, '手机导航项超出视口: ' + view);
  }
  const font = await page.evaluate(() => getComputedStyle(document.body).fontSize);
  assert(font === '16px', '手机正文应为 16px，实际 ' + font);
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#accountsList .account-row');
  const loginHeight = await page.locator('#loginOpen').evaluate((el) => el.getBoundingClientRect().height);
  assert(loginHeight >= 44, '手机主按钮高度应 >=44px，实际 ' + loginHeight);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert(overflow <= 1, '手机出现页面级横向溢出: ' + overflow);
  await page.locator('#themeSelect').selectOption('light');
  await page.screenshot({ path: path.join(evidenceDir, 'accounts-390-light.png'), fullPage: true });
  await page.locator('#themeSelect').selectOption('dark');
  await page.screenshot({ path: path.join(evidenceDir, 'accounts-390-dark.png'), fullPage: true });
  await page.locator('#themeSelect').selectOption('light');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  await page.screenshot({ path: path.join(evidenceDir, 'editor-390-light.png') });
  await page.locator('#accountCancel').click();
});

test('layout: 768px 宿主覆盖层宽度仍无溢出', async page => {
  await page.setViewportSize({ width: 768, height: 800 });
  await must(page, '#tasknav [data-view="accounts"]');
  for (const view of ['overview', 'accounts', 'quota', 'models', 'records', 'settings']) {
    const box = await page.locator('#tasknav [data-view="' + view + '"]').boundingBox();
    assert(box && box.x >= 0 && box.x + box.width <= 769, '768px 导航项超出视口: ' + view);
  }
  assert(await page.evaluate(() => getComputedStyle(document.body).fontSize) === '16px', '768px 应按手机正文 16px');
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#loginOpen');
  const loginHeight = await page.locator('#loginOpen').evaluate((el) => el.getBoundingClientRect().height);
  assert(loginHeight >= 44, '768px 主按钮应 >=44px');
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  assert(overflow <= 1, '768px 出现页面级横向溢出: ' + overflow);
});

test('accounts: 列表语义、备注优先、global 签到占位、禁用状态', async (page) => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#accountsList .account-row');
  assert(await page.locator('#accountsList .account-row').count() === 3, '账号行数应为 3');
  const first = await textOf(page, '[data-account="p1"]');
  assert(first.includes('主力手机号'), '显示名应优先使用备注: ' + first);
  assert(first.includes('手机(推断)'), '登录方式应推断为手机: ' + first);
  assert(first.includes('8041'), '应显示 UID 尾号');
  assert((await textOf(page, '[data-account="p2"] [data-label="上次签到"]')).includes('—'), 'global 账号上次签到应为 —');
  assert((await textOf(page, '[data-account="p3"]')).includes('已禁用'), '受限账号应显示已禁用');
});

test('account editor: 取消不写入、保存双字段、部分失败可解释', async (page) => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  assert(await valueOf(page, '#editNote') === '主力手机号', '编辑框应回显备注');
  await page.locator('#editNote').fill('取消不保存');
  await page.locator('#editPriority').fill('99');
  await page.locator('#accountCancel').click();
  await page.waitForSelector('#accountDialog[open]', { state: 'detached', timeout: 3000 }).catch(() => {});
  assert(await page.locator('#accountDialog[open]').count() === 0, '取消后对话框应关闭');
  assert(mock.patches.length === 0, '取消不应发送 PATCH: ' + JSON.stringify(mock.patches));
  assert(mock.accounts[0].note === '主力手机号', '取消后服务端备注不应变化');

  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  await page.locator('#editNote').fill('改后备注');
  await page.locator('#editPriority').fill('30');
  await page.locator('#accountSave').click();
  await page.waitForSelector('#accountDialog[open]', { state: 'detached', timeout: 5000 });
  assert(mock.patches.length === 2, '应发送两条独立 PATCH（优先级/备注），实际 ' + mock.patches.length);
  assert(mock.accounts[0].note === '改后备注' && mock.accounts[0].priority === 30, '保存后服务端字段未更新');
  await page.waitForFunction(() => document.querySelector('[data-account="p1"]')?.textContent.includes('改后备注'));

  mock.failFields.note = { status: 500, message: '备注写入被拒绝' };
  await page.locator('[data-edit="p2"]').click();
  await must(page, '#accountDialog[open]');
  await page.locator('#editNote').fill('不应保存的备注');
  await page.locator('#editPriority').fill('22');
  await page.locator('#accountSave').click();
  await must(page, '#accountError');
  const message = await textOf(page, '#accountError');
  assert(message.includes('优先级') && message.includes('备注'), '部分失败提示应说明哪项成功/失败: ' + message);
  assert(await page.locator('#accountDialog[open]').count() === 1, '部分失败后编辑框应保持打开');
  assert(mock.accounts[1].priority === 22, '优先级应先保存成功');
  assert(mock.accounts[1].note === '', '失败字段不应落盘');
});

test('account editor: 禁用账号备注受保护，仅保存优先级', async (page) => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p3"]');
  await page.locator('[data-edit="p3"]').click();
  await must(page, '#accountDialog[open]');
  const note = page.locator('#editNote');
  assert(await note.isDisabled() || await note.getAttribute('readonly') !== null, '禁用账号备注必须只读');
  assert((await note.inputValue()).includes('受限账号'), '只读备注应保留系统保护内容');
  await page.locator('#editPriority').fill('8');
  await page.locator('#accountSave').click();
  await page.waitForSelector('#accountDialog[open]', { state: 'detached', timeout: 5000 });
  const payload = mock.patches.find((p) => p.name === 'workbuddy-p3.json');
  assert(payload && Object.prototype.hasOwnProperty.call(payload, 'priority'), '禁用账号仍应能保存优先级');
  assert(!Object.prototype.hasOwnProperty.call(payload, 'note'), '禁用账号不得发送备注写入: ' + JSON.stringify(payload));
});

test('account editor: 受限备注标记即使 disabled=false 也不得覆盖', async page => {
  mock.accounts.push({
    auth_index: 'r1', nickname: 'restricted', note: 'WorkBuddy 11140：上游明确限制账号异常行为；申诉确认后手动启用',
    uid_tail: '7777', realm: 'cn', expires_at: '', last_checkin: '', priority: 0, file: 'workbuddy-r1.json',
  });
  await page.locator('#tasknav [data-view="accounts"]').click();
  await page.locator('#accountsRefresh').click();
  await must(page, '[data-edit="r1"]');
  await page.locator('[data-edit="r1"]').click();
  await must(page, '#accountDialog[open]');
  assert((await textOf(page, '#editIdentity')).includes('受限'), '标记备注应显示受限状态');
  assert(await page.locator('#editNote').getAttribute('readonly') !== null, '受限备注应只读');
  await page.locator('#accountSave').click();
  assert(mock.patches.length === 0, '无改动时不得写入');

  await page.locator('[data-edit="r1"]').click();
  await page.locator('#editPriority').fill('7');
  const patch = waitReply((data) => data.type === 'patch', '受限账号优先级');
  await page.locator('#accountSave').click();
  await patch;
  assert(mock.patches.length === 1 && mock.patches[0].priority === 7, '受限账号应只保存优先级');
  assert(!Object.prototype.hasOwnProperty.call(mock.patches[0], 'note'), '受限账号不得发送 note');
});

test('delete: 必须显式确认，取消无副作用', async (page) => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.locator('#accountDelete').click();
  assert(mock.deletes.length === 0, '取消确认不得删除');
  page.once('dialog', (dialog) => dialog.accept());
  await page.locator('#accountDelete').click();
  await page.waitForSelector('#accountDialog[open]', { state: 'detached', timeout: 5000 });
  assert(mock.deletes.length === 1 && mock.deletes[0] === 'workbuddy-p1.json', '确认后应 DELETE 宿主文件名');
  await page.waitForFunction(() => document.querySelectorAll('[data-account="p1"]').length === 0);
});

test('account editor: 旧保存完成不得关闭后开的编辑面板', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  await page.locator('#editNote').fill('旧账号备注');
  mock.patchDelay = 500;
  const patch = waitReply((data) => data.type === 'patch', '旧账号保存');
  await page.locator('#accountSave').click();
  await page.locator('#accountClose').click();
  await page.waitForFunction(() => document.querySelector('#accountDialog').open === false);
  await page.locator('[data-edit="p2"]').click();
  await must(page, '#accountDialog[open]');
  await patch;
  await page.waitForFunction(() => (document.querySelector('#toastRoot')?.textContent || '').includes('账号已更新'));
  assert(await page.evaluate(() => document.querySelector('#accountDialog').open) === true, '旧保存完成关闭了新编辑面板');
  const title = await textOf(page, '#accountDialogTitle');
  assert(title.includes('5520') || title.includes('moqutu'), '编辑面板应仍指向新账号: ' + title);
  assert(mock.accounts[0].note === '旧账号备注', '旧账号备注应已保存');
});

test('account editor: 同账号旧保存完成不得关闭重开的编辑面板', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  await page.locator('#editNote').fill('旧账号备注');

  let releaseHeld;
  let heldReadyResolve;
  const heldReady = new Promise((resolve) => { heldReadyResolve = resolve; });
  let heldRoute = null;
  releaseHeld = () => heldRoute.continue();
  await page.route('**' + AUTH + '/auth-files/fields', async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    heldRoute = route;
    heldReadyResolve();
  });
  const saved = page.waitForResponse((response) => response.url().endsWith('/auth-files/fields') && response.status() === 200, { timeout: 6000 });
  await page.locator('#accountSave').click();
  await heldReady;
  await page.locator('#accountClose').click();
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  // 重开后的表单值与提交值一致，只有「是否同一编辑生命周期」能判定这次完成属于谁。
  await page.locator('#editNote').fill('旧账号备注');
  releaseHeld();
  await saved;
  await page.waitForFunction(() => (document.querySelector('#toastRoot')?.textContent || '').includes('账号已更新'));
  assert(await page.evaluate(() => document.querySelector('#accountDialog').open) === true, '同账号旧保存完成关闭了重开的编辑面板');
  assert((await textOf(page, '#accountDialogTitle')).includes('8041'), '重开的同账号编辑面板应仍指向 p1');
  await page.unroute('**' + AUTH + '/auth-files/fields');
});

test('account editor: 保存期间的新草稿不得被旧提交结果丢弃', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  await page.locator('#editNote').fill('提交值');

  let releaseHeld;
  let heldReadyResolve;
  const heldReady = new Promise((resolve) => { heldReadyResolve = resolve; });
  let heldRoute = null;
  releaseHeld = () => heldRoute.continue();
  await page.route('**' + AUTH + '/auth-files/fields', async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    heldRoute = route;
    heldReadyResolve();
  });
  const saved = page.waitForResponse((response) => response.url().endsWith('/auth-files/fields') && response.status() === 200, { timeout: 6000 });
  await page.locator('#accountSave').click();
  await heldReady;
  await page.locator('#editNote').fill('保存期间新草稿');
  releaseHeld();
  await saved;
  await page.waitForFunction(() => (document.querySelector('#toastRoot')?.textContent || '').includes('账号已更新'));
  assert(await page.evaluate(() => document.querySelector('#accountDialog').open) === true, '保存完成后仍有新草稿时编辑面板应保持打开');
  assert((await valueOf(page, '#editNote')) === '保存期间新草稿', '保存完成不得覆盖保存期间输入的新草稿');
  assert(mock.accounts[0].note === '提交值', '服务端应只保存点击提交时的备注');
  await page.unroute('**' + AUTH + '/auth-files/fields');
});

test('account editor: 同账号旧删除完成不得关闭重开的编辑面板', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');

  let releaseHeld;
  let heldReadyResolve;
  const heldReady = new Promise((resolve) => { heldReadyResolve = resolve; });
  let heldRoute = null;
  releaseHeld = () => heldRoute.continue();
  await page.route('**' + AUTH + '/auth-files?name=*', async (route) => {
    if (route.request().method() !== 'DELETE') return route.continue();
    heldRoute = route;
    heldReadyResolve();
  });
  page.once('dialog', (dialog) => dialog.accept());
  const deleted = page.waitForResponse((response) => response.url().includes('/auth-files?name=') && response.status() === 200, { timeout: 6000 });
  await page.locator('#accountDelete').click();
  await heldReady;
  await page.locator('#accountClose').click();
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  releaseHeld();
  await deleted;
  await page.waitForFunction(() => (document.querySelector('#toastRoot')?.textContent || '').includes('账号已删除'));
  assert(await page.evaluate(() => document.querySelector('#accountDialog').open) === true, '同账号旧删除完成关闭了重开的编辑面板');
  assert((await textOf(page, '#accountDialogTitle')).includes('8041'), '重开的同账号编辑面板应仍指向 p1');
  await page.unroute('**' + AUTH + '/auth-files?name=*');
});

test('account editor: 旧删除完成不得关闭后开的编辑面板', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  mock.deleteDelay = 500;
  const deleted = waitReply((data) => data.type === 'delete', '旧账号删除');
  page.once('dialog', (dialog) => dialog.accept());
  await page.locator('#accountDelete').click();
  await page.locator('#accountClose').click();
  await page.waitForFunction(() => document.querySelector('#accountDialog').open === false);
  await page.locator('[data-edit="p2"]').click();
  await must(page, '#accountDialog[open]');
  await deleted;
  await page.waitForFunction(() => (document.querySelector('#toastRoot')?.textContent || '').includes('账号已删除'));
  assert(await page.evaluate(() => document.querySelector('#accountDialog').open) === true, '旧删除完成关闭了新编辑面板');
  const title = await textOf(page, '#accountDialogTitle');
  assert(title.includes('5520') || title.includes('moqutu'), '编辑面板应仍指向新账号: ' + title);
});

test('priority presets: 批量写入需确认且语义保持', async (page) => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#presetMenu');
  await page.locator('#presetMenu > summary').click();
  page.once('dialog', (dialog) => dialog.dismiss());
  await page.locator('[data-preset="global"]').click();
  assert(mock.patches.length === 0, '取消确认不得批量写入');
  const beforeAccounts = mock.completed.accounts;
  const writes = [];
  for (let i = 0; i < 3; i++) writes.push(waitReply((data) => data.type === 'patch', '预设批量写入 ' + (i + 1)));
  const reload = waitReply((data) => data.type === 'accounts' && data.count >= beforeAccounts + 2, '预设后刷新账号');
  mock.patchDelay = 60;
  await page.locator('#presetMenu > summary').click();
  page.once('dialog', (dialog) => dialog.accept());
  await page.locator('[data-preset="global"]').click();
  await writes[0];
  const busy = await page.evaluate(() => ['global', 'cn', 'clear'].every((mode) => document.querySelector('[data-preset="' + mode + '"]').disabled));
  assert(busy, '批量写入期间三个预设入口应禁用');
  await Promise.all(writes);
  await reload;
  const byName = Object.fromEntries(mock.patches.map((p) => [p.name, p.priority]));
  assert(byName['workbuddy-p1.json'] === 0, '国内账号应写 0');
  assert(byName['workbuddy-p2.json'] === 10 && byName['workbuddy-p3.json'] === 10, '国际账号应写 10');
  const enabled = await page.evaluate(() => ['global', 'cn', 'clear'].every((mode) => !document.querySelector('[data-preset="' + mode + '"]').disabled));
  assert(enabled, '批量完成后预设入口应恢复');
});

test('selectors: 刷新后保留选择；迟到的账号/配额响应不得覆盖', async (page) => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#accountsList .account-row');
  await page.locator('#tasknav [data-view="quota"]').click();
  for (const id of ['#quotaSelect', '#checkinSelect', '#benefitSelect']) {
    await page.locator(id).selectOption('p2');
  }
  await page.locator('#tasknav [data-view="accounts"]').click();
  const reply = waitReply((data) => data.type === 'accounts', '账号刷新');
  await page.locator('#accountsRefresh').click();
  await reply;
  for (const id of ['#quotaSelect', '#checkinSelect', '#benefitSelect']) {
    assert(await valueOf(page, id) === 'p2', id + ' 选择在刷新后丢失');
  }

  await page.locator('#tasknav [data-view="quota"]').click();
  mock.delays.quotaByIdx.p1 = 350;
  const slow = waitReply((data) => data.type === 'quota' && data.idx === 'p1', 'p1 迟到配额');
  const fast = waitReply((data) => data.type === 'quota' && data.idx === 'p2', 'p2 配额');
  await page.locator('#quotaSelect').selectOption('p1');
  await page.locator('#quotaRefresh').click();
  await page.locator('#quotaSelect').selectOption('p2');
  await page.locator('#quotaRefresh').click();
  await fast;
  await page.waitForFunction(() => document.querySelector('#quotaBody').textContent.includes('222'));
  await slow;
  const bodyText = await textOf(page, '#quotaBody');
  assert(bodyText.includes('222') && !bodyText.includes('111'), '迟到响应覆盖了当前账号: ' + bodyText);
  assert(await valueOf(page, '#quotaSelect') === 'p2', '迟到响应改变了选择器');
});

test('quota: 单账号重复点击只发一次查询', async page => {
  await page.locator('#tasknav [data-view="quota"]').click();
  await page.locator('#quotaSelect').selectOption('p1');
  mock.delays.quotaByIdx.p1 = 400;
  const reply = waitReply((data) => data.type === 'quota' && data.idx === 'p1', 'p1 配额');
  await page.locator('#quotaRefresh').click();
  await page.locator('#quotaRefresh').click();
  await reply;
  await page.waitForFunction(() => (document.querySelector('#quotaBody')?.textContent || '').includes('111'));
  const calls = mock.calls.filter((call) => call.method === 'POST' && call.path === API + '/quota');
  assert(calls.length === 1, '重复点击应只发一次配额查询，实际 ' + calls.length);
  const again = waitReply((data) => data.type === 'quota' && data.idx === 'p1', 'p1 再次查询');
  await page.locator('#quotaRefresh').click();
  await again;
  assert(mock.calls.filter((call) => call.method === 'POST' && call.path === API + '/quota').length === 2, '完成后应允许再次查询');
});

test('quota/checkin/benefits: 全部账号查询与显式写操作', async page => {
  await page.locator('#tasknav [data-view="quota"]').click();
  await page.locator('#quotaSelect').selectOption('');
  await page.locator('#quotaRefresh').click();
  await page.waitForFunction(() => document.querySelectorAll('#quotaBody tbody tr').length === 3);
  const quotaText = await textOf(page, '#quotaBody');
  assert(quotaText.includes('111') && quotaText.includes('222') && quotaText.includes('333'), '全部账号应逐号汇总: ' + quotaText);

  await page.locator('#checkinSelect').selectOption('p1');
  await page.locator('#checkinRun').click();
  await page.waitForFunction(() => (document.querySelector('#checkinBody')?.textContent || '').includes('签到成功'));
  assert(mock.lastCheckinBody && mock.lastCheckinBody.auth_index === 'p1', '签到应按当前选择发送 auth_index');

  await page.locator('#benefitSelect').selectOption('p1');
  await page.locator('#benefitQuery').click();
  await page.waitForFunction(() => (document.querySelector('#benefitBody')?.textContent || '').includes('可领取'));
  await page.locator('#benefitClaim').click();
  await page.waitForFunction(() => (document.querySelector('#benefitBody')?.textContent || '').includes('赠送'));
  assert(mock.claims.length === 1 && mock.claims[0].auth_index === 'p1', '领取应按当前选择发送 auth_index');
  assert((await textOf(page, '#benefitBody')).includes('OK'), '领取结果应保留在面板');
});

test('overview: CN 签到口径、连登与关注列表', async page => {
  await page.waitForFunction(() => document.querySelector('#statAccounts')?.textContent === '3');
  assert(await textOf(page, '#statSigned') === '1 / 1', '今日已签只统计国内版');
  assert(await textOf(page, '#statStreak') === '12 天', '最长连登应取可读账号最大值');
  const attention = await textOf(page, '#attentionBody');
  assert(attention.includes('受限账号') && attention.includes('moqutu'), '关注列表应覆盖禁用与连续失败账号: ' + attention);
  assert((await textOf(page, '#overviewLedger')).includes('730'), '最近流水应展示余额变化');
});

test('overview: 流水/签到/连登失败显示未知而非零', async page => {
  await page.waitForFunction(() => document.querySelector('#statAccounts')?.textContent === '3');
  mock.failLedger = true;
  mock.failCheckin = true;
  mock.failStreak = true;
  await page.locator('#overviewRefresh').click();
  await page.waitForFunction(() => (document.querySelector('#statsBody')?.textContent || '').includes('读取失败'));
  const summary = await textOf(page, '#statsBody');
  assert(summary.includes('读取失败'), '失败的聚合项应标记读取失败: ' + summary);
  assert(!summary.includes('0 条'), '失败的流水/签到不得显示为 0 条: ' + summary);
  assert(await textOf(page, '#statStreak') === '—', '连登读取失败不得显示 0 天');
  assert((await textOf(page, '#statSync')).includes('失败'), '部分失败时同步状态应说明: ' + await textOf(page, '#statSync'));
  assert((await textOf(page, '#overviewLedger')).includes('加载失败'), '流水面板应显示失败而不是空态');
});

test('key change: 关闭对话框并丢弃旧密钥的迟到响应', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  mock.delays.accountsByKey['old-key'] = 350;
  const oldReply = waitReply((data) => data.type === 'accounts' && data.key === 'old-key', '旧密钥迟到响应');
  await page.locator('#accountsRefresh').click();
  await page.locator('[data-edit="p1"]').click();
  await must(page, '#accountDialog[open]');
  mock.key = 'new-key';
  mock.accounts = [{ auth_index: 'n1', nickname: 'new-account', note: '新密钥账号', uid_tail: '9001', realm: 'cn', expires_at: '2026-12-31 00:00:00', last_checkin: today + ' 09:00:00', priority: 0, file: 'workbuddy-n1.json' }];
  const newReply = waitReply((data) => data.type === 'accounts' && data.key === 'new-key', '新密钥响应');
  await page.evaluate(() => {
    document.querySelector('#tasknav [data-view="settings"]').click();
    document.querySelector('#keyInput').value = 'new-key';
    document.querySelector('#keySave').click();
  });
  await newReply;
  await oldReply;
  await page.locator('#tasknav [data-view="accounts"]').click();
  await page.waitForFunction(() => document.querySelectorAll('#accountsList .account-row').length === 1);
  const list = await textOf(page, '#accountsList');
  assert(list.includes('新密钥账号') && !list.includes('主力手机号'), '旧密钥迟到响应覆盖了新列表: ' + list);
  assert(await page.locator('#accountDialog[open]').count() === 0, '改密钥后编辑框应关闭');
}, { key: 'old-key', prepare: () => { mock.key = 'old-key'; } });

test('key: 旧密钥的迟到 401 不得把新密钥标记为无效', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '[data-edit="p1"]');
  // 扣住旧密钥请求，等新密钥成功渲染后再放行 401，避免响应顺序不可控。
  let releaseHeld;
  const heldReady = new Promise((resolve) => { releaseHeld = resolve; });
  let heldRoute = null;
  await page.route('**' + API + '/accounts', async (route) => {
    if (route.request().headers()['authorization'] !== 'Bearer old-key') return route.continue();
    heldRoute = route;
    releaseHeld();
  });
  const oldResponse = page.waitForResponse((response) => response.url().endsWith(API + '/accounts') && response.status() === 401, { timeout: 6000 });
  await page.locator('#accountsRefresh').click();
  await heldReady;
  mock.key = 'new-key';
  mock.accounts = [{ auth_index: 'n1', nickname: 'new-account', note: '新密钥账号', uid_tail: '9001', realm: 'cn', expires_at: '2026-12-31 00:00:00', last_checkin: today + ' 09:00:00', priority: 0, file: 'workbuddy-n1.json' }];
  const fresh = waitReply((data) => data.type === 'accounts' && data.key === 'new-key', '新密钥账号');
  await page.evaluate(() => {
    document.querySelector('#tasknav [data-view="settings"]').click();
    document.querySelector('#keyInput').value = 'new-key';
    document.querySelector('#keySave').click();
  });
  await fresh;
  await page.waitForFunction(() => document.querySelector('#keyState')?.dataset.state === 'ready');
  await heldRoute.fulfill({ status: 401, contentType: 'application/json', body: JSON.stringify({ error: 'unauthorized' }) });
  await oldResponse;
  const flipped = await page.waitForFunction(
    () => document.querySelector('#keyState')?.dataset.state === 'error',
    null,
    { timeout: 800 }
  ).then(() => true).catch(() => false);
  assert(!flipped, '旧密钥 401 覆盖了新密钥状态');
  assert((await textOf(page, '#keyState')) === '已连接', '新密钥应保持已连接: ' + await textOf(page, '#keyState'));
}, { key: 'old-key', prepare: () => { mock.key = 'old-key'; } });

test('oauth: 取消后的旧轮询不得完成新登录', async page => {
  await openLoginPanel(page);
  mock.auth.pollDelay = 0;
  const firstPoll = waitOAuthPoll(1);
  await page.locator('#loginStart').click();
  await page.clock.runFor(2600);
  await firstPoll;
  mock.auth.pollDelay = 900;
  mock.auth.status = 'ok';
  const secondPoll = waitOAuthPoll(2);
  await page.clock.runFor(2600);
  await secondPoll;
  mock.auth.status = 'pending';
  mock.auth.pollDelay = 0;
  const staleReply = waitOAuthReply(2);
  await page.locator('#loginCancel').click();
  await openLoginPanel(page);
  await page.locator('#loginStart').click();
  await must(page, '#loginProgress:not([hidden])');
  await staleReply;
  assert(await page.locator('#loginDialog[open]').count() === 1, '旧轮询响应关闭了新登录面板');
  assert((await textOf(page, '#loginMessage')).includes('等待'), '新登录不得被旧轮询判定成功: ' + await textOf(page, '#loginMessage'));
}, { clock: true });

test('oauth: 取消可停轮询，等待上限 5 分钟', async page => {
  await openLoginPanel(page);
  await page.locator('#loginStart').click();
  await must(page, '#loginProgress:not([hidden])');
  await page.clock.runFor(2600);
  const first = await waitOAuthPoll(1);
  await page.locator('#loginCancel').click();
  await page.waitForFunction(() => document.querySelector('#loginDialog').open === false);
  const stoppedAt = mock.completed.oauthPolls;
  assert(stoppedAt === first.count, '取消后不应继续轮询: ' + stoppedAt + ' vs ' + first.count);
  await page.clock.runFor(10000);
  assert(mock.completed.oauthPolls === stoppedAt, '取消后 10 秒内仍产生轮询');

  await openLoginPanel(page);
  const nextPoll = waitOAuthPoll(stoppedAt + 1);
  await page.locator('#loginStart').click();
  await page.clock.runFor(2600);
  await nextPoll;
  await page.clock.runFor(301000);
  await page.waitForFunction(() => (document.querySelector('#loginMessage')?.textContent || '').includes('超时'));
  const timedOutAt = mock.completed.oauthPolls;
  await page.clock.runFor(10000);
  assert(mock.completed.oauthPolls === timedOutAt, '超时后仍继续轮询');
  assert(await page.locator('#loginStart').isEnabled(), '超时后应允许重新开始');
}, { clock: true });

test('oauth: 成功刷新账号并关闭面板', async page => {
  await openLoginPanel(page);
  await page.locator('#loginRealm').selectOption('global');
  const firstPoll = waitOAuthPoll(1);
  await page.locator('#loginStart').click();
  await must(page, '#loginProgress:not([hidden])');
  assert((await textOf(page, '#loginMessage')).includes('等待'), '应显示等待授权状态');
  const opened = await page.evaluate(() => window.__opened);
  assert(opened.length === 1 && opened[0] === mock.auth.url, '应以授权 URL 打开新窗口: ' + JSON.stringify(opened));
  assert(await page.locator('#loginManual').getAttribute('href') === mock.auth.url, '手动链接应指向授权地址');
  await firstPoll;
  const reload = waitReply((data) => data.type === 'accounts', '登录成功刷新账号');
  const reloadModels = waitReply((data) => data.type === 'models', '登录成功刷新模型');
  mock.auth.status = 'ok';
  await page.waitForSelector('#loginDialog[open]', { state: 'detached', timeout: 8000 });
  await reload;
  await reloadModels;
});

test('oauth: 启动中关闭面板会放弃迟到结果', async page => {
  await openLoginPanel(page);
  mock.auth.urlDelay = 250;
  const reply = waitReply((data) => data.type === 'auth-url', '授权链接响应');
  await page.locator('#loginStart').click();
  await page.locator('#loginClose').click();
  await reply;
  assert(await page.locator('#loginDialog[open]').count() === 0, '关闭后登录面板应保持关闭');
  assert((await page.evaluate(() => window.__opened)).length === 0, '取消后不得再打开授权窗口');
  assert(await page.locator('#loginProgress').isHidden(), '取消后不得显示等待进度');
});

test('oauth: 非法授权 URL 必须拒绝', async page => {
  mock.auth.url = 'javascript:alert(1)';
  await openLoginPanel(page);
  await page.locator('#loginStart').click();
  await must(page, '#loginError:not([hidden])');
  assert((await textOf(page, '#loginError')).includes('授权'), '应提示授权链接无效');
  assert((await page.evaluate(() => window.__opened)).length === 0, '非法 URL 不得打开新窗口');
  const manual = page.locator('#loginManual');
  const hidden = await manual.getAttribute('hidden');
  assert(hidden !== null || !(await manual.getAttribute('href')), '非法 URL 不得写入手动链接');
});

test('models: 同内部 ID 合并、同展示名不同 ID 分行、过滤与空态', async (page) => {
  await page.locator('#tasknav [data-view="models"]').click();
  await must(page, '#modelsBody tr[data-model]');
  assert(await page.locator('#modelsBody tr[data-model]').count() === 3, '期望 3 个合并后的模型行');
  const merged = await textOf(page, '[data-model="deepseek-v4.1-flash"]');
  assert(merged.includes('workbuddy/deepseek-v4.1-flash'), '调用 ID 必须由内部 ID 生成');
  assert(merged.includes('x1.00') && merged.includes('x0.50'), '全球/国内倍率应并列');
  assert(await page.locator('#modelsBody tr[data-model="o3"]').count() === 1 && await page.locator('#modelsBody tr[data-model="o4"]').count() === 1, '同展示名不同内部 ID 应分行');
  await page.locator('#modelFilter').fill('o3');
  assert(await page.locator('#modelsBody tr[data-model]').count() === 1, '过滤后应只剩 1 行');
  await page.locator('#modelFilter').fill('zzz-no-match');
  assert((await textOf(page, '#modelsBody')).includes('没有匹配'), '无匹配应显示空态');
});

test('feedback: 加载失败可重试，空态文案清晰', async page => {
  await page.locator('#tasknav [data-view="accounts"]').click();
  await must(page, '#accountsList .account-row');
  mock.failAccounts = true;
  await page.locator('#accountsRefresh').click();
  await page.waitForFunction(() => (document.querySelector('#accountsState')?.textContent || '').includes('加载失败'));
  assert(await page.locator('[data-retry="accounts"]').count() === 1, '账号加载失败应提供重试');
  mock.failAccounts = false;
  await page.locator('[data-retry="accounts"]').click();
  await page.waitForFunction(() => document.querySelectorAll('#accountsList .account-row').length === 3);

  mock.models = [];
  await page.locator('#tasknav [data-view="models"]').click();
  const modelsReply = waitReply((data) => data.type === 'models', '空模型目录');
  await page.locator('#modelsRefresh').click();
  await modelsReply;
  await page.waitForFunction(() => (document.querySelector('#modelsBody')?.textContent || '').includes('模型目录为空'));

  mock.tasks = [];
  await page.locator('#tasknav [data-view="records"]').click();
  await page.locator('#recordsRefresh').click();
  await page.waitForFunction(() => (document.querySelector('#tasksBody')?.textContent || '').includes('暂无任务记录'));
});

test('records: 流水全字段、最新在前、任务与成本可读', async (page) => {
  await page.locator('#tasknav [data-view="records"]').click();
  await must(page, '#ledgerBody tr[data-ledger]');
  assert(await page.locator('#tasksBody tr[data-task]').count() === 2, '任务时间线应有 2 行');
  assert(await page.locator('#checkinRecordBody tr[data-checkin]').count() === 2, '签到记录应有 2 行');
  assert(await page.locator('#ledgerBody tr[data-ledger]').count() === 3, '流水应有 3 行');
  const first = await textOf(page, '#ledgerBody tr[data-ledger]');
  assert(first.includes('2026-11-25') && first.includes('02:00:00'), '流水应最新在前: ' + first);
  assert(first.includes('222') && first.includes('-5') && first.includes('manual'), '流水应含余额/变化/来源');
  assert(await page.locator('#ledgerBody tr[data-ledger]').first().getAttribute('data-auth-id') === 'p2', '流水应保留完整 auth_id');
  assert((await textOf(page, '#ledgerBody')).includes('0.2500'), '模型成本应显示单价');
});

test('settings: 密钥协议不变、改密钥生效、主题持久化且不泄露密钥', async (page) => {
  await page.locator('#tasknav [data-view="settings"]').click();
  assert(await valueOf(page, '#keyInput') === 'test-key', '密钥输入应回显 localStorage 中的密钥');
  const reload = waitReply((data) => data.type === 'accounts', '保存密钥后加载账号');
  await page.locator('#keyInput').fill('next-key');
  mock.key = 'next-key';
  await page.locator('#keySave').click();
  await reload;
  assert(await page.evaluate(() => localStorage.getItem('wb_mgmt_key')) === 'next-key', '密钥仍应存于 wb_mgmt_key');
  assert(!(await page.evaluate(() => location.href)).includes('next-key'), 'URL 不得包含密钥');
  assert(!(await page.evaluate(() => document.body.innerText)).includes('test-key') && !(await page.evaluate(() => document.body.innerText)).includes('next-key'), '页面文本不得包含密钥');

  await page.locator('#themeSelect').selectOption('dark');
  assert(await page.evaluate(() => document.documentElement.dataset.theme) === 'dark', '选择深色应立即生效');
  assert(await page.evaluate(() => localStorage.getItem('workbuddy.ui.theme')) === 'dark', '主题应存入 workbuddy.ui.theme');
  assert(await valueOf(page, '#themeSelectSettings') === 'dark', '设置页主题应同步');
  await page.reload({ waitUntil: 'domcontentloaded' });
  assert(await page.evaluate(() => document.documentElement.dataset.theme) === 'dark', '刷新后应在首屏应用深色主题');
});

test('no auto actions: 切换视图不触发登录、签到、领取', async (page) => {
  await page.locator('#tasknav [data-view="quota"]').click();
  await page.waitForFunction(() => document.querySelector('[data-view-panel="quota"]')?.hidden === false);
  await page.locator('#tasknav [data-view="records"]').click();
  await page.waitForFunction(() => document.querySelector('[data-view-panel="records"]')?.hidden === false);
  const auto = mock.calls.filter((call) => /\/workbuddy-auth-url|\/checkin|\/benefits\/claim/.test(call.path));
  assert(auto.length === 0, '视图切换不应自动登录/签到/领取: ' + JSON.stringify(auto));
  assert(mock.claims.length === 0, '不应自动领取权益');

  await page.locator('#tasknav [data-view="quota"]').click();
  await page.locator('#benefitQuery').click();
  await page.waitForFunction(() => document.querySelector('#benefitBody').textContent.includes('可领取'));
  assert(mock.claims.length === 0, '查询权益不得自动领取');
});

/* ------------------------------------------------------------------ */

await fs.mkdir(evidenceDir, { recursive: true });
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const address = server.address();
base = 'http://127.0.0.1:' + address.port;

try {
  browser = await chromium.launch({
    headless: true,
    executablePath: process.env.CHROMIUM_PATH || undefined,
    args: ['--disable-dev-shm-usage']
  });
  for (const item of tests) await withTest(item.name, item.fn, item.options);
} finally {
  if (browser) await browser.close();
  await new Promise((resolve) => server.close(resolve));
}

const failed = failures.filter((item) => !item.ok);
const summary = failures.map((item) => (item.ok ? 'PASS ' : 'FAIL ') + item.name + (item.ok ? '' : '\n  ' + (item.error && item.error.message ? item.error.message : item.error)));
summary.push('', (failures.length - failed.length) + '/' + failures.length + ' passed');
if (failed.length) summary.push('FAILED: ' + failed.map((item) => item.name).join(' | '));
const output = summary.join('\n') + '\n';
console.log('\n' + output);
await fs.writeFile(path.join(evidenceDir, 'last-run.txt'), output, 'utf8');
if (failed.length) process.exitCode = 1;
