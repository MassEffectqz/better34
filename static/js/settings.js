import { App } from './state.js';
import { _, icon, esc } from './utils.js';
import { API } from './api.js';
App.toggleSettings = function () {
  this.state.settingsOpen = !this.state.settingsOpen;
  if (this.state.settingsOpen && this.state.profileOpen) {
    this.state.profileOpen = false;
    this.els.profilePanel.classList.add('hidden');
  }
  this.els.settingsPanel.classList.toggle('hidden', !this.state.settingsOpen);
  this._syncPanels();
  if (this.state.settingsOpen) { this.applyPanelWidth(); this.syncCustomControls(); }
};

App.syncCustomControls = function () {
  if (this.els.settingTheme) this.els.settingTheme.value = this.state.theme || 'dark';
  if (this.els.settingGrid) this.els.settingGrid.value = this.state.gridCols === null ? 'auto' : String(this.state.gridCols);
  this.renderAccent();
};

App.renderAPIKeys = function (keys) {
  const listEl = _('api-keys-list');
  if (!listEl) return;
  const list = (keys && keys.length) ? keys.map(k => ({ ...k })) : [{ name: '', api_key: '', user_id: '' }];
  this.state.apiKeys = list;
  listEl.innerHTML = '';
  list.forEach((k, i) => {
    const row = document.createElement('div');
    row.className = 'api-key-row';
    row.innerHTML = `
      <input type="text" class="api-key-name" placeholder="Название" value="${esc(k.name || '')}">
      <input type="password" class="api-key-value" placeholder="API ключ" value="${esc(k.api_key || '')}" spellcheck="false">
      <input type="text" class="api-key-uid" placeholder="user_id" value="${esc(k.user_id || '')}" spellcheck="false">
      <button type="button" class="btn-icon btn-icon-sm" title="Удалить">${icon('x', 13)}</button>`;
    row.querySelector('.api-key-name').addEventListener('input', e => { list[i].name = e.target.value; });
    row.querySelector('.api-key-value').addEventListener('input', e => { list[i].api_key = e.target.value; });
    row.querySelector('.api-key-uid').addEventListener('input', e => { list[i].user_id = e.target.value; });
    row.querySelector('.btn-icon').addEventListener('click', () => {
      list.splice(i, 1);
      this.renderAPIKeys(list);
    });
    listEl.appendChild(row);
  });
};

App.renderProviderOptions = function (providers) {
  const sel = this.els.settingProvider;
  if (!sel) return;
  const list = (providers && providers.length)
    ? providers
    : [{ value: 'rule34', name: 'rule34.xxx' }, { value: 'gelbooru', name: 'Gelbooru' }];
  const prev = sel.value;
  sel.innerHTML = list.map(p =>
    `<option value="${esc(p.value)}">${esc(p.name)}</option>`
  ).join('');
  if (prev) sel.value = prev;
};

App.loadSettings = async function () {
  try {
    const d = await API.get('/settings');
    this.renderProviderOptions(d.providers);
    if (this.els.settingProvider) this.els.settingProvider.value = d.provider || 'rule34';
    this.state.activeProvider = d.provider || 'rule34';
    this.state.providers = d.providers || [];
    this.state.maxQueryLen = d.max_query_len || 3800;
    this.renderProviderBadge();
    this.renderAPIKeys(d.api_keys || []);
    this.els.settingProxy.value = d.proxy_url || '';
    this.els.settingSavepath.value = d.save_path || 'data/posts';
    this.els.settingConcurrent.value = d.concurrent_downloads || 3;
    this.state.thumbSize = d.thumb_size || 300;
    this.state.autoDownload = d.auto_download || false;
    this.state.minId = d.min_id || null;
    this.state.renameTemplate = d.rename_template || '';
  } catch (err) {
    console.error('Failed to load settings:', err);
  }
};

