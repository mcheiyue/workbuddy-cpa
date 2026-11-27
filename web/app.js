(function () {
  'use strict';

  var API = '/v0/management/workbuddy';
  var AUTH_BASE = '/v0/management';
  var KEY_STORAGE = 'wb_mgmt_key';
  var THEME_STORAGE = 'workbuddy.ui.theme';
  var VIEWS = ['overview', 'accounts', 'quota', 'models', 'records', 'settings'];
  var LOGIN_POLL_MS = 2500;
  var LOGIN_MAX_MS = 300000;
  var RESTRICTED_NOTE_MARKER = 'WorkBuddy 11140';

  var SELECT_DEFS = [
    { id: 'quotaSelect', key: 'quota', body: 'quotaBody', hint: '选择账号后点击「刷新配额」' },
    { id: 'checkinSelect', key: 'checkin', body: 'checkinBody', hint: '选择账号后点击「签到」' },
    { id: 'benefitSelect', key: 'benefit', body: 'benefitBody', hint: '选择账号后点击「查询」' }
  ];

  var state = {
    key: readStoredKey(),
    keyValid: null,
    view: 'overview',
    accounts: [],
    models: [],
    accountFilter: '',
    epoch: 0,
    seq: { accounts: 0, models: 0, overview: 0, tasks: 0, lastCheckin: 0, ledger: 0, quota: 0, checkin: 0, benefit: 0 },
    selections: { quota: '', checkin: '', benefit: '' },
    editor: null,
    editorSeq: 0,
    editorReturn: null,
    presetBusy: false,
    quotaPending: null,
    login: { busy: false, attempt: 0, epoch: 0, timer: 0, ticker: 0, deadline: 0, state: '', returnFocus: null }
  };

  function $(id) { return document.getElementById(id); }

  function readStoredKey() {
    try { return localStorage.getItem(KEY_STORAGE) || ''; } catch (e) { return ''; }
  }

  function esc(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (character) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[character];
    });
  }

  function num(value) {
    var parsed = Number(value);
    return isFinite(parsed) ? parsed.toLocaleString('zh-CN') : '—';
  }

  function rawTime(value) {
    return String(value || '').replace('T', ' ').slice(0, 19);
  }

  function displayName(account) {
    return account.note || account.nickname || '未命名账号';
  }

  // 受限账号的备注由插件写入并在 auth 文件中置 disabled；列表接口未回传该位时按固定标记识别。
  function noteProtected(account) {
    return !!account.disabled || String(account.note || '').indexOf(RESTRICTED_NOTE_MARKER) > -1;
  }

  function initials(account) {
    var source = String(displayName(account)).replace(/[账号全球国内测试备用\s]/g, '');
    return (source.slice(0, 1) || 'W').toUpperCase();
  }

  function loginMethod(nickname) {
    var name = String(nickname || '').trim();
    if (!name) return '自定义(推断)';
    if (/^\d{11}$/.test(name)) return '手机(推断)';
    if (name.indexOf('@') > -1) return '邮箱(推断)';
    return '自定义(推断)';
  }

  function realmLabel(realm) {
    if (realm === 'cn') return '国内';
    if (realm === 'global') return '国际';
    return realm || '未知区域';
  }

  function panel(el, status, html) {
    if (!el) return;
    el.setAttribute('aria-busy', status === 'loading' ? 'true' : 'false');
    el.innerHTML = html;
  }

  function panelLoading(el, message) {
    panel(el, 'loading', '<div class="panel-state" data-state="loading"><span class="pulse" aria-hidden="true"></span><span>' + esc(message) + '</span></div>');
  }

  function panelIdle(el, message) {
    panel(el, 'idle', '<div class="panel-state" data-state="idle">' + esc(message) + '</div>');
  }

  function panelEmpty(el, title, detail) {
    panel(el, 'empty', '<div class="panel-state" data-state="empty"><strong>' + esc(title) + '</strong><span>' + esc(detail || '') + '</span></div>');
  }

  function panelError(el, message, retry) {
    var action = retry ? '<button class="button small" type="button" data-retry="' + esc(retry) + '">重试</button>' : '';
    panel(el, 'error', '<div class="panel-state" data-state="error" role="alert"><strong>加载失败</strong><span>' + esc(message) + '</span>' + action + '</div>');
  }

  function errorBody(status, text) {
    try {
      var parsed = JSON.parse(text);
      if (parsed) {
        if (parsed.error) return String(parsed.error);
        if (parsed.message) return String(parsed.message);
        if (parsed.msg) return String(parsed.msg);
      }
    } catch (e) {}
    var trimmed = String(text || '').trim();
    return trimmed ? trimmed.slice(0, 200) : ('HTTP ' + status);
  }

  async function request(base, path, init) {
    if (!state.key) return { ok: false, status: 0, error: '请先保存管理密钥' };
    var key = state.key;
    var options = init || {};
    var headers = { Authorization: 'Bearer ' + key };
    if (options.body) headers['Content-Type'] = 'application/json';
    try {
      var response = await fetch(base + path, { method: options.method || 'GET', headers: headers, body: options.body });
      var text = await response.text();
      if (response.status === 401) {
        if (key === state.key) { state.keyValid = false; updateKeyState(); }
        return { ok: false, status: 401, error: '管理密钥无效或已过期' };
      }
      if (!response.ok) return { ok: false, status: response.status, error: errorBody(response.status, text) };
      if (key === state.key) { state.keyValid = true; updateKeyState(); }
      var data = {};
      if (text) {
        try { data = JSON.parse(text); } catch (e) { data = {}; }
      }
      return { ok: true, status: response.status, data: data };
    } catch (e) {
      return { ok: false, status: 0, error: '网络错误: ' + e.message };
    }
  }

  function api(path, body) {
    if (body === undefined) return request(API, path, {});
    return request(API, path, { method: 'POST', body: JSON.stringify(body) });
  }

  function authApi(path, body) {
    if (body === undefined) return request(AUTH_BASE, path, {});
    return request(AUTH_BASE, path, { method: 'POST', body: JSON.stringify(body) });
  }

  function authPatch(path, body) {
    return request(AUTH_BASE, path, { method: 'PATCH', body: JSON.stringify(body) });
  }

  function authDelete(name) {
    return request(AUTH_BASE, '/auth-files?name=' + encodeURIComponent(name), { method: 'DELETE' });
  }

  function toast(message, type) {
    var root = $('toastRoot');
    if (!root) return;
    var item = document.createElement('div');
    item.className = 'toast ' + (type || 'info');
    var label = type === 'ok' ? '完成' : (type === 'err' ? '失败' : '提示');
    item.innerHTML = '<strong>' + label + '</strong><span>' + esc(message) + '</span>';
    root.appendChild(item);
    setTimeout(function () { item.remove(); }, 3200);
  }

  function updateKeyState() {
    var el = $('keyState');
    if (!el) return;
    if (!state.key) { el.textContent = '未连接'; el.dataset.state = 'missing'; return; }
    if (state.keyValid === false) { el.textContent = '密钥无效'; el.dataset.state = 'error'; return; }
    if (state.keyValid === true) { el.textContent = '已连接'; el.dataset.state = 'ready'; return; }
    el.textContent = '已保存密钥';
    el.dataset.state = 'saved';
  }

  function setView(view, options) {
    if (VIEWS.indexOf(view) < 0) view = 'overview';
    state.view = view;
    document.querySelectorAll('[data-view-panel]').forEach(function (section) {
      section.hidden = section.getAttribute('data-view-panel') !== view;
    });
    document.querySelectorAll('#tasknav [data-view]').forEach(function (button) {
      var active = button.getAttribute('data-view') === view;
      button.classList.toggle('is-active', active);
      button.setAttribute('aria-current', active ? 'page' : 'false');
    });
    var hash = '#' + view;
    if (location.hash !== hash) history.replaceState(null, '', hash);
    var skipLoad = options && options.noLoad;
    if (!skipLoad && state.key) {
      if (view === 'records') loadRecords();
      if (view === 'accounts' && !state.accounts.length) loadAccounts();
    }
  }

  /* ---------------- Accounts ---------------- */

  function accountMeta(account) {
    var parts = ['UID ' + (account.uid_tail ? '尾号 ' + account.uid_tail : '—'), loginMethod(account.nickname), realmLabel(account.realm)];
    if (account.expires_at) parts.push('到期 ' + account.expires_at);
    return parts.join(' · ');
  }

  function accountStatus(account) {
    if (account.disabled) {
      var reason = account.disabled_reason ? '<small class="badge-note">' + esc(account.disabled_reason) + '</small>' : '';
      return '<span class="badge danger">已禁用</span>' + reason;
    }
    if (noteProtected(account)) return '<span class="badge warn">受限</span>';
    if (account.dead_count) return '<span class="badge warn" title="连续 12153 失败计数">12153×' + esc(account.dead_count) + '</span>';
    return '<span class="badge ok">可用</span>';
  }

  function accountRow(account) {
    var last = account.realm === 'global' ? '—' : (account.last_checkin || '—');
    return '<article class="account-row" data-account="' + esc(account.auth_index) + '">' +
      '<div class="identity-cell">' +
        '<span class="avatar" aria-hidden="true">' + esc(initials(account)) + '</span>' +
        '<div class="identity-copy"><strong>' + esc(displayName(account)) + '</strong><small>' + esc(accountMeta(account)) + '</small></div>' +
      '</div>' +
      '<div class="cell" data-label="状态">' + accountStatus(account) + '</div>' +
      '<div class="cell" data-label="上次签到">' + esc(last) + '</div>' +
      '<div class="row-action"><button class="button small" type="button" data-edit="' + esc(account.auth_index) + '">编辑</button></div>' +
      '</article>';
  }

  function filteredAccounts() {
    var needle = state.accountFilter.trim().toLowerCase();
    if (!needle) return state.accounts;
    return state.accounts.filter(function (account) {
      return [account.note, account.nickname, account.uid_tail, account.auth_index, realmLabel(account.realm)].join(' ').toLowerCase().indexOf(needle) > -1;
    });
  }

  function renderAccountSummary() {
    var total = state.accounts.length;
    var available = state.accounts.filter(function (account) { return !noteProtected(account) && !account.dead_count; }).length;
    $('accountsTotal').textContent = String(total);
    $('accountsAvailable').textContent = String(available);
    $('accountsAttention').textContent = String(total - available);
    $('accountsSync').textContent = total ? '刚刚更新 · 备注优先显示' : '等待加载';
  }

  function renderAccounts() {
    var list = $('accountsList');
    var host = $('accountsState');
    var rows = filteredAccounts();
    var focused = document.activeElement;
    var focusIndex = list.contains(focused) ? focused.getAttribute('data-edit') : null;
    renderAccountSummary();
    if (!state.accounts.length) {
      panelEmpty(host, '还没有 WorkBuddy 账号', '使用「登录 / 添加账号」完成一次授权。');
      list.innerHTML = '';
    } else if (!rows.length) {
      panelEmpty(host, '没有匹配的账号', '换一个备注、昵称或 UID 再试。');
      list.innerHTML = '';
    } else {
      panel(host, 'ready', '');
      var head = '<div class="account-head" aria-hidden="true"><span>账号</span><span>状态</span><span>上次签到</span><span>操作</span></div>';
      list.innerHTML = head + rows.map(accountRow).join('');
    }
    if (focusIndex !== null) {
      var target = Array.from(list.querySelectorAll('[data-edit]')).find(function (button) {
        return button.getAttribute('data-edit') === focusIndex;
      }) || $('accountsRefresh');
      target.focus({ preventScroll: true });
    }
  }

  async function loadAccounts() {
    var seq = ++state.seq.accounts;
    var epoch = state.epoch;
    var host = $('accountsState');
    if (!state.accounts.length) panelLoading(host, '正在加载账号…');
    var response = await api('/accounts');
    if (seq !== state.seq.accounts || epoch !== state.epoch) return;
    if (!response.ok) {
      panelError(host, response.error, 'accounts');
      return;
    }
    state.accounts = (response.data && response.data.accounts) || [];
    renderAccounts();
    syncAccountSelects();
  }

  function syncAccountSelects() {
    SELECT_DEFS.forEach(function (definition) {
      var select = $(definition.id);
      if (!select) return;
      var previous = select.value || state.selections[definition.key] || '';
      var options = '<option value="">全部账号</option>';
      state.accounts.forEach(function (account) {
        options += '<option value="' + esc(account.auth_index) + '">' + esc((account.uid_tail || '?') + ' · ' + displayName(account)) + '</option>';
      });
      select.innerHTML = options;
      var exists = state.accounts.some(function (account) { return account.auth_index === previous; });
      var next = exists ? previous : '';
      select.value = next;
      state.selections[definition.key] = next;
      if (next !== previous) {
        state.seq[definition.key]++;
        panelIdle($(definition.body), definition.hint);
      }
    });
  }

  function openAccountEditor(authIndex) {
    var account = state.accounts.find(function (item) { return item.auth_index === authIndex; });
    if (!account) return;
    state.editor = { authIndex: authIndex, id: ++state.editorSeq };
    state.editorReturn = document.activeElement;
    $('accountDialogTitle').textContent = '编辑账号 · ' + (account.uid_tail || account.nickname || '');
    $('accountDialogSub').textContent = '备注与优先级保存到宿主 auth 文件字段。';
    $('editIdentity').innerHTML =
      '<span class="avatar" aria-hidden="true">' + esc(initials(account)) + '</span>' +
      '<div class="identity-copy"><strong>' + esc(displayName(account)) + '</strong><small>' + esc(accountMeta(account)) + '</small></div>' +
      '<div class="identity-status">' + accountStatus(account) + '</div>';
    var details = [
      ['UID 尾号', account.uid_tail || '—'],
      ['授权索引', account.auth_index || '—'],
      ['宿主文件', account.file || '—'],
      ['登录方式', loginMethod(account.nickname)],
      ['区域', realmLabel(account.realm)],
      ['过期时间', account.expires_at || '—'],
      ['上次签到', account.realm === 'global' ? '—' : (account.last_checkin || '—')]
    ];
    $('editDetails').innerHTML = details.map(function (entry) {
      return '<div><dt>' + esc(entry[0]) + '</dt><dd>' + esc(entry[1]) + '</dd></div>';
    }).join('');

    var note = $('editNote');
    var protectedNote = noteProtected(account);
    note.value = account.note || '';
    note.readOnly = protectedNote;
    note.classList.toggle('is-protected', protectedNote);
    $('editNoteHint').textContent = protectedNote
      ? '该账号已禁用/受限，备注由插件保护，不能在此修改。'
      : '只影响面板显示，不改变昵称或登录方式。';
    $('editPriority').value = String(account.priority || 0);
    // 每个编辑生命周期从可用按钮开始：上一次保存/删除的在途禁用不得遗留到本面板。
    $('accountSave').disabled = false;
    $('accountCancel').disabled = false;
    $('accountDelete').disabled = !account.file;
    $('accountDelete').title = account.file ? '' : '该账号没有宿主文件名，无法删除';
    setEditorError('');
    var dialog = $('accountDialog');
    if (!dialog.open) dialog.showModal();
    if (protectedNote) $('editPriority').focus(); else note.focus();
  }

  function closeAccountEditor() {
    var dialog = $('accountDialog');
    var target = state.editorReturn;
    var editor = state.editor;
    if (target && !document.contains(target) && editor) {
      target = Array.from(document.querySelectorAll('#accountsList [data-edit]')).find(function (button) {
        return button.getAttribute('data-edit') === editor.authIndex;
      }) || $('accountsRefresh');
    }
    if (dialog.open) dialog.close();
    state.editor = null;
    state.editorReturn = null;
    if (target && document.contains(target)) target.focus({ preventScroll: true });
  }

  function setEditorError(message) {
    var el = $('accountError');
    if (!message) { el.hidden = true; el.textContent = ''; return; }
    el.textContent = message;
    el.hidden = false;
  }

  function editorAccount() {
    return state.accounts.find(function (item) { return state.editor && item.auth_index === state.editor.authIndex; });
  }

  async function saveAccountEditor() {
    var account = editorAccount();
    if (!account) return;
    if (!account.file) { setEditorError('该账号没有宿主文件名，无法保存字段。'); return; }
    var priorityRaw = String($('editPriority').value || '').trim();
    if (!/^-?\d+$/.test(priorityRaw)) { setEditorError('优先级需为整数。'); return; }
    var priority = parseInt(priorityRaw, 10);
    var noteValue = String($('editNote').value || '').trim();
    var writes = [];
    if (priority !== Number(account.priority || 0)) writes.push({ field: '优先级', body: { name: account.file, priority: priority } });
    if (!noteProtected(account) && noteValue !== String(account.note || '').trim()) writes.push({ field: '备注', body: { name: account.file, note: noteValue } });
    if (!writes.length) { closeAccountEditor(); toast('没有需要保存的更改', 'info'); return; }

    var save = $('accountSave');
    var cancel = $('accountCancel');
    save.disabled = true;
    cancel.disabled = true;
    setEditorError('');
    var saved = [];
    var epoch = state.epoch;
    var editor = state.editor;
    var submittedNoteInput = String($('editNote').value || '');
    var submittedPriorityInput = String($('editPriority').value || '');
    try {
      for (var i = 0; i < writes.length; i++) {
        var write = writes[i];
        var response = await authPatch('/auth-files/fields', write.body);
        if (epoch !== state.epoch) return;
        if (!response.ok) {
          var prefix = saved.length ? saved.join('、') + '已保存；' : '';
          if (editor === state.editor) setEditorError(prefix + write.field + '保存失败：' + response.error);
          toast(prefix + write.field + '保存失败', 'err');
          loadAccounts();
          return;
        }
        saved.push(write.field);
      }
      var draftChanged = editor === state.editor && (String($('editNote').value || '') !== submittedNoteInput || String($('editPriority').value || '') !== submittedPriorityInput);
      if (editor === state.editor && !draftChanged) closeAccountEditor();
      toast('账号已更新', 'ok');
      loadAccounts();
    } finally {
      if (editor === state.editor && epoch === state.epoch) {
        save.disabled = false;
        cancel.disabled = false;
      }
    }
  }

  async function deleteEditorAccount() {
    var account = editorAccount();
    if (!account) return;
    if (!account.file) { setEditorError('该账号没有宿主文件名，无法删除。'); return; }
    if (!window.confirm('删除账号 ' + (account.uid_tail || account.file) + '？\n\n会移除宿主中的凭证文件，需重新登录才能恢复。')) return;
    var button = $('accountDelete');
    var epoch = state.epoch;
    var editor = state.editor;
    button.disabled = true;
    var response = await authDelete(account.file);
    if (editor === state.editor) button.disabled = false;
    if (epoch !== state.epoch) return;
    if (!response.ok) { if (editor === state.editor) setEditorError('删除失败：' + response.error); return; }
    if (editor === state.editor) closeAccountEditor();
    toast('账号已删除', 'ok');
    loadAccounts();
  }

  var PRESET_LABELS = { global: '国际优先', cn: '国内优先', clear: '全部清零' };

  async function applyPriorityPreset(mode) {
    if (state.presetBusy) { toast('优先级预设进行中，请稍候', 'info'); return; }
    if (!state.accounts.length) { toast('无账号可设置', 'err'); return; }
    if (!window.confirm('将按「' + (PRESET_LABELS[mode] || mode) + '」批量改写全部账号的优先级。\n\n该操作会直接写入宿主 auth 字段，确定继续？')) return;
    state.presetBusy = true;
    var buttons = document.querySelectorAll('[data-preset]');
    buttons.forEach(function (button) { button.disabled = true; });
    var epoch = state.epoch;
    try {
      var snapshot = await api('/accounts');
      if (epoch !== state.epoch) return;
      if (!snapshot.ok) { toast('读取账号失败: ' + snapshot.error, 'err'); return; }
      var accountList = ((snapshot.data && snapshot.data.accounts) || []).filter(function (account) { return account.file; });
      if (!accountList.length) { toast('没有可写入的账号文件', 'err'); return; }
      var ok = 0;
      for (var i = 0; i < accountList.length; i++) {
        if (epoch !== state.epoch) return;
        var account = accountList[i];
        var priority = 0;
        if (mode === 'global') priority = account.realm === 'global' ? 10 : 0;
        else if (mode === 'cn') priority = account.realm === 'cn' ? 10 : 0;
        toast('正在写入优先级 ' + (i + 1) + '/' + accountList.length + '…', 'info');
        var response = await authPatch('/auth-files/fields', { name: account.file, priority: priority });
        if (response.ok) ok++;
      }
      toast('已更新 ' + ok + '/' + accountList.length + ' 个账号', ok === accountList.length ? 'ok' : 'err');
    } finally {
      state.presetBusy = false;
      buttons.forEach(function (button) { button.disabled = false; });
      loadAccounts();
    }
  }

  /* ---------------- Models ---------------- */

  function creditsCell(credits) {
    if (!credits) return '<span class="muted">—</span>';
    var parsed = parseFloat(String(credits).replace(/^x/i, ''));
    var free = parsed === 0 ? '<span class="badge ok">免费</span> ' : '';
    return free + '<code>' + esc(credits) + '</code>';
  }

  function mergeModels(rows) {
    var byId = new Map();
    rows.forEach(function (row) {
      var id = row.internal_id || '';
      if (!byId.has(id)) byId.set(id, { internal_id: id, display_name: row.display_name || '', global: '', cn: '' });
      var entry = byId.get(id);
      if (!entry.display_name) entry.display_name = row.display_name || '';
      if (row.realm === 'cn') entry.cn = row.credits || ''; else entry.global = row.credits || '';
    });
    return Array.from(byId.values()).sort(function (a, b) { return a.internal_id.localeCompare(b.internal_id); });
  }

  function filteredModels() {
    var needle = String($('modelFilter').value || '').toLowerCase();
    var merged = mergeModels(state.models);
    if (!needle) return merged;
    return merged.filter(function (row) {
      return [row.internal_id, row.display_name, 'workbuddy/' + row.internal_id].join(' ').toLowerCase().indexOf(needle) > -1;
    });
  }

  function renderModels() {
    var host = $('modelsBody');
    var rows = filteredModels();
    $('modelsCount').textContent = rows.length ? ('共 ' + rows.length + ' 个调用 ID') : '没有匹配的模型';
    if (!state.models.length) { panelEmpty(host, '模型目录为空', '点击「刷新目录」重新读取。'); return; }
    if (!rows.length) { panelEmpty(host, '没有匹配的模型', '换一个调用 ID 或展示名再试。'); return; }
    var html = '<div class="table-wrap"><table class="data-table"><thead><tr><th>调用 ID</th><th>内部 ID</th><th>展示名</th><th>全球</th><th>国内</th></tr></thead><tbody>';
    rows.forEach(function (row) {
      html += '<tr data-model="' + esc(row.internal_id) + '">' +
        '<td data-label="调用 ID">' + (row.internal_id ? '<code>workbuddy/' + esc(row.internal_id) + '</code>' : '<span class="muted">—</span>') + '</td>' +
        '<td data-label="内部 ID"><code>' + esc(row.internal_id) + '</code></td>' +
        '<td data-label="展示名"><strong>' + esc(row.display_name) + '</strong></td>' +
        '<td data-label="全球">' + creditsCell(row.global) + '</td>' +
        '<td data-label="国内">' + creditsCell(row.cn) + '</td>' +
        '</tr>';
    });
    html += '</tbody></table></div>';
    panel(host, 'ready', html);
  }

  async function loadModels() {
    var seq = ++state.seq.models;
    var epoch = state.epoch;
    var host = $('modelsBody');
    if (!state.models.length) panelLoading(host, '正在加载模型…');
    var response = await api('/models');
    if (seq !== state.seq.models || epoch !== state.epoch) return;
    if (!response.ok) { panelError(host, response.error, 'models'); return; }
    state.models = (response.data && response.data.models) || [];
    renderModels();
  }

  /* ---------------- Quota ---------------- */

  function renderQuotaDetail(data) {
    var remain = Number(data.remain || 0);
    var used = Number(data.used || 0);
    var size = Number(data.size || 0);
    var packages = Number(data.packages || 0);
    var percent = size > 0 ? Math.max(0, Math.min(100, remain / size * 100)) : 0;
    var html = '<dl class="stat-list">' +
      '<div><dt>剩余额度</dt><dd><strong>' + num(remain) + '</strong> 积分</dd></div>' +
      '<div><dt>已用额度</dt><dd>' + num(used) + ' 积分</dd></div>' +
      '<div><dt>总量</dt><dd>' + num(size) + ' 积分</dd></div>' +
      '<div><dt>套餐数</dt><dd>' + num(packages) + '</dd></div>' +
      '</dl>';
    if (size > 0) {
      html += '<div class="meter-row"><div class="meter" role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' + percent.toFixed(1) + '" aria-label="剩余额度比例"><span style="width:' + percent.toFixed(1) + '%"></span></div><strong>' + percent.toFixed(1) + '%</strong></div>';
    }
    return html;
  }

  async function refreshQuota() {
    var select = $('quotaSelect');
    var index = select.value;
    state.selections.quota = index;
    var epoch = state.epoch;
    var host = $('quotaBody');
    var button = $('quotaRefresh');
    if (index) {
      var pending = state.quotaPending;
      if (pending && pending.index === index && pending.epoch === epoch) return;
      state.quotaPending = { index: index, epoch: epoch };
    }
    var seq = ++state.seq.quota;
    if (!index) {
      if (!state.accounts.length) { panelError(host, '尚无账号数据，请先刷新账号。', 'accounts'); return; }
      if (button.disabled) return;
      button.disabled = true;
      var list = state.accounts.slice();
      var rows = [];
      var ok = 0;
      try {
        for (var i = 0; i < list.length; i++) {
          if (seq !== state.seq.quota || epoch !== state.epoch) return;
          panelLoading(host, '正在查询配额 ' + (i + 1) + '/' + list.length + '…');
          var item = await api('/quota', { auth_index: list[i].auth_index });
          if (seq !== state.seq.quota || epoch !== state.epoch) return;
          if (item.ok && item.data && !item.data.error) {
            ok++;
            var quota = item.data;
            rows.push('<tr><td data-label="UID 尾号">' + esc(list[i].uid_tail || '—') + '</td><td data-label="区域">' + esc(realmLabel(list[i].realm)) + '</td><td data-label="剩余"><strong>' + num(quota.remain) + '</strong></td><td data-label="已用">' + num(quota.used) + '</td><td data-label="总量">' + num(quota.size) + '</td><td data-label="套餐">' + num(quota.packages) + '</td></tr>');
          } else {
            rows.push('<tr><td data-label="UID 尾号">' + esc(list[i].uid_tail || '—') + '</td><td data-label="区域">' + esc(realmLabel(list[i].realm)) + '</td><td data-label="结果" colspan="4" class="cell-error">' + esc((item.data && item.data.error) || item.error || '查询失败') + '</td></tr>');
          }
        }
        panel(host, 'ready', '<div class="table-wrap"><table class="data-table"><thead><tr><th>UID 尾号</th><th>区域</th><th>剩余</th><th>已用</th><th>总量</th><th>套餐</th></tr></thead><tbody>' + rows.join('') + '</tbody></table></div>');
        toast('配额查询完成 ' + ok + '/' + list.length, ok === list.length ? 'ok' : 'err');
      } finally {
        if (epoch === state.epoch) button.disabled = false;
      }
      return;
    }

    panelLoading(host, '正在查询配额…');
    try {
      var response = await api('/quota', { auth_index: index });
      if (seq !== state.seq.quota || epoch !== state.epoch || select.value !== index) return;
      if (!response.ok) { panelError(host, response.error, 'quota'); return; }
      if (response.data && response.data.error) { panelError(host, response.data.error, 'quota'); return; }
      panel(host, 'ready', renderQuotaDetail(response.data || {}));
      toast('配额已刷新', 'ok');
    } finally {
      var still = state.quotaPending;
      if (still && still.index === index && still.epoch === epoch) state.quotaPending = null;
    }
  }

  /* ---------------- Check-in ---------------- */

  async function runCheckin() {
    var index = $('checkinSelect').value;
    state.selections.checkin = index;
    var seq = ++state.seq.checkin;
    var epoch = state.epoch;
    var host = $('checkinBody');
    var button = $('checkinRun');
    if (button.disabled) return;
    button.disabled = true;
    panelLoading(host, '正在签到…');
    try {
      var response = await api('/checkin', index ? { auth_index: index } : {});
      if (seq !== state.seq.checkin || epoch !== state.epoch) return;
      if (!response.ok) { panelError(host, response.error, 'checkin'); return; }
      var results = (response.data && response.data.results) || [];
      if (!results.length) { panelEmpty(host, '没有可签到的账号', '国内版账号才支持签到。'); return; }
      var html = '<div class="table-wrap"><table class="data-table"><thead><tr><th>UID</th><th>状态</th><th>信息</th><th>余额</th></tr></thead><tbody>';
      results.forEach(function (row) {
        var tone = row.status === 'OK' || row.status === 'ALREADY' ? 'ok' : (row.status === 'AUTH_INVALID' ? 'danger' : 'warn');
        html += '<tr><td data-label="UID">' + esc(row.uid || '—') + '</td><td data-label="状态"><span class="badge ' + tone + '">' + esc(row.status) + '</span></td><td data-label="信息">' + esc(row.message || '—') + '</td><td data-label="余额">' + (row.remain != null ? num(row.remain) + ' 积分' : '—') + '</td></tr>';
      });
      html += '</tbody></table></div>';
      panel(host, 'ready', html);
      var ok = results.filter(function (row) { return row.status === 'OK' || row.status === 'ALREADY'; }).length;
      toast('签到完成 ' + ok + '/' + results.length, 'ok');
    } finally {
      if (epoch === state.epoch) button.disabled = false;
    }
  }

  /* ---------------- Benefits ---------------- */

  function checkinCell(checkin) {
    if (!checkin) return '<span class="muted">—</span>';
    if (!checkin.active) return '<span class="badge danger">未开启</span>';
    var label = checkin.today_checked_in ? '今日已签' : '今日未签';
    var tone = checkin.today_checked_in ? 'ok' : 'warn';
    var extra = checkin.total_credits ? '<small class="cell-note">累计 ' + num(checkin.total_credits) + ' 积分</small>' : '';
    return '<span class="badge ' + tone + '">' + label + '</span><span class="cell-note">连签 ' + num(checkin.streak_days || 0) + ' 天</span>' + extra;
  }

  function benefitCell(benefit) {
    if (!benefit) return '<span class="muted">—</span>';
    var label = benefit.claimed ? '已领取' : (benefit.active ? '可领取' : '未开启');
    var tone = benefit.claimed ? 'ok' : (benefit.active ? 'warn' : 'danger');
    var amount = benefit.credit_num ? ' ' + num(benefit.credit_num) + ' 积分' : '';
    var end = benefit.end_time ? '<small class="cell-note">截止 ' + esc(benefit.end_time) + '</small>' : '';
    return '<span class="badge ' + tone + '">' + label + '</span>' + esc(amount) + end;
  }

  function benefitRows(rows) {
    var html = '<div class="table-wrap"><table class="data-table"><thead><tr><th>UID 尾号</th><th>区域</th><th>签到</th><th>赠送积分</th><th>补偿积分</th><th>错误</th></tr></thead><tbody>';
    rows.forEach(function (row) {
      html += '<tr><td data-label="UID 尾号">' + esc(row.uid_tail || row.auth_index) + '</td><td data-label="区域">' + esc(realmLabel(row.realm)) + '</td><td data-label="签到">' + checkinCell(row.checkin) + '</td><td data-label="赠送积分">' + benefitCell(row.gift) + '</td><td data-label="补偿积分">' + benefitCell(row.compensation) + '</td><td data-label="错误" class="cell-error">' + esc(row.error || '—') + '</td></tr>';
    });
    return html + '</tbody></table></div>';
  }

  async function loadBenefits() {
    var index = $('benefitSelect').value;
    state.selections.benefit = index;
    var seq = ++state.seq.benefit;
    var epoch = state.epoch;
    var host = $('benefitBody');
    panelLoading(host, '正在查询权益…');
    var response = await api('/benefits', index ? { auth_index: index } : {});
    if (seq !== state.seq.benefit || epoch !== state.epoch) return;
    if (!response.ok) { panelError(host, response.error, 'benefit'); return; }
    var rows = (response.data && response.data.benefits) || [];
    if (!rows.length) { panelEmpty(host, '没有可查询的账号', '先添加账号再查询权益。'); return; }
    panel(host, 'ready', benefitRows(rows));
  }

  async function claimBenefits() {
    var index = $('benefitSelect').value;
    state.selections.benefit = index;
    var seq = ++state.seq.benefit;
    var epoch = state.epoch;
    var host = $('benefitBody');
    var button = $('benefitClaim');
    if (button.disabled) return;
    button.disabled = true;
    panelLoading(host, '正在领取可领权益…');
    try {
      var response = await api('/benefits/claim', index ? { auth_index: index } : {});
      if (seq !== state.seq.benefit || epoch !== state.epoch) return;
      if (!response.ok) { panelError(host, response.error, 'benefit'); return; }
      var results = (response.data && response.data.results) || [];
      if (!results.length) { panelEmpty(host, '没有可领取的权益', '只对窗口未过期且未领取的权益发起领取。'); return; }
      var html = '<div class="table-wrap"><table class="data-table"><thead><tr><th>UID 尾号</th><th>区域</th><th>权益</th><th>状态</th><th>信息</th></tr></thead><tbody>';
      results.forEach(function (row) {
        var tone = row.status === 'OK' ? 'ok' : (row.status === 'FAIL' ? 'danger' : 'warn');
        html += '<tr><td data-label="UID 尾号">' + esc(row.uid_tail || row.auth_index) + '</td><td data-label="区域">' + esc(realmLabel(row.realm)) + '</td><td data-label="权益">' + (row.kind === 'compensation' ? '补偿' : '赠送') + '</td><td data-label="状态"><span class="badge ' + tone + '">' + esc(row.status) + '</span></td><td data-label="信息">' + esc(row.message || '—') + '</td></tr>';
      });
      html += '</tbody></table></div>';
      panel(host, 'ready', html);
      toast('领取完成', 'ok');
    } finally {
      if (epoch === state.epoch) button.disabled = false;
    }
  }

  /* ---------------- Records ---------------- */

  async function loadTasks() {
    var seq = ++state.seq.tasks;
    var epoch = state.epoch;
    var host = $('tasksBody');
    if (!host.textContent || host.querySelector('[data-state="idle"]')) panelLoading(host, '正在加载任务…');
    var response = await api('/tasks');
    if (seq !== state.seq.tasks || epoch !== state.epoch) return;
    if (!response.ok) { panelError(host, response.error, 'tasks'); return; }
    var rows = (response.data && response.data.tasks) || [];
    if (!rows.length) { panelEmpty(host, '暂无任务记录', '进程内任务环为空。'); return; }
    var html = '<div class="table-wrap"><table class="data-table"><thead><tr><th>时间</th><th>任务</th><th>账号</th><th>状态</th><th>信息</th></tr></thead><tbody>';
    rows.slice().reverse().forEach(function (row) {
      var ok = !!row.ok;
      var label = ok ? (row.err === 'already' ? 'ALREADY' : 'OK') : 'FAIL';
      html += '<tr data-task><td data-label="时间">' + esc(rawTime(row.ts)) + '</td><td data-label="任务">' + esc(row.task) + '</td><td data-label="账号"><code>' + esc(row.auth_id) + '</code></td><td data-label="状态"><span class="badge ' + (ok ? 'ok' : 'danger') + '">' + label + '</span></td><td data-label="信息">' + esc(row.err || '—') + '</td></tr>';
    });
    html += '</tbody></table></div>';
    panel(host, 'ready', html);
  }

  async function loadLastCheckin() {
    var seq = ++state.seq.lastCheckin;
    var epoch = state.epoch;
    var host = $('checkinRecordBody');
    if (!host.textContent || host.querySelector('[data-state="idle"]')) panelLoading(host, '正在加载签到记录…');
    var response = await api('/last-checkin');
    if (seq !== state.seq.lastCheckin || epoch !== state.epoch) return;
    if (!response.ok) { panelError(host, response.error, 'last-checkin'); return; }
    var rows = (response.data && response.data.last_checkins) || [];
    if (!rows.length) { panelEmpty(host, '暂无签到记录', '本地观测为空。'); return; }
    var html = '<div class="table-wrap"><table class="data-table"><thead><tr><th>账号</th><th>时间</th><th>来源</th><th>状态</th></tr></thead><tbody>';
    rows.forEach(function (row) {
      var tone = row.status === 'OK' || row.status === 'TICKER' ? 'ok' : 'warn';
      html += '<tr data-checkin><td data-label="账号">' + esc(row.auth_id) + '</td><td data-label="时间">' + esc(row.ts) + '</td><td data-label="来源">' + esc(row.source) + '</td><td data-label="状态"><span class="badge ' + tone + '">' + esc(row.status) + '</span></td></tr>';
    });
    html += '</tbody></table></div>';
    panel(host, 'ready', html);
  }

  async function loadLedger() {
    var seq = ++state.seq.ledger;
    var epoch = state.epoch;
    var host = $('ledgerBody');
    if (!host.textContent || host.querySelector('[data-state="idle"]')) panelLoading(host, '正在加载流水…');
    var responses = await Promise.all([api('/credits-ledger'), api('/accounts')]);
    if (seq !== state.seq.ledger || epoch !== state.epoch) return;
    var ledger = responses[0];
    if (!ledger.ok) { panelError(host, ledger.error, 'ledger'); return; }
    var accountMap = {};
    ((responses[1] && responses[1].data && responses[1].data.accounts) || []).forEach(function (account) {
      accountMap[account.auth_index] = account.uid_tail || account.nickname || account.auth_index;
    });
    function shortName(authId) {
      return accountMap[authId] || (authId ? String(authId).slice(0, 8) : '—');
    }
    var entries = (ledger.data && ledger.data.entries) || [];
    var costs = (ledger.data && ledger.data.model_costs) || {};
    var html = '';
    var costRows = [];
    Object.keys(costs).forEach(function (authId) {
      Object.keys(costs[authId]).forEach(function (model) {
        costRows.push([authId, model, costs[authId][model]]);
      });
    });
    if (costRows.length) {
      html += '<div class="subsection"><h3>模型成本</h3><p class="subsection-note">usage.credit 消耗观测，/千 token，EMA</p><div class="table-wrap"><table class="data-table"><thead><tr><th>账号</th><th>模型</th><th>单价</th><th>样本</th><th>最近</th></tr></thead><tbody>';
      costRows.forEach(function (row) {
        html += '<tr><td data-label="账号">' + esc(shortName(row[0])) + '</td><td data-label="模型"><code>' + esc(row[1]) + '</code></td><td data-label="单价">' + Number(row[2].cost_per_1k || 0).toFixed(4) + '</td><td data-label="样本">' + num(row[2].samples || 0) + '</td><td data-label="最近">' + esc(rawTime(row[2].last_seen)) + '</td></tr>';
      });
      html += '</tbody></table></div></div>';
    }
    if (entries.length) {
      html += '<div class="table-wrap"><table class="data-table"><thead><tr><th>时间</th><th>账号</th><th>余额</th><th>变化</th><th>来源</th></tr></thead><tbody>';
      entries.slice().reverse().forEach(function (entry) {
        var delta = Number(entry.delta || 0);
        var tone = delta > 0 ? 'delta-up' : (delta < 0 ? 'delta-down' : 'delta-flat');
        html += '<tr data-ledger data-auth-id="' + esc(entry.auth_id) + '"><td data-label="时间">' + esc(rawTime(entry.ts)) + '</td><td data-label="账号" title="' + esc(entry.auth_id) + '">' + esc(shortName(entry.auth_id)) + '</td><td data-label="余额">' + num(entry.balance) + '</td><td data-label="变化" class="' + tone + '">' + (delta > 0 ? '+' : '') + delta + '</td><td data-label="来源">' + esc(entry.source) + '</td></tr>';
      });
      html += '</tbody></table></div>';
    }
    if (!html) { panelEmpty(host, '暂无流水记录', '本地观测尚未产生条目。'); return; }
    panel(host, 'ready', html);
  }

  function loadRecords() {
    loadTasks();
    loadLastCheckin();
    loadLedger();
  }

  /* ---------------- Overview ---------------- */

  function todayString() {
    var now = new Date();
    return now.getFullYear() + '-' + String(now.getMonth() + 1).padStart(2, '0') + '-' + String(now.getDate()).padStart(2, '0');
  }

  async function loadOverview() {
    var seq = ++state.seq.overview;
    var epoch = state.epoch;
    var host = $('statsBody');
    panelLoading(host, '正在加载统计…');
    var responses = await Promise.all([api('/accounts'), api('/credits-ledger'), api('/last-checkin'), api('/streak', {})]);
    if (seq !== state.seq.overview || epoch !== state.epoch) return;
    var accountsResponse = responses[0];
    if (!accountsResponse.ok) { panelError(host, accountsResponse.error, 'overview'); return; }
    var accounts = (accountsResponse.data && accountsResponse.data.accounts) || [];
    var ledgerResponse = responses[1];
    var checkinResponse = responses[2];
    var streakResponse = responses[3];
    var entries = (ledgerResponse.ok && ledgerResponse.data && ledgerResponse.data.entries) || [];
    var checkins = (checkinResponse.ok && checkinResponse.data && checkinResponse.data.last_checkins) || [];
    var streaks = (streakResponse.ok && streakResponse.data && streakResponse.data.streaks) || [];

    var cnAccounts = accounts.filter(function (account) { return account.realm === 'cn'; });
    var today = todayString();
    var signed = cnAccounts.filter(function (account) { return String(account.last_checkin || '').slice(0, 10) === today; }).length;
    var bySource = {};
    entries.forEach(function (entry) {
      var source = entry.source || '?';
      bySource[source] = (bySource[source] || 0) + 1;
    });
    var sourceText = Object.keys(bySource).sort().map(function (source) { return source + ' ' + bySource[source]; }).join(' / ');
    var readable = streaks.filter(function (row) { return !row.error; });
    var maxDays = readable.length ? Math.max.apply(null, readable.map(function (row) { return row.days || 0; })) : 0;
    var available = [];
    readable.forEach(function (row) {
      ['7d', '14d', '28d'].forEach(function (tier) {
        if (row['tier_' + tier] === 'available' && available.indexOf(tier) < 0) available.push(tier);
      });
    });

    var partial = !ledgerResponse.ok || !checkinResponse.ok || !streakResponse.ok;
    $('statAccounts').textContent = String(accounts.length);
    $('statSigned').textContent = signed + ' / ' + cnAccounts.length;
    $('statStreak').textContent = streakResponse.ok ? maxDays + ' 天' : '—';
    $('statSync').textContent = partial ? '部分数据读取失败' : '刚刚更新 · 本地观测';
    $('overviewLine').setAttribute('data-ready', partial ? 'partial' : 'true');

    var failedText = '<span class="muted">读取失败</span>';
    var ledgerText = ledgerResponse.ok ? '<strong>' + entries.length + '</strong> 条' + (sourceText ? '<span class="cell-note">' + esc(sourceText) + '</span>' : '') : failedText;
    var checkinText = checkinResponse.ok ? '<strong>' + checkins.length + '</strong> 条' : failedText;
    var streakText = streakResponse.ok ? '<strong>' + maxDays + '</strong> 天 <span class="cell-note">' + readable.length + '/' + streaks.length + ' 个账号可读</span>' : failedText;
    var availableText = !streakResponse.ok ? failedText : (available.length ? '<strong>' + esc(available.join(' / ')) + '</strong>' : '<span class="muted">暂无</span>');
    var summary = '<dl class="stat-list">' +
      '<div><dt>账号总数</dt><dd><strong>' + accounts.length + '</strong><span class="cell-note">CN ' + cnAccounts.length + ' / 国际 ' + (accounts.length - cnAccounts.length) + '</span></dd></div>' +
      '<div><dt>今日已签到</dt><dd><strong>' + signed + '</strong> / ' + cnAccounts.length + ' <span class="cell-note">仅国内版</span></dd></div>' +
      '<div><dt>Credits 流水</dt><dd>' + ledgerText + '</dd></div>' +
      '<div><dt>签到记录</dt><dd>' + checkinText + '</dd></div>' +
      '<div><dt>最长连登</dt><dd>' + streakText + '</dd></div>' +
      '<div><dt>可领档位</dt><dd>' + availableText + '</dd></div>' +
      '</dl>';
    panel(host, 'ready', summary);

    renderAttention(accounts);
    if (ledgerResponse.ok) renderOverviewLedger(entries);
    else panelError($('overviewLedger'), ledgerResponse.error, 'overview');
  }

  function renderAttention(accounts) {
    var host = $('attentionBody');
    var attention = accounts.filter(function (account) { return noteProtected(account) || account.dead_count; });
    if (!attention.length) { panelEmpty(host, '没有需要处理的账号', '当前没有被禁用、受限或连续失败的账号。'); return; }
    panel(host, 'ready', '<ul class="attention-list">' + attention.map(function (account) {
      var tag = account.disabled ? '<span class="badge danger">已禁用</span>' : (noteProtected(account) ? '<span class="badge warn">受限</span>' : '<span class="badge warn">12153×' + esc(account.dead_count) + '</span>');
      var reason = account.disabled_reason || (noteProtected(account) ? account.note : account.last_checkin) || '需要重新登录或检查网络';
      return '<li><div><strong>' + esc(displayName(account)) + '</strong><small>' + esc(reason) + '</small></div>' + tag + '</li>';
    }).join('') + '</ul>');
  }

  function renderOverviewLedger(entries) {
    var host = $('overviewLedger');
    if (!entries.length) { panelEmpty(host, '暂无 Credits 变动', '本地观测尚未产生条目。'); return; }
    var html = '<div class="table-wrap"><table class="data-table compact"><thead><tr><th>时间</th><th>账号</th><th>余额</th><th>变化</th></tr></thead><tbody>';
    entries.slice(-5).reverse().forEach(function (entry) {
      var delta = Number(entry.delta || 0);
      var tone = delta > 0 ? 'delta-up' : (delta < 0 ? 'delta-down' : 'delta-flat');
      html += '<tr><td data-label="时间">' + esc(rawTime(entry.ts)) + '</td><td data-label="账号" title="' + esc(entry.auth_id) + '">' + esc(entry.auth_id) + '</td><td data-label="余额">' + num(entry.balance) + '</td><td data-label="变化" class="' + tone + '">' + (delta > 0 ? '+' : '') + delta + '</td></tr>';
    });
    html += '</tbody></table></div>';
    panel(host, 'ready', html);
  }

  /* ---------------- Key & theme ---------------- */

  function resetPanels() {
    var hint = state.key ? '正在加载…' : '请先在「设置」保存管理密钥。';
    panelIdle($('statsBody'), hint);
    panelIdle($('attentionBody'), hint);
    panelIdle($('overviewLedger'), hint);
    panelIdle($('accountsState'), state.key ? '正在加载账号…' : hint);
    $('accountsList').innerHTML = '';
    panelIdle($('modelsBody'), state.key ? '正在加载模型…' : hint);
    panelIdle($('tasksBody'), state.key ? '正在加载任务…' : hint);
    panelIdle($('checkinRecordBody'), state.key ? '正在加载签到记录…' : hint);
    panelIdle($('ledgerBody'), state.key ? '正在加载流水…' : hint);
    $('accountsTotal').textContent = '—';
    $('accountsAvailable').textContent = '—';
    $('accountsAttention').textContent = '—';
    $('accountsSync').textContent = '等待加载';
    SELECT_DEFS.forEach(function (definition) {
      var select = $(definition.id);
      if (select) select.innerHTML = '<option value="">全部账号</option>';
      state.selections[definition.key] = '';
      panelIdle($(definition.body), definition.hint);
    });
    $('statAccounts').textContent = '—';
    $('statSigned').textContent = '—';
    $('statStreak').textContent = '—';
    $('statSync').textContent = '等待加载';
    $('modelsCount').textContent = '等待加载';
  }

  function invalidateAll() {
    Object.keys(state.seq).forEach(function (key) { state.seq[key]++; });
    state.epoch++;
    state.quotaPending = null;
    ['quotaRefresh', 'checkinRun', 'benefitClaim'].forEach(function (id) {
      var button = $(id);
      if (button) button.disabled = false;
    });
  }

  function resetData() {
    invalidateAll();
    state.accounts = [];
    state.models = [];
    state.keyValid = null;
    resetPanels();
  }

  function saveKey() {
    var next = String($('keyInput').value || '').trim();
    var changed = next !== state.key;
    if (changed) {
      state.key = next;
      try { localStorage.setItem(KEY_STORAGE, next); } catch (e) {}
      invalidateAll();
      cancelLoginFlow();
      closeAccountEditor();
      resetData();
    }
    updateKeyState();
    toast(next ? '密钥已保存' : '密钥已清除', 'info');
    if (next) bootstrap();
  }

  function clearKey() {
    $('keyInput').value = '';
    saveKey();
  }

  function themeMode() {
    var mode = document.documentElement.dataset.themeMode;
    return mode === 'light' || mode === 'dark' || mode === 'system' ? mode : 'system';
  }

  function applyTheme(mode, persist) {
    if (mode !== 'light' && mode !== 'dark' && mode !== 'system') mode = 'system';
    var resolved = mode;
    if (mode === 'system') {
      try { resolved = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'; } catch (e) { resolved = 'light'; }
    }
    document.documentElement.dataset.theme = resolved;
    document.documentElement.dataset.themeMode = mode;
    if ($('themeSelect')) $('themeSelect').value = mode;
    if ($('themeSelectSettings')) $('themeSelectSettings').value = mode;
    if (persist) {
      try { localStorage.setItem(THEME_STORAGE, mode); } catch (e) {}
    }
  }

  /* ---------------- Login ---------------- */

  function safeAuthURL(raw) {
    try {
      var url = new URL(String(raw));
      if (url.protocol !== 'http:' && url.protocol !== 'https:') return '';
      return url.href;
    } catch (e) {
      return '';
    }
  }

  function resetLoginPanel() {
    $('loginProgress').hidden = true;
    $('loginError').hidden = true;
    $('loginError').textContent = '';
    $('loginManual').hidden = true;
    $('loginManual').removeAttribute('href');
    $('loginMessage').textContent = '等待授权…';
    $('loginCountdown').textContent = '05:00';
    $('loginStart').disabled = false;
    $('loginCancel').hidden = true;
  }

  function openLoginPanel() {
    stopLogin();
    resetLoginPanel();
    state.login.returnFocus = document.activeElement;
    var dialog = $('loginDialog');
    if (!dialog.open) dialog.showModal();
    $('loginRealm').focus();
  }

  function stopLogin() {
    state.login.attempt++;
    if (state.login.timer) { clearTimeout(state.login.timer); state.login.timer = 0; }
    if (state.login.ticker) { clearInterval(state.login.ticker); state.login.ticker = 0; }
    state.login.busy = false;
  }

  function closeLoginPanel() {
    stopLogin();
    var dialog = $('loginDialog');
    if (dialog.open) dialog.close();
  }

  function cancelLoginFlow() {
    stopLogin();
    var dialog = $('loginDialog');
    if (dialog.open) dialog.close();
    if ($('loginStart')) $('loginStart').disabled = false;
    if ($('loginCancel')) $('loginCancel').hidden = true;
    if ($('loginProgress')) $('loginProgress').hidden = true;
  }

  function loginError(message) {
    stopLogin();
    $('loginStart').disabled = false;
    $('loginCancel').hidden = true;
    $('loginMessage').textContent = message;
    $('loginError').textContent = message;
    $('loginError').hidden = false;
  }

  function updateCountdown() {
    var remaining = Math.max(0, state.login.deadline - Date.now());
    var seconds = Math.ceil(remaining / 1000);
    var minutes = Math.floor(seconds / 60);
    var rest = seconds % 60;
    $('loginCountdown').textContent = String(minutes).padStart(2, '0') + ':' + String(rest).padStart(2, '0');
  }

  function scheduleLoginPoll() {
    state.login.timer = setTimeout(pollLogin, LOGIN_POLL_MS);
  }

  async function startLogin() {
    if (state.login.busy) return;
    var epoch = state.epoch;
    var attempt = state.login.attempt;
    state.login.busy = true;
    $('loginStart').disabled = true;
    $('loginCancel').hidden = true;
    $('loginError').hidden = true;
    $('loginProgress').hidden = true;
    var realm = $('loginRealm').value === 'global' ? 'global' : 'cn';
    var response = await authApi('/workbuddy-auth-url?realm=' + encodeURIComponent(realm));
    if (!state.login.busy || attempt !== state.login.attempt || epoch !== state.epoch) return;
    if (!response.ok) { loginError(response.error || '无法获取授权链接'); return; }
    var rawURL = response.data && response.data.url;
    var url = safeAuthURL(rawURL);
    if (!url) { loginError('授权链接无效（仅支持 http/https 地址）'); return; }

    state.login.epoch = epoch;
    state.login.state = String((response.data && response.data.state) || '');
    $('loginManual').href = url;
    $('loginManual').hidden = false;
    $('loginProgress').hidden = false;
    $('loginMessage').textContent = realm === 'global' ? '等待 WorkBuddy 国际版授权…' : '等待 WorkBuddy 国内版授权…';
    $('loginCancel').hidden = false;
    try { window.open(url, '_blank', 'noopener'); } catch (e) {}
    state.login.deadline = Date.now() + LOGIN_MAX_MS;
    updateCountdown();
    state.login.ticker = setInterval(updateCountdown, 1000);
    scheduleLoginPoll();
  }

  async function pollLogin() {
    var epoch = state.epoch;
    var attempt = state.login.attempt;
    if (!state.login.busy || state.login.epoch !== epoch) return;
    if (Date.now() >= state.login.deadline) { loginError('授权等待超时（5 分钟），请重新开始'); return; }
    var response = await authApi('/get-auth-status?state=' + encodeURIComponent(state.login.state));
    if (attempt !== state.login.attempt || !state.login.busy || state.login.epoch !== state.epoch) return;
    if (response.ok && response.data && response.data.status === 'ok') {
      stopLogin();
      closeLoginPanel();
      toast('登录成功', 'ok');
      loadAccounts();
      loadModels();
      loadOverview();
      return;
    }
    if (response.ok && response.data && response.data.status === 'error') {
      loginError('登录失败：' + ((response.data && response.data.error) || '未知错误'));
      return;
    }
    if (!response.ok && response.status === 401) { loginError('管理密钥无效或已过期'); return; }
    scheduleLoginPoll();
  }

  /* ---------------- Wiring ---------------- */

  var RETRY = {
    accounts: loadAccounts,
    models: loadModels,
    overview: loadOverview,
    tasks: loadTasks,
    'last-checkin': loadLastCheckin,
    ledger: loadLedger,
    quota: refreshQuota,
    checkin: runCheckin,
    benefit: loadBenefits
  };

  function bootstrap() {
    loadOverview();
    loadAccounts();
    loadModels();
    if (state.view === 'records') loadRecords();
  }

  function bind() {
    document.querySelectorAll('#tasknav [data-view]').forEach(function (button) {
      button.addEventListener('click', function () { setView(button.getAttribute('data-view')); });
    });
    window.addEventListener('hashchange', function () { setView(location.hash.slice(1)); });

    document.addEventListener('click', function (event) {
      var edit = event.target.closest('[data-edit]');
      if (edit) { openAccountEditor(edit.getAttribute('data-edit')); return; }
      var goto = event.target.closest('[data-goto-view]');
      if (goto) { setView(goto.getAttribute('data-goto-view')); return; }
      var preset = event.target.closest('[data-preset]');
      if (preset) {
        var menu = preset.closest('details');
        if (menu) menu.removeAttribute('open');
        applyPriorityPreset(preset.getAttribute('data-preset'));
        return;
      }
      var retry = event.target.closest('[data-retry]');
      if (retry) {
        var handler = RETRY[retry.getAttribute('data-retry')];
        if (handler) handler();
      }
    });

    $('overviewRefresh').addEventListener('click', loadOverview);
    $('accountsRefresh').addEventListener('click', loadAccounts);
    $('modelsRefresh').addEventListener('click', loadModels);
    $('recordsRefresh').addEventListener('click', loadRecords);
    $('quotaRefresh').addEventListener('click', refreshQuota);
    $('checkinRun').addEventListener('click', runCheckin);
    $('benefitQuery').addEventListener('click', loadBenefits);
    $('benefitClaim').addEventListener('click', claimBenefits);

    $('accountFilter').addEventListener('input', function () {
      state.accountFilter = this.value || '';
      renderAccounts();
    });
    $('modelFilter').addEventListener('input', renderModels);

    SELECT_DEFS.forEach(function (definition) {
      var select = $(definition.id);
      if (!select) return;
      select.addEventListener('change', function () {
        state.selections[definition.key] = select.value;
        state.seq[definition.key]++;
        panelIdle($(definition.body), definition.hint);
      });
    });

    $('loginOpen').addEventListener('click', openLoginPanel);
    $('loginStart').addEventListener('click', startLogin);
    $('loginCancel').addEventListener('click', cancelLoginFlow);
    $('loginClose').addEventListener('click', cancelLoginFlow);
    $('loginCloseFoot').addEventListener('click', cancelLoginFlow);

    $('accountClose').addEventListener('click', closeAccountEditor);
    $('accountCancel').addEventListener('click', closeAccountEditor);
    $('accountSave').addEventListener('click', saveAccountEditor);
    $('accountDelete').addEventListener('click', deleteEditorAccount);

    $('keySave').addEventListener('click', saveKey);
    $('keyClear').addEventListener('click', clearKey);

    $('themeSelect').addEventListener('change', function () { applyTheme(this.value, true); });
    $('themeSelectSettings').addEventListener('change', function () { applyTheme(this.value, true); });

    ['accountDialog', 'loginDialog'].forEach(function (id) {
      var dialog = $(id);
      dialog.addEventListener('cancel', function (event) {
        event.preventDefault();
        if (id === 'loginDialog') cancelLoginFlow(); else closeAccountEditor();
      });
      dialog.addEventListener('click', function (event) {
        if (event.target !== dialog) return;
        if (id === 'loginDialog') cancelLoginFlow(); else closeAccountEditor();
      });
    });
    $('loginDialog').addEventListener('close', function () {
      stopLogin();
      if (state.login.returnFocus && document.contains(state.login.returnFocus)) {
        try { state.login.returnFocus.focus(); } catch (e) {}
      }
      state.login.returnFocus = null;
    });

    try {
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function () {
        if (themeMode() === 'system') applyTheme('system', false);
      });
    } catch (e) {}
  }

  function init() {
    $('keyInput').value = state.key;
    applyTheme(themeMode(), false);
    updateKeyState();
    bind();
    setView(location.hash.slice(1) || 'overview', { noLoad: true });
    if (state.key) bootstrap(); else resetPanels();
  }

  init();
})();
