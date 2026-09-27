// social.js — обсуждение поста во вьюере: локальные комментарии между
// пользователями + комментарии с бура-источника (dapi s=comment) в одном
// списке, отсортированном по времени. Локальные живые (SSE, state.js
// type=comment), комментарии источника read-only и приходят из кэша.
import { App } from './state.js';
import { esc, icon } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

const MAX_LEN = 500;

// sortKeyTime приводит даты к числу для сортировки. Локальные комментарии
// приходят в RFC3339 («2026-09-27T12:00:00Z»), буры — в своём формате
// («2026-09-27 12:00»); нераспознанное уходит в конец списка.
function sortKeyTime(raw) {
  const s = String(raw || '').trim();
  if (!s) return 0;
  const norm = s.includes('T') ? s : s.replace(' ', 'T');
  const ms = Date.parse(norm);
  if (!Number.isNaN(ms)) return ms;
  const m = norm.match(/^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})/);
  if (m) return Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  return 0;
}

function displayTime(raw) {
  return String(raw || '').slice(0, 16).replace('T', ' ');
}

// renderComments собирает панель обсуждения. Принимает объект поста (нужен
// источник для кнопки) — для обратной совместимости допускается и голый id.
App.renderComments = function (post) {
  const host = this.els.viewerComments;
  if (!host) return;
  const p = post && typeof post === 'object' ? post : { id: post };
  const postId = p.id;
  this._commentsPostId = postId;
  this._commentsPost = p;
  this._commentsSite = typeof this.sourceSiteOf === 'function' ? this.sourceSiteOf(p) : '';
  this._localComments = null;
  this._sourceComments = null;
  this._sourceNote = '';
  const site = this._commentsSite;
  const srcTitle = site ? tf('srcComments.title', { site }) : '';
  host.innerHTML =
    '<div class="vc-head">' + esc(t('comments.head')) + ' <span class="vc-count" id="vc-count"></span>' +
      (site && !this._srcUnsupported.has(site)
        // Загрузить с источника / обновить — рядом с локальными комментариями,
        // в один список. Скрыто, если сайт не отдаёт комментарии.
        ? '<span class="vc-src-actions">' +
            '<button type="button" class="btn-ss" id="vc-src-load"' + (srcTitle ? ' title="' + esc(srcTitle) + '"' : '') + '>' +
              esc(t('srcComments.load')) + '</button>' +
            '<button type="button" class="btn-ss hidden" id="vc-src-refresh">' + esc(t('srcComments.refresh')) + '</button>' +
          '</span>'
        : '') +
    '</div>' +
    '<div class="vc-note hidden" id="vc-src-note"></div>' +
    '<div class="vc-list" id="vc-list"></div>' +
    '<form class="vc-form" id="vc-form">' +
    '<input type="text" id="vc-input" maxlength="' + MAX_LEN + '" placeholder="' + esc(t('comments.inputPh')) + '" autocomplete="off">' +
    '<span class="vc-counter" id="vc-counter">' + esc(tf('comments.left', { left: MAX_LEN, max: MAX_LEN })) + '</span>' +
    '<button type="submit" class="btn-ss" title="' + esc(t('comments.send')) + '">' + icon('arrowUp', 14, true) + '</button>' +
    '</form>';
  const input = host.querySelector('#vc-input');
  const form = host.querySelector('#vc-form');
  const counter = host.querySelector('#vc-counter');
  const updateCounter = () => {
    const remaining = MAX_LEN - input.value.length;
    if (counter) counter.textContent = tf('comments.left', { left: remaining, max: MAX_LEN });
  };
  input.addEventListener('input', updateCounter);
  updateCounter();
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const text = input.value.trim();
    if (!text || !App.state.viewerOpen) return;
    const submitBtn = form.querySelector('button[type="submit"]');
    if (submitBtn) { submitBtn.disabled = true; submitBtn.setAttribute('aria-busy', 'true'); }
    try {
      await API.post(`/comments/${postId}`, { text });
      input.value = '';
      if (counter) counter.textContent = tf('comments.left', { left: MAX_LEN, max: MAX_LEN });
      App.loadComments(postId);
    } catch (err) {
      App.showToast(String(err && err.message || err).slice(0, 80), 'error');
    } finally {
      if (submitBtn) { submitBtn.disabled = false; submitBtn.removeAttribute('aria-busy'); }
    }
  });
  // Кнопки комментариев источника живут в той же панели, что и локальные.
  const srcLoad = host.querySelector('#vc-src-load');
  const srcRefresh = host.querySelector('#vc-src-refresh');
  if (srcLoad) srcLoad.addEventListener('click', () => App.fetchSourceComments(false));
  if (srcRefresh) srcRefresh.addEventListener('click', () => App.fetchSourceComments(true));
  App.loadComments(postId);
  // Кэш источника подтягиваем сразу: это запрос к нашему серверу, буры не
  // трогаем (cachedOnly) — сеть не тратится, открытый пост сразу полный.
  if (site) App.fetchSourceComments(false, true);
};