App.saveSettings = async function () {
  try {
    const apiKeys = (this.state.apiKeys || []).filter(k => k.api_key).map(k => ({ name: k.name || '', api_key: k.api_key, user_id: k.user_id || '', ...(k.provider ? { provider: k.provider } : {}) }));
    const providerChanged = this.els.settingProvider && this.els.settingProvider.value !== this.state.activeProvider;
    await API.post('/settings', {
      api_keys: apiKeys,
      proxy_url: this.els.settingProxy.value, save_path: this.els.settingSavepath.value,
      concurrent_downloads: parseInt(this.els.settingConcurrent.value) || 3,
      thumb_size: this.state.thumbSize || 300,
      auto_download: !!this.state.autoDownload,
      min_id: this.state.minId || null,
      rename_template: this.state.renameTemplate || '',
      provider: this.els.settingProvider ? this.els.settingProvider.value : undefined,
    });
    if (this.els.settingProvider) this.state.activeProvider = this.els.settingProvider.value;
    if (providerChanged) {
      API.invalidate('/');
      this.loadPosts(true, null, true);
    }
    this.showToast('Настройки сохранены');
    this.toggleSettings();
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.renderProviderBadge = function () {
  const label = document.getElementById('provider-badge-label');
  if (!label) return;
  const list = this.state.providers && this.state.providers.length
    ? this.state.providers
    : [{ value: 'rule34', name: 'rule34.xxx' }, { value: 'gelbooru', name: 'Gelbooru' }];
  const active = list.find(p => p.value === this.state.activeProvider);
  label.textContent = active ? active.name : this.state.activeProvider;
  const menu = document.getElementById('provider-menu-list');
  if (menu) {
    menu.innerHTML = list.map(p =>
      `<button type="button" class="provider-menu-item${p.value === this.state.activeProvider ? ' active' : ''}" data-value="${esc(p.value)}">${esc(p.name)}</button>`
    ).join('');
  }
};

App.switchProvider = async function (value) {
  if (!value || value === this.state.activeProvider) return;
  try {
    await API.post('/settings', { provider: value });
    this.state.activeProvider = value;
    if (this.els.settingProvider) this.els.settingProvider.value = value;
    this.renderProviderBadge();
    API.invalidate('/');
    this.loadPosts(true, null, true);
    this.showToast(`Источник: ${value}`, 'success');
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.showStats = async function () {
  if (this.state.viewerOpen) this.closeViewer();
  try {
    const [d, tagD, user] = await Promise.all([API.get('/stats'), API.get('/tag-stats'), API.get('/user-stats').catch(() => null)]);
    const tags = tagD.tags || {};
    const tagHtml = Object.entries(tags).slice(0, 30).map(([tag, c]) =>
      `<span class="stat-tag"><span class="stat-tag-name">${esc(tag)}</span><span class="stat-tag-count">${c}</span></span>`
    ).join('');
    let userHtml = '';
    if (user) {
      const cards = `
        <div class="stat-card"><div class="stat-value">${user.likes || 0}</div><div class="stat-label">Лайки</div></div>
        <div class="stat-card"><div class="stat-value">${user.comments || 0}</div><div class="stat-label">Комментарии</div></div>
        <div class="stat-card"><div class="stat-value">${user.collections || 0}</div><div class="stat-label">Коллекции</div></div>
        <div class="stat-card"><div class="stat-value">${user.downloaded_likes || 0}</div><div class="stat-label">Скачано из лайков</div></div>`;
      const topTags = (user.top_tags || []).map(x =>
        `<span class="stat-tag"><span class="stat-tag-name">${esc(x.tag)}</span><span class="stat-tag-count">${x.count}</span></span>`
      ).join('');
      const maxAct = Math.max(1, ...(user.activity || []).map(a => a.count));
      const bars = (user.activity || []).map(a =>
        `<div class="act-col" title="${esc(a.date)}: ${a.count}"><i style="height:${Math.round((a.count / maxAct) * 100)}%"></i><span>${esc(a.date.slice(0, 5))}</span></div>`
      ).join('');
      userHtml = `
        <h3 class="stat-tags-title">Моя активность</h3>
        <div class="stat-cards">${cards}</div>
        ${(user.top_tags || []).length ? `<div class="stat-tags">${topTags}</div>` : ''}
        <div class="act-chart">${bars}</div>`;
    }
    this.els.statsBody.innerHTML = `
      <div class="stat-cards"><div class="stat-card"><div class="stat-value">${d.total_searched || 0}</div><div class="stat-label">Найдено</div></div>
      <div class="stat-card"><div class="stat-value">${d.total_downloaded || 0}</div><div class="stat-label">Скачано</div></div>
      <div class="stat-card"><div class="stat-value">${d.thumbnails || 0}</div><div class="stat-label">Миниатюр</div></div>
      <div class="stat-card"><div class="stat-value">${d.disk_usage_mb || '0'}</div><div class="stat-label">Занято (MB)</div></div></div>
      <h3 class="stat-tags-title">Топ теги (скачанные)</h3>
      <div class="stat-tags">${tagHtml || '<p style="color:var(--text-dim);font-size:.8rem">Нет данных</p>'}</div>
      ${userHtml}`;
    this.els.statsModal.classList.remove('hidden');
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.hideStats = function () { this.els.statsModal.classList.add('hidden'); };

App.cleanDB = async function () {
  const ok = await this.confirmDialog({
    title: 'Очистка базы данных',
    message: 'Удалить все <b>нескачанные</b> записи из базы? Скачанные посты не пострадают.',
    okText: 'Очистить',
    danger: true,
  });
  if (!ok) return;
  try {
    const r = await API.post('/db/clean');
    this.showToast(`База очищена: ${r.deleted} записей удалено`, 'success');
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.findDuplicates = async function () {
  const btn = this.els.btnFindDups;
  const info = this.els.dupsInfo;
  if (btn) { btn.disabled = true; btn.textContent = 'Поиск…'; }
  if (info) info.classList.add('hidden');
  this.els.btnCleanDups.classList.add('hidden');
  try {
    const d = await API.get('/dups');
    const groups = (d && d.dups) || [];
    const total = groups.reduce((s, g) => s + Math.max(0, (g.files || []).length - 1), 0);
    if (!groups.length) {
      if (info) { info.textContent = 'Дубликаты не найдены'; info.classList.remove('error'); info.classList.remove('hidden'); }
      this._dupsTotal = 0;
      return;
    }
    if (info) { info.textContent = `Найдено: ${total} дубликатов в ${groups.length} группах`; info.classList.add('error'); info.classList.remove('hidden'); }
    this._dupsTotal = total;
    this.els.btnCleanDups.textContent = `Удалить ${total} дубликатов`;
    this.els.btnCleanDups.classList.remove('hidden');
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  if (btn) { btn.disabled = false; btn.textContent = 'Найти дубликаты'; }
};

App.cleanDuplicates = async function () {
  const total = this._dupsTotal || 0;
  if (!total) { this.els.btnCleanDups.classList.add('hidden'); return; }
  const ok = await this.confirmDialog({
    title: 'Удаление дубликатов',
    message: `Удалить <b>${total}</b> файлов-дубликатов? Останется по одной копии каждого файла. Действие необратимо.`,
    okText: 'Удалить',
    danger: true,
  });
  if (!ok) return;
  try {
    const r = await API.post('/dups/clean', null, { 'X-Confirm-Dupes': '1' });
    this.showToast(`Удалено файлов: ${r.count || 0}`, 'success');
    this.els.dupsInfo.classList.add('hidden');
    this.els.btnCleanDups.classList.add('hidden');
    this._dupsTotal = 0;
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

// ── Полный бэкап профиля ────────────────────────────────────────────────

App.exportProfile = function () {
  const a = document.createElement('a');
  a.href = '/api/profile/export';
  a.download = 'briefly-profile.json';
  a.click();
  this.showToast('Профиль экспортирован', 'success');
};

App.importProfile = async function (ev) {
  const file = ev.target.files[0];
  if (!file) return;
  try {
    const data = JSON.parse(await file.text());
    if (!data || typeof data !== 'object' || !data.profile) throw new Error('Неверный формат файла');
    const r = await API.post('/profile/import', { profile: data.profile, comments: data.comments || [] });
    const n = r.imported || {};
    API.invalidate('/profile');
    await this.loadProfile();
    this.showToast(`Импорт: лайков ${n.liked_posts || 0}, скрытий ${n.hidden_posts || 0}, пресетов ${n.presets || 0}, коллекций ${n.collections || 0}, комментариев ${n.comments || 0}`, 'success');
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  ev.target.value = '';
};

// ── QR-вход на другом устройстве ──────────────────────────────────────────
App.showQRLogin = function () {
  let overlay = document.getElementById('qr-login-overlay');
  if (overlay) overlay.remove();
  overlay = document.createElement('div');
  overlay.id = 'qr-login-overlay';
  overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.72);z-index:1200;display:flex;align-items:center;justify-content:center;';
  const card = document.createElement('div');
  card.style.cssText = 'background:#1a1a1a;border-radius:16px;padding:24px;text-align:center;max-width:320px;color:#eee;box-shadow:0 8px 40px rgba(0,0,0,.5);';
  card.innerHTML = `
    <div style="font-weight:600;margin-bottom:12px">QR-вход на другом устройстве</div>
    <img id="qr-login-img" alt="QR" style="width:256px;height:256px;border-radius:8px;background:#fff">
    <div id="qr-login-hint" style="font-size:12px;opacity:.7;margin-top:12px">Отсканируйте камерой телефона. Код действует 5 минут и сгорает после входа.</div>
    <button id="qr-login-close" class="btn-primary btn-sm" style="margin-top:14px">Закрыть</button>`;
  overlay.appendChild(card);
  document.body.appendChild(overlay);
  const close = () => { document.removeEventListener('keydown', onKey); overlay.remove(); };
  const onKey = (e) => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', onKey);
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  card.querySelector('#qr-login-close').addEventListener('click', close);
  const img = card.querySelector('#qr-login-img');
  img.src = '/api/auth/qr/svg?t=' + Date.now();
  img.onerror = () => {
    img.style.display = 'none';
    card.querySelector('#qr-login-hint').textContent = 'Не удалось получить QR — проверьте, что вы залогинены.';
  };
};