// loadComments тянет локальные комментарии и перерисовывает общий список.
App.loadComments = function (postId) {
  const list = document.getElementById('vc-list');
  if (!list || !this.state.viewerOpen) return;
  if (this._localComments === null) {
    list.innerHTML = `<div class="vc-loading"><span class="pf-more-spin"></span> ${esc(t('comments.loading'))}</div>`;
  }
  API.get('/comments/' + postId, { fresh: true }).then((d) => {
    // Пока грузились — пост уже сменился.
    if (this._commentsPostId !== postId || !this.state.viewerOpen) return;
    this._localComments = (d && d.comments) || [];
    this.renderMergedComments();
  }).catch(() => {
    if (this._commentsPostId !== postId) return;
    this._localComments = [];
    this.renderMergedComments(true);
  });
};

// fetchSourceComments подтягивает комментарии источника и вливает их в общий
// список. cachedOnly=true — читаем только кэш (при открытии поста).
App.fetchSourceComments = function (refresh, cachedOnly) {
  const postId = this._commentsPostId;
  const site = this._commentsSite;
  if (!postId || !site) return Promise.resolve();
  if (typeof this.loadSourceComments !== 'function') return Promise.resolve();
  const buttons = Array.from(document.querySelectorAll('.vc-src-actions .btn-ss'));
  buttons.forEach((b) => { b.disabled = true; b.setAttribute('aria-busy', 'true'); });
  const note = document.getElementById('vc-src-note');
  if (note && !cachedOnly) {
    note.textContent = t('srcComments.loading');
    note.classList.remove('hidden');
  }
  return this.loadSourceComments(postId, site, { refresh, cachedOnly })
    .then((d) => {
      if (this._commentsPostId !== postId || !this.state.viewerOpen) return;
      this._sourceComments = d.comments || [];
      this._sourceNote = d.unsupported ? t('srcComments.unsupported')
        : (d.error ? t('srcComments.failed') : '');
      // Кнопка «обновить» нужна, когда мы реально получили данные.
      const refreshBtn = document.getElementById('vc-src-refresh');
      if (refreshBtn && !d.unsupported) refreshBtn.classList.remove('hidden');
      const loadBtn = document.getElementById('vc-src-load');
      if (loadBtn && d.unsupported) loadBtn.classList.add('hidden');
      this.renderMergedComments();
    })
    .finally(() => {
      buttons.forEach((b) => { b.disabled = false; b.removeAttribute('aria-busy'); });
    });
};

// renderMergedComments рисует единый список: локальные комментарии и
// комментарии источника вперемешку по времени. Локальные помечены аватаром и
// дают удалять, сообщения с бура — read-only, с подписью сайта.
App.renderMergedComments = function (loadFailed) {
  const list = document.getElementById('vc-list');
  if (!list) return;
  const me = this.state.user ? (this.state.user.username || '') : '';
  const site = this._commentsSite;
  const local = this._localComments || [];
  const src = this._sourceComments || [];

  const items = [];
  local.forEach((cm) => {
    const mine = me && cm.username === me;
    const author = cm.nickname && cm.nickname !== cm.username ? cm.nickname : cm.username;
    // Аватар — URL из чужого профиля: пропускаем только безопасные схемы,
    // javascript:/data:text и прочее не должно попасть в src.
    const avatarOK = cm.avatar && /^(https?:\/\/|\/|data:image\/)/.test(cm.avatar);
    const avatar = avatarOK
      ? `<img class="vc-avatar" src="${esc(cm.avatar)}" alt="">`
      : '<span class="vc-avatar vc-avatar-empty"></span>';
    const del = mine
      ? `<button type="button" class="vc-del" data-cid="${cm.id}" title="${esc(t('comments.del'))}">${icon('x', 11)}</button>`
      : '';
    const when = displayTime(cm.created_at);
    items.push({
      sort: sortKeyTime(cm.created_at),
      html: `<div class="vc-item${mine ? ' mine' : ''}">${avatar}` +
        `<div class="vc-body"><div class="vc-meta">${esc(author)} · <time>${esc(when)}</time>${del}</div>` +
        `<div class="vc-text">${esc(cm.text)}</div></div></div>`,
    });
  });
  src.forEach((c) => {
    // Текст с чужого сайта: экранируем всё, ссылки не рендерим, длинное режем.
    const body = String(c.body || '');
    const max = this.SRC_COMMENT_MAX || 2000;
    const shown = body.length > max ? esc(body.slice(0, max)) + '…' : esc(body);
    const when = displayTime(c.created_at);
    const tag = site
      ? `<span class="vc-src-tag" title="${esc(tf('srcComments.title', { site }))}">${esc(site)}</span>`
      : '';
    items.push({
      sort: sortKeyTime(c.created_at),
      html: '<div class="vc-item src"><div class="vc-body"><div class="vc-meta">' + tag +
        esc(c.author || '?') + (when ? ' · <time>' + esc(when) + '</time>' : '') + '</div>' +
        `<div class="vc-text">${shown}</div></div></div>`,
    });
  });

  // Порядок по времени; при равных датах (и пустых — sort 0) сохраняем
  // стабильность по исходному индексу, иначе список прыгал бы при обновлении.
  items.forEach((it, i) => { it.i = i; });
  items.sort((a, b) => (a.sort - b.sort) || (a.i - b.i));

  const count = document.getElementById('vc-count');
  if (count) count.textContent = items.length ? `(${items.length})` : '';
  const note = document.getElementById('vc-src-note');
  if (note) {
    const msg = this._sourceNote || '';
    note.textContent = msg;
    note.classList.toggle('hidden', !msg);
  }
  if (!items.length) {
    list.innerHTML = `<div class="vc-empty">${esc(loadFailed ? t('comments.loadFailed') : t('comments.empty'))}</div>`;
    return;
  }
  list.innerHTML = items.map((it) => it.html).join('');
  list.scrollTop = list.scrollHeight;
  list.querySelectorAll('.vc-del').forEach(btn => {
    btn.addEventListener('click', async () => {
      const ok = await this.confirmDialog({
        title: t('comments.delTitle'),
        message: t('comments.delMsg'),
        okText: t('confirm.ok'),
        danger: true,
      });
      if (!ok) return;
      try { await API.del(`/comments/${btn.dataset.cid}`); App.loadComments(App._commentsPostId); }
      catch { /* noop */ }
    });
  });
};

// SSE: кто-то (возможно с другого устройства) написал в текущий пост.
App._onCommentEvent = function (postId) {
  if (this.state.viewerOpen &&
      this.state.posts[this.state.viewerIndex] &&
      this.state.posts[this.state.viewerIndex].id === postId) {
    this.loadComments(postId);
  }
};
