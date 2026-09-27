import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';
App._thumbs = App._thumbs || {};

// Превью помечаются kind=preview: immutable Cache-Control + cache-first
// в service worker (превью неизменяемы по построению).
const pfProxy = u => `/api/proxy?url=${encodeURIComponent(u)}&kind=preview`;

// Кандидаты превью по порядку предпочтения. Для старых лайков preview_url на
// CDN часто протухает (хотлинк/удаление поста), а оригинал или локальная
// миниатюра при этом живы — tile перебирает кандидатов при ошибке загрузки.
const pfCandidatesFor = post => {
  const out = [];
  const push = u => { if (u && !out.includes(u)) out.push(u); };
  if (post.preview_url) push(pfProxy(post.preview_url));
  if (post.downloaded && post.thumb_path) push(`/api/thumb/${post.id}`);
  if (post.sample_url) push(pfProxy(post.sample_url));
  if (post.file_url) push(pfProxy(post.file_url));
  if (!out.length && post.downloaded) push(`/api/thumb/${post.id}`);
  return out;
};

App.renderProfile = function () {
  const p = this.state.profile;
  const img = this.els.profileAvatarImg;
  if (p.avatar) {
    img.src = p.avatar;
    img.style.display = 'block';
  } else {
    // Пустой src="" заставляет браузер запросить саму страницу, а .src при
    // чтении отдаёт URL документа — атрибут просто убираем.
    img.removeAttribute('src');
    img.style.display = 'none';
  }
  this.els.profileNickname.value = p.nickname || '';
  this._syncAvatarInitials(p.nickname);
  this._lastProfileMeta = { nickname: p.nickname || '', avatar: p.avatar || '' };
  this.updateAuthUI();
  this.renderPresets(p.presets);
  this.renderPresetMenu();
  this.renderThumbs('likes', p.liked_posts);
  this.renderThumbs('hides', p.hidden_posts);
  if (typeof this.renderCollections === 'function') this.renderCollections();
  this.renderTagLists();
  this._updateTagFilterCount();
  this.updateStats(p);
};

// Инициал вместо пустого тёмного круга, когда аватар не задан.
App._syncAvatarInitials = function (nickname) {
  const el = this.els.profileAvatarInitials;
  if (!el) return;
  const hasAvatar = !!(this.els.profileAvatarImg && this.els.profileAvatarImg.getAttribute('src'));
  if (hasAvatar) {
    el.textContent = '';
    el.style.display = 'none';
    return;
  }
  const u = this.state.user || {};
  const name = (nickname || u.nickname || u.username || '?').trim();
  el.textContent = (name.charAt(0) || '?').toUpperCase();
  el.style.display = '';
};

App.renderTagLists = function () {
  const p = this.state.profile || {};
  this.renderTagList('favTagsList', p.fav_tags || [], 'fav');
  this.renderTagList('hiddenTagsList', p.hidden_tags || [], 'hidden');
  this.renderTagPresetSelects();
};

App._tagPresetLists = function (pr) {
  const k = pr.kind || 'query';
  if (k === 'hidden') return { hidden: pr.tags || [], fav: [] };
  if (k === 'fav') return { hidden: [], fav: pr.tags || [] };
  return { hidden: pr.hidden_tags || [], fav: pr.fav_tags || [] };
};

App.renderTagPresetSelects = function () {
  const presets = ((this.state.profile && this.state.profile.presets) || [])
    .filter(pr => (pr.kind || 'query') !== 'query');
  const plural = (n, one, few, many) => {
    const m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return one;
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
    return many;
  };
  [['fav', this.els.favPresetSelect], ['hidden', this.els.hiddenPresetSelect]].forEach(([type, el]) => {
    if (!el) return;
    el.innerHTML = `<option value="">${esc(t('preset.standCurrent'))}</option>` + presets.map(pr => {
      const lists = this._tagPresetLists(pr);
      const n = type === 'fav' ? lists.fav.length : lists.hidden.length;
      return `<option value="${esc(pr.id)}">${esc(pr.name)} — ${n} ${plural(n, t('pf.tagOne'), t('pf.tagFew'), t('pf.tagMany'))}</option>`;
    }).join('');
    el.onchange = () => {
      const id = el.value;
      el.value = '';
      if (id) this.applyPresetById(id, type);
    };
  });
};

App.onTagFilter = function () {
  // U9: debounce 300ms — фильтрация на каждый keydown тормозит на больших списках.
  clearTimeout(this._tagFilterTimer);
  this._tagFilterTimer = setTimeout(() => {
    this._tagFilter = (this.els.tagFilter.value || '').toLowerCase().trim();
    this.renderTagLists();
    this._updateTagFilterCount();
  }, 300);
};

App._updateTagFilterCount = function () {
  const el = this.els.tagFilterCount;
  if (!el) return;
  const p = this.state.profile || {};
  const q = this._tagFilter || '';
  const fav = (p.fav_tags || []).filter(tag => !q || tag.toLowerCase().includes(q));
  const hid = (p.hidden_tags || []).filter(tag => !q || tag.toLowerCase().includes(q));
  const total = (p.fav_tags || []).length + (p.hidden_tags || []).length;
  const shown = fav.length + hid.length;
  el.textContent = total ? (q ? tf('pf.filterCount', { shown, total }) : `${total}`) : '';
};

App.updateStats = function (p) {
  const setNum = (id, n) => {
    const el = document.getElementById(id);
    if (el) el.textContent = n;
  };
  const base = this.state.profile || {};
  if (!p) p = base;
  const presetN = (p.presets || []).length;
  const likeN = (p.liked_posts || []).length;
  const hideN = (p.hidden_posts || []).length;
  const tagN = (p.fav_tags || []).length + (p.hidden_tags || []).length;
  const colN = (p.collections || []).length;
  setNum('st-presets', presetN);
  setNum('st-likes', likeN);
  setNum('st-hides', hideN);
  setNum('st-tags', tagN);
  setNum('st-collections', colN);
  // «Скачать все лайки» бессмыслен при пустом списке.
  const dlBtn = this.els.btnLikesDownload;
  if (dlBtn) dlBtn.style.display = likeN ? '' : 'none';
};

App.renderPresets = function (presets) {
  const pl = this.els.presetsList;
  pl.innerHTML = '';
  const cur = this.state.query || '';
  const hint = this.els.presetCurrent;
  if (hint) {
    hint.innerHTML = cur
      ? `${esc(t('preset.savingCurrent'))} <b>${esc(cur)}</b>`
      : esc(t('preset.saveHint'));
  }
  presets = (presets || []).filter(pr => (pr.kind || 'query') === 'query');
  if (!presets.length) {
    pl.innerHTML = `<p class="profile-empty">${esc(t('preset.empty'))}</p>`;
    return;
  }
  presets.forEach((pr, i) => {
    const active = (pr.query || '') === cur;
    const d = document.createElement('div');
    d.className = 'preset-item' + (active ? ' active' : '');
    d.innerHTML = `
      <span class="preset-kind preset-kind-query">${icon('search', 13)}</span>
      <span class="preset-name">${esc(pr.name)}</span>
      <span class="preset-query">${esc(pr.query || t('preset.main'))}</span>
      <button class="btn-icon btn-icon-sm pf-up" title="${esc(t('preset.moveUp'))}">${icon('arrowUp', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-down" title="${esc(t('preset.moveDown'))}">${icon('arrowDown', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-edit" title="${esc(t('preset.rename'))}">${icon('pencil', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-del" title="${esc(t('preset.delete'))}">${icon('x', 14)}</button>`;
    d.addEventListener('click', (e) => {
      if (e.target.closest('button')) return;
      this.applyPreset(pr);
      this.toggleProfile();
    });
    const up = d.querySelector('.pf-up');
    const down = d.querySelector('.pf-down');
    if (i === 0) up.style.visibility = 'hidden';
    if (i === presets.length - 1) down.style.visibility = 'hidden';
    up.addEventListener('click', (e) => { e.stopPropagation(); this.movePreset(pr.id, -1); });
    down.addEventListener('click', (e) => { e.stopPropagation(); this.movePreset(pr.id, 1); });
    d.querySelector('.pf-edit').addEventListener('click', (e) => { e.stopPropagation(); this.editPreset(d, pr); });
    d.querySelector('.pf-del').addEventListener('click', async (e) => {
      e.stopPropagation();
      const ok = await this.confirmDialog({
        title: t('preset.delete'),
        message: tf('preset.deleteConfirm', { name: esc(pr.name) }),
        okText: t('btn.delete'),
        danger: true,
      });
      if (!ok) return;
      API.del(`/preset/${pr.id}`).then(() => { API.invalidate('/profile'); this.loadProfile(); this.showToast(tf('preset.deleted', { name: pr.name })); }).catch(() => {});
    });
    pl.appendChild(d);
  });
};

App.editPreset = function (d, pr) {
  const nameEl = d.querySelector('.preset-name');
  const input = document.createElement('input');
  input.className = 'preset-rename';
  input.value = pr.name;
  input.maxLength = 60;
  input.spellcheck = false;
  nameEl.replaceWith(input);
  input.focus();
  input.select();
  let doneFlag = false;
  const finish = (save) => {
    if (doneFlag) return;
    doneFlag = true;
    const name = input.value.trim();
    if (save && name && name !== pr.name) {
      API.patch(`/preset/${pr.id}`, { name }).then(() => {
        API.invalidate('/profile');
        this.loadProfile();
        this.showToast(t('preset.renamed'));
      }).catch(err => {
        this.showToast(tf('err.withMsg', { msg: err.message }), 'error');
        nameEl.textContent = pr.name;
        input.replaceWith(nameEl);
      });
    } else {
      nameEl.textContent = pr.name;
      input.replaceWith(nameEl);
    }
  };
  input.addEventListener('keydown', (ev) => {
    ev.stopPropagation();
    if (ev.key === 'Enter') { ev.preventDefault(); finish(true); }
    else if (ev.key === 'Escape') { finish(false); }
  });
  input.addEventListener('blur', () => finish(true));
};

App.movePreset = function (id, dir) {
  API.post(`/preset/${id}/move`, { dir }).then(() => {
    API.invalidate('/profile');
    this.loadProfile();
  }).catch(() => {});
};

App._thumbsBatch = 100;

// ── Опции вкладок «Лайки»/«Скрытые»: сортировка и фильтр плиток ───────────
// Фильтры считаются по полям ответа /posts-by-ids: viewed (view_history),
// downloaded, file_type, плюс заглушки «недоступен». Сортировка лайков — по
// liked_at (карта из /api/profile), скрытых — по id (бэкенд отдаёт по убыванию).
// Настройка переживает перезагрузку: хранится в localStorage.
const THUMB_OPTS_KEY = 'briefly_thumb_opts';
const THUMB_FILTERS = ['all', 'downloaded', 'unviewed', 'video', 'unavailable'];
const THUMB_SORTS = ['new', 'old'];

App._thumbOpts = { likes: { sort: 'new', filter: 'all' }, hides: { sort: 'new', filter: 'all' } };
App._thumbOptsLoaded = false;

App.loadThumbOpts = function () {
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(THUMB_OPTS_KEY) || 'null'); } catch { saved = null; }
  ['likes', 'hides'].forEach(k => {
    const s = (saved && saved[k]) || {};
    const o = this._thumbOpts[k];
    if (THUMB_SORTS.includes(s.sort)) o.sort = s.sort;
    if (THUMB_FILTERS.includes(s.filter)) o.filter = s.filter;
  });
  this._thumbOptsLoaded = true;
  return this._thumbOpts;
};

App._saveThumbOpts = function () {
  try { localStorage.setItem(THUMB_OPTS_KEY, JSON.stringify(this._thumbOpts)); } catch { /* приватный режим */ }
};

App.setThumbSort = function (type, sort) {
  if (!THUMB_SORTS.includes(sort) || !this._thumbOpts[type]) return;
  this._thumbOpts[type].sort = sort;
  this._saveThumbOpts();
  this._syncThumbTools(type);
  this._applyThumbOpts(type);
};

App.setThumbFilter = function (type, filter) {
  if (!THUMB_FILTERS.includes(filter) || !this._thumbOpts[type]) return;
  this._thumbOpts[type].filter = filter;
  this._saveThumbOpts();
  this._syncThumbTools(type);
  this._applyThumbOpts(type);
};

// Предикаты фильтра — чистые функции: их удобно тестировать без DOM.
App._thumbIsDownloaded = post => !!(post && (post.downloaded || post.downloadedAt));
App._thumbIsVideo = post => !!(post && (post.file_type === 'video' || post.file_type === 'gif'));
App._thumbMatchesFilter = function (post, filter) {
  switch (filter) {
    case 'downloaded': return this._thumbIsDownloaded(post);
    case 'unviewed': return !(post && post.viewed);
    case 'video': return this._thumbIsVideo(post);
    case 'unavailable': return !!(post && post.missing);
    default: return true;
  }
};
// Ключ сортировки: у лайков приоритет у времени лайка, у скрытых — у id.
App._thumbSortKey = function (type, post) {
  const likedAt = ((this.state.profile || {}).liked_at || {})[post && post.id];
  if (type === 'likes' && likedAt) return Number(likedAt);
  return Number(post && post.id) || 0;
};

// Пересчёт видимости и порядка уже отрисованных плиток. Работает поверх DOM,
// поэтому не трогает постепенную подгрузку: недогруженные плитки применят
// текущие опции при добавлении.
App._applyThumbOpts = function (type) {
  const s = this._thumbs && this._thumbs[type];
  if (!s || !s.tiles) return;
  const el = type === 'likes' ? this.els.likesList : this.els.hidesList;
  if (!el) return;
  if (!this._thumbOptsLoaded) this.loadThumbOpts();
  const filter = this._thumbOpts[type].filter;
  const dir = this._thumbOpts[type].sort === 'old' ? -1 : 1;
  s.tiles.forEach(tile => {
    const on = this._thumbMatchesFilter(tile._pfPost, filter);
    tile.hidden = !on;
    if (tile.classList) tile.classList.toggle('pf-hidden', !on);
  });
  const sorted = s.tiles.slice().sort((a, b) =>
    (this._thumbSortKey(type, a._pfPost) - this._thumbSortKey(type, b._pfPost)) * dir);
  sorted.forEach(tile => el.appendChild(tile));
  this._updateThumbCount(type);
};

App._updateThumbCount = function (type) {
  const counter = document.getElementById(type === 'likes' ? 'likes-shown-count' : 'hides-shown-count');
  if (!counter) return;
  const s = this._thumbs && this._thumbs[type];
  if (!s || !s.tiles) { counter.textContent = ''; return; }
  const total = (s.ids || []).length;
  const shown = s.tiles.filter(tl => !tl.hidden).length;
  counter.textContent = total ? tf('pf.shownOf', { shown, total }) : '';
};

// Подсветка активных чипов/кнопок сортировки после смены опций (в т.ч. при
// восстановлении из localStorage и переключении языка).
App._syncThumbTools = function (type) {
  const o = this._thumbOpts[type];
  if (!o) return;
  const box = document.getElementById(type === 'likes' ? 'likes-tools' : 'hides-tools');
  if (!box) return;
  box.querySelectorAll('[data-sort]').forEach(b => {
    const on = b.dataset.sort === o.sort;
    b.classList.toggle('active', on);
    b.setAttribute('aria-pressed', on ? 'true' : 'false');
  });
  box.querySelectorAll('[data-filter]').forEach(b => {
    const on = b.dataset.filter === o.filter;
    b.classList.toggle('active', on);
    b.setAttribute('aria-pressed', on ? 'true' : 'false');
  });
};

App.renderThumbs = function (type, ids) {
  this.initThumbRecheck();
  if (!this._thumbs) this._thumbs = {};
  const el = this.els[type === 'likes' ? 'likesList' : 'hidesList'];
  const empty = this.els[type === 'likes' ? 'likesEmpty' : 'hidesEmpty'];
  const gridBtn = type === 'likes' ? this.els.btnLikesGrid : this.els.btnHidesGrid;
  const list = ids || [];
  // Тулбар (чипы, счётчик, панель выбора) приводим к сохранённым опциям: они
  // восстанавливаются из localStorage и не зависят от порядка отрисовки плиток.
  if (!this._thumbOptsLoaded) this.loadThumbOpts();
  this._syncThumbTools(type);
  this._syncThumbSelBar(type);


  if (!list.length) {
    el.innerHTML = '';
    if (empty) empty.style.display = '';
    if (gridBtn) gridBtn.style.display = 'none';
    this._hideThumbMore(type);
    this._thumbs[type] = { key: '', ids: [], posts: [], loaded: 0, token: 0, missing: [], tiles: [] };
    return;
  }
  if (empty) empty.style.display = 'none';
  if (gridBtn) gridBtn.style.display = '';

  const key = list.join(',');
  const tabEl = document.getElementById(type === 'likes' ? 'tab-likes' : 'tab-hides');
  const tabActive = !tabEl || !tabEl.closest('.panel-nav') || tabEl.classList.contains('active');
  const ex = this._thumbs[type];

  if (ex && ex.key === key) {
    if (ex.loaded > 0 && tabActive) {
      el.innerHTML = '';
      // Пересоздаём DOM плиток, поэтому список узлов тоже с нуля — иначе
      // новые плитки добавились бы к старым и в сортировке/выборе каждая
      // учитывалась бы дважды.
      ex.tiles = [];
      this._appendThumbs(el, ex.posts, type);
      if (ex.missing && ex.missing.length) this._appendMissingThumbs(el, ex.missing, type);
      this._updateThumbMore(type);
      return;
    }
    if (!tabActive) { el.innerHTML = ''; return; }
  }

  this._thumbs[type] = { key, ids: list, posts: [], loaded: 0, token: 0, missing: [], tiles: [] };
  el.innerHTML = '';
  const n = Math.min(6, list.length);
  for (let i = 0; i < n; i++) {
    const s = document.createElement('div');
    s.className = 'pf-thumb-skeleton';
    el.appendChild(s);
  }
  if (tabActive) this.loadMoreThumbs(type);
};

App.loadMoreThumbs = async function (type) {
  const s = this._thumbs && this._thumbs[type];
  if (!s) return;
  const total = s.ids ? s.ids.length : 0;
  if (s.loaded >= total) { this._updateThumbMore(type); return; }
  const moreBtn = type === 'likes' ? this.els.btnLikesMore : this.els.btnHidesMore;
  const token = ++s.token;
  if (moreBtn) { moreBtn.disabled = true; moreBtn.style.display = ''; moreBtn.innerHTML = `<span class="pf-more-spin"></span>${esc(t('pf.loading'))}`; }
  const next = Math.min(s.loaded + this._thumbsBatch, total);
  const ids = s.ids.slice(s.loaded, next);
  try {
    const data = await API.get(`/posts-by-ids?ids=${ids.join(',')}`);
    if (s.token !== token) return;
    const posts = (data && data.posts) || [];
    const found = new Set(posts.map(p => p.id).filter(Number.isFinite));
    // Посты, о которых источник НЕ ОТВЕТИЛ (сеть/лимит). Это не «поста нет»:
    // такие не кладём в s.missing, иначе одна сетевая ошибка навсегда
    // закрепила бы живой пост как недоступный — до перезагрузки страницы.
    const unresolved = new Set((data && data.unresolved) || []);
    posts.forEach(p => s.posts.push(p));
    s.loaded = next;
    const el = type === 'likes' ? this.els.likesList : this.els.hidesList;
    this._appendThumbs(el, posts, type);
    el.querySelectorAll('.pf-thumb-skeleton').forEach(s => s.remove());
    // Старые лайки, которых уже нет ни в локальной БД, ни на источнике:
    // показываем заглушку «недоступен», чтобы было видно, что id учитывался.
    const already = new Set(s.missing || []);
    const missing = ids.filter(id => !found.has(id) && !unresolved.has(id) && !already.has(id));
    if (missing.length) {
      s.missing = [...(s.missing || []), ...missing];
      this._appendMissingThumbs(el, missing, type);
    }
    // Непроверенные — с кнопкой повтора: когда сеть вернётся, их можно
    // переспросить, не перезагружая вкладку.
    const retryable = ids.filter(id => unresolved.has(id) && !already.has(id));
    if (retryable.length) this._appendUnresolvedThumbs(el, retryable, type);
    // Оба непроверенных множества — в очередь автоперепроверки: пользователю
    // не нужно жать «проверить» на каждой плитке, приложение переспросит само.
    // Для missing счётчик попыток растёт: сколько раз источник сказал «поста
    // нет» — столько раз id переспрашивается, и только потом признаётся
    // удалённым (см. THUMB_MISSING_TRIES).
    this._queueThumbRecheck(type, retryable, false);
    this._queueThumbRecheck(type, missing, true);
    this._syncThumbRecheckBar(type);
    this._armThumbRecheckTimer();
    this._updateThumbMore(type);
  } catch (err) {
    if (s.token === token && moreBtn) { moreBtn.disabled = false; moreBtn.textContent = t('pf.retry'); }
  }
};

App._hideThumbMore = function (type) {
  const more = type === 'likes' ? this.els.btnLikesMore : this.els.btnHidesMore;
  if (more) { more.style.display = 'none'; more.disabled = false; }
};

App._updateThumbMore = function (type) {
  const more = type === 'likes' ? this.els.btnLikesMore : this.els.btnHidesMore;
  const s = this._thumbs && this._thumbs[type];
  if (!more || !s) return;
  const left = (s.ids ? s.ids.length : 0) - s.loaded;
  if (s.loaded <= 0 || left <= 0) { more.style.display = 'none'; more.disabled = false; return; }
  more.style.display = '';
  more.disabled = false;
  more.textContent = tf('pf.showMore', { n: left });
};

App._buildThumbTile = function (post, i, type) {
  const tile = document.createElement('div');
  tile.className = 'pf-thumb';
  tile.style.setProperty('--d', `${Math.min(i, 14) * 26}ms`);
  tile._pfPost = post;
  tile.dataset.pfId = String(post.id);
  if (post.width && post.height) tile.style.aspectRatio = String(Math.min(post.width / post.height, 1.4));
  const srcs = pfCandidatesFor(post);
  const play = post.file_type === 'video'
    ? `<span class="pf-play">${icon('play', null, true)}</span>`
    : post.file_type === 'gif'
      ? '<span class="pf-gif">GIF</span>'
      : '';
  const dl = (post.downloadedAt || post.downloaded)
    ? `<span class="pf-dl" title="${esc(t('pf.downloaded'))}">${icon('check', null, true)}</span>`
    : '';
  const score = post.score ? `<span class="pf-score">${icon('star', null, true)}${post.score}</span>` : '';
  // Метка «новое»: пост помечен liked/hidden, но ни разу не открыт (view_history).
  const fresh = !post.viewed ? `<span class="pf-new" title="${esc(t('pf.filterUnviewed'))}">${esc(t('pf.newBadge'))}</span>` : '';
  // Быстрые действия: в списке лайков «лайк» снимает отметку, в списке скрытых
  // «глаз» возвращает пост. Подписи зависят от вкладки.
  const likeTitle = type === 'likes' ? t('pf.unlike') : t('pf.like');
  const hideTitle = type === 'hides' ? t('pf.unhide') : t('pf.hide');
  const acts = `<div class="pf-acts">
    <button class="pf-act" data-act="like" title="${esc(likeTitle)}" aria-label="${esc(likeTitle)}">${icon('heart', 14, type === 'likes')}</button>
    <button class="pf-act" data-act="hide" title="${esc(hideTitle)}" aria-label="${esc(hideTitle)}">${icon(type === 'hides' ? 'eye' : 'eyeOff', 14)}</button>
    <button class="pf-act" data-act="download" title="${esc(t('pf.downloadPost'))}" aria-label="${esc(t('pf.downloadPost'))}">${icon('download', 14)}</button>
  </div>`;
  const sel = `<button class="pf-sel" data-act="select" title="${esc(t('pf.select'))}" aria-label="${esc(t('pf.select'))}" aria-pressed="false">${icon('check', 14, true)}</button>`;
  tile.innerHTML = `<img src="" alt="" loading="lazy" decoding="async"><div class="pf-fallback">#${post.id}<span class="pf-err"></span></div>${play}${dl}${fresh}${acts}${sel}<div class="pf-open"><span class="pf-open-icon">${icon('externalLink')}</span></div><div class="pf-bar"><span>#${post.id}</span>${score}</div>`;
  // Клавиатурная доступность: плитка как кнопка (Enter/Пробел — открыть).
  tile.setAttribute('role', 'button');
  tile.setAttribute('tabindex', '0');
  tile.setAttribute('aria-label', tf('pf.openPost', { id: post.id }));
  const img = tile.querySelector('img');
  const errEl = tile.querySelector('.pf-err');
  if (img) {
    // Перебор кандидатов: preview_url → локальная миниатюра → sample → original.
    // Смена src отменяет старый запрос («error» по aborted-запросу) — отсекаем
    // по номеру попытки.
    let attempt = 0;
    let done = false;
    img.dataset.attempt = '0';
    const tryNext = () => {
      if (done) return;
      if (attempt >= srcs.length) {
        tile.classList.add('pf-broken');
        if (errEl) errEl.textContent = t('pf.unavailable');
        return;
      }
      img.dataset.attempt = String(++attempt);
      img.src = srcs[attempt - 1];
    };
    img.addEventListener('load', () => {
      if (String(attempt) !== img.dataset.attempt) return;
      done = true;
      img.classList.add('loaded');
      tile.classList.remove('pf-broken');
    });
    img.addEventListener('error', () => {
      if (done || String(attempt) !== img.dataset.attempt) return;
      img.style.display = 'none';
      tryNext();
    }, { once: false });
    tryNext();
  }
  tile.addEventListener('click', (e) => {
    // Кнопки быстрых действий и чекбокс выбора живут внутри плитки, но ведут
    // себя как отдельные элементы: клик по ним не открывает пост.
    const btn = e.target && e.target.closest ? e.target.closest('.pf-act, .pf-sel') : null;
    if (btn) {
      e.preventDefault();
      e.stopPropagation();
      this.onThumbAction(type, post, btn.dataset.act, tile);
      return;
    }
    // Ctrl/Cmd+ЛКМ — как у ссылки: пост открывается в новой вкладке.
    if (this.isOpenInNewTabClick(e)) {
      e.preventDefault();
      this.openInNewTab(this.postUrl(this.state.query, post.id));
      return;
    }
    // В режиме выбора клик по плитке (и Enter/Пробел) выделяет, а не открывает.
    if (this._thumbSelMode && this._thumbSelMode[type]) {
      this.toggleThumbSelect(type, post.id, tile);
      return;
    }
    this.openPostFromProfile(post);
  });
  tile.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    tile.click();
  });
  return tile;
};

// Заглушки для лайков/скрытий, которых нет ни локально, ни на источнике
// (пост удалён/CDN отдаёт 404): пользователь видит, что id учтён в профиле.
App._appendMissingThumbs = function (el, ids, type) {
  if (!el || !ids || !ids.length) return;
  const s = this._thumbs && this._thumbs[type];
  const frag = document.createDocumentFragment();
  ids.forEach(id => {
    const tile = document.createElement('div');
    tile.className = 'pf-thumb pf-broken pf-missing';
    tile.title = tf('pf.postUnavailable', { id });
    tile._pfPost = { id, missing: true };
    tile.dataset.pfId = String(id);
    tile.textContent = '';
    tile.appendChild((() => {
      const fb = document.createElement('div');
      fb.className = 'pf-fallback';
      fb.textContent = tf('pf.missingTile', { id });
      return fb;
    })());
    if (s && s.tiles) s.tiles.push(tile);
    frag.appendChild(tile);
  });
  el.appendChild(frag);
  if (s) this._applyThumbOpts(type);
};

// Заглушки для id, о которых источник не ответил (сеть/лимит). В отличие от
// «недоступен» это НЕ приговор посту, поэтому здесь кнопка повтора: когда сеть
// вернётся, переспрашиваем ровно эти id.
App._appendUnresolvedThumbs = function (el, ids, type) {
  if (!el || !ids || !ids.length) return;
  const s = this._thumbs && this._thumbs[type];
  const frag = document.createDocumentFragment();
  ids.forEach(id => {
    const tile = document.createElement('div');
    tile.className = 'pf-thumb pf-unresolved';
    tile.title = tf('pf.unresolvedTile', { id });
    tile._pfPost = { id, unresolved: true };
    tile.dataset.pfId = String(id);
    const fb = document.createElement('div');
    fb.className = 'pf-fallback';
    fb.textContent = tf('pf.unresolvedTile', { id });
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'pf-retry';
    btn.dataset.retryThumbs = String(id);
    btn.dataset.retryType = type;
    btn.textContent = t('pf.retryCheck');
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      this.retryUnresolvedThumbs(type, id);
    });
    tile.appendChild(fb);
    tile.appendChild(btn);
    if (s && s.tiles) s.tiles.push(tile);
    frag.appendChild(tile);
  });
  el.appendChild(frag);
  if (s) this._applyThumbOpts(type);
};

// retryUnresolvedThumbs переспрашивает у источника один id и, если тот ответил,
// заменяет заглушку настоящей плиткой.
App.retryUnresolvedThumbs = async function (type, id) {
  const s = this._thumbs && this._thumbs[type];
  const num = Number(id);
  if (!s || !Number.isFinite(num)) return;
  if (s.retrying && s.retrying.has(num)) return;
  s.retrying = s.retrying || new Set();
  s.retrying.add(num);
  try {
    const data = await API.get(`/posts-by-ids?ids=${num}`);
    const post = ((data && data.posts) || []).find(p => p && p.id === num);
    if (!post) return; // по-прежнему нет ответа — заглушка остаётся
    s.posts.push(post);
    const el = type === 'likes' ? this.els.likesList : this.els.hidesList;
    if (!el) return;
    const stale = (s.tiles || []).find(tile => tile._pfPost && tile._pfPost.unresolved && Number(tile.dataset.pfId) === num);
    if (stale) {
      stale.remove();
      s.tiles = (s.tiles || []).filter(tile => tile !== stale);
    }
    this._appendThumbs(el, [post], type);
    if (s.missing) s.missing = s.missing.filter(x => Number(x) !== num);
  } catch {
    // сеть по-прежнему недоступна — заглушка с кнопкой остаётся на месте
  } finally {
    s.retrying.delete(num);
  }
};
// ── Автопроверка плиток профиля ───────────────────────────────────────────
// Заглушки «недоступен» и «не удалось проверить» берутся из двух разных ответов
// источника, и у обеих был общий недостаток: починить их можно было только
// вручную — кнопкой на каждой плитке (и то не у «недоступен») либо перезагрузкой
// страницы. При неработающей сети это десятки нажатий на каждый пост.
//
// Здесь непроверенные id переспрашиваются автоматически и разом: пачками по
// _thumbsBatch, то есть один HTTP-запрос на 100 постов вместо сотни запросов по
// одному. Триггеры: отрисовка вкладки профиля, событие online, возврат фокуса и
// таймер, пока есть что проверять.
//
// Два принципиальных ограничения:
//  * backoff — без него приложение само долбит мёртвый API; интервал между
//    попытками растёт от THUMB_RECHECK_MIN до THUMB_RECHECK_MAX;
//  * лимит попыток для «недоступен» — источник ответил «поста нет», но такой
//    вердикт уже бывал ложным (сайт проигнорировал список id, см.
//    TestAdversarialPostsByIDsIgnoredIDListIsUnresolved). Такой id
//    переспрашивается THUMB_MISSING_TRIES раз и только потом признаётся
//    удалённым: держать его в очереди навечно — значит опрашивать источник из-за
//    постов, которых уже нет.
const THUMB_RECHECK_KEY = 'briefly_thumb_recheck';
const THUMB_RECHECK_MIN = 30 * 1000;
const THUMB_RECHECK_MAX = 5 * 60 * 1000;
const THUMB_MISSING_TRIES = 3;

App._thumbRecheck = App._thumbRecheck || { likes: {}, hides: {}, timer: 0 };

// Состояние живёт в localStorage: незакрытая очередь должна пережить
// перезагрузку страницы, иначе каждое открытие профиля снова опрашивало бы всё
// подряд. Записи: { <id>: { t: <ms последней попытки>, n: <попытки> } }.
App._loadThumbRecheck = function () {
  let raw = null;
  try { raw = localStorage.getItem(THUMB_RECHECK_KEY); } catch { /* приватный режим */ }
  let data = null;
  try { data = raw ? JSON.parse(raw) : null; } catch { data = null; }
  for (const type of ['likes', 'hides']) {
    const src = data && data[type];
    this._thumbRecheck[type] = src && typeof src === 'object' ? src : {};
  }
};

App._saveThumbRecheck = function () {
  try {
    localStorage.setItem(THUMB_RECHECK_KEY, JSON.stringify({
      likes: this._thumbRecheck.likes, hides: this._thumbRecheck.hides,
    }));
  } catch { /* переполнено или приватный режим — не критично */ }
};

// id, которые ещё не удалось подтвердить, по каждому виду.
App._pendingThumbIds = function (type) {
  const bag = this._thumbRecheck && this._thumbRecheck[type];
  return bag ? Object.keys(bag).map(Number).filter(n => Number.isFinite(n)) : [];
};

// Кладёт id в очередь перепроверки. isMissing=true — источник ответил «поста
// нет»: такой id переспрашиваем ограниченное число раз, иначе он ушёл бы в
// бесконечную очередь.
App._queueThumbRecheck = function (type, ids, isMissing) {
  if (!ids || !ids.length) return;
  const bag = this._thumbRecheck[type] || (this._thumbRecheck[type] = {});
  let changed = false;
  for (const raw of ids) {
    const id = Number(raw);
    if (!Number.isFinite(id)) continue;
    const prev = bag[id];
    if (prev) { prev.n = (prev.n || 0) + (isMissing ? 1 : 0); changed = true; continue; }
    bag[id] = { t: 0, n: isMissing ? 1 : 0 };
    changed = true;
  }
  if (changed) this._saveThumbRecheck();
};

App._clearThumbRecheck = function (type, id) {
  const bag = this._thumbRecheck && this._thumbRecheck[type];
  if (bag && id != null && bag[id] != null) {
    delete bag[id];
    this._saveThumbRecheck();
  }
};

// Массовый переспрос: забирает пачками по _thumbsBatch и подменяет заглушки
// настоящими плитками НА СВОЁМ МЕСТЕ, чтобы сетка не пересобиралась и порядок
// не прыгал. Ручной вызов (force) игнорирует backoff.
App.recheckThumbs = async function (type, opts) {
  const force = !!(opts && opts.force);
  const s = this._thumbs && this._thumbs[type];
  if (!s || !this._thumbRecheck) return 0;
  const ids = this._pendingThumbIds(type);
  if (!ids.length) { this._stopThumbRecheckTimer(); return 0; }

  if (!force) {
    const wait = this._thumbRecheckWait(type);
    if (wait > 0) return 0;
  }
  if (s.rechecking) return 0;
  s.rechecking = true;

  const bag = this._thumbRecheck[type];
  const now = Date.now();
  let healed = 0;
  try {
    for (let start = 0; start < ids.length; start += this._thumbsBatch) {
      const batch = ids.slice(start, start + this._thumbsBatch);
      let data = null;
      try {
        data = await API.get(`/posts-by-ids?ids=${batch.join(',')}`);
      } catch {
        // Сеть/источник недоступны: сдвигаем время последней попытки, чтобы
        // backoff рос, и оставляем очередь как есть — попробуем позже.
        for (const id of batch) { const e = bag[id]; if (e) e.t = now; }
        break;
      }
      const posts = (data && data.posts) || [];
      const unresolved = new Set((data && data.unresolved) || []);
      const found = new Set(posts.map(p => p && p.id).filter(Number.isFinite));
      const el = type === 'likes' ? this.els.likesList : this.els.hidesList;

      for (const p of posts) {
        if (this._replaceThumbPlaceholder(type, p, el)) healed++;
        delete bag[p.id];
      }
      for (const id of batch) {
        const e = bag[id];
        if (!e) continue;
        e.t = now;
        if (found.has(id)) continue;
        if (unresolved.has(id)) { e.n = e.n || 0; continue; }
        // Источник ответил и пост не отдал — это не «не удалось проверить»,
        // а отказ подтвердить пост. Счётчик попыток растёт здесь, а не только
        // при первичной пометке: иначе «недоступен» так и не исчерпает лимит
        // и очередь будет опрашивать источник до бесконечности.
        const tries = (e.n || 0) + 1;
        e.n = tries;
        if (tries >= THUMB_MISSING_TRIES) delete bag[id];
      }
    }
    this._saveThumbRecheck();
  } finally {
    s.rechecking = false;
    this._applyThumbOpts(type);
    this._syncThumbRecheckBar(type);
    if (this._pendingThumbIds(type).length) this._armThumbRecheckTimer(); else this._stopThumbRecheckTimer();
  }
  return healed;
};

// Подменяет заглушку (missing/unresolved) плиткой поста — на её же месте.
App._replaceThumbPlaceholder = function (type, post, el) {
  const s = this._thumbs && this._thumbs[type];
  if (!s || !post || post.id == null) return false;
  const id = String(post.id);
  const stale = (s.tiles || []).find(tile => tile.dataset && tile.dataset.pfId === id
    && tile._pfPost && (tile._pfPost.missing || tile._pfPost.unresolved));
  if (!stale) return false;
  const tile = this._buildThumbTile(post, 0, type);
  if (el && stale.parentNode) el.insertBefore(tile, stale); else if (el) el.appendChild(tile);
  stale.remove();
  s.tiles = (s.tiles || []).filter(x => x !== stale).concat([tile]);
  s.posts = (s.posts || []).filter(p => Number(p.id) !== Number(post.id)).concat([post]);
  if (s.missing) s.missing = s.missing.filter(x => Number(x) !== Number(post.id));
  if (s.unresolved) s.unresolved = s.unresolved.filter(x => Number(x) !== Number(post.id));
  return true;
};

// Сколько ещё ждать до следующей автопопытки: чем больше подряд неудач, тем
// реже. Ручная кнопка и событие online ждут не ждут.
App._thumbRecheckWait = function (type) {
  const bag = this._thumbRecheck && this._thumbRecheck[type];
  if (!bag) return 0;
  const entries = Object.values(bag);
  if (!entries.length) return 0;
  const since = entries.map(e => Date.now() - ((e && e.t) || 0));
  const last = Math.min(...since);
  const n = Math.max(0, ...entries.map(e => (e && e.n) || 0));
  const step = Math.min(THUMB_RECHECK_MAX, THUMB_RECHECK_MIN * Math.pow(2, Math.min(n, 4)));
  return Math.max(0, step - last);
};

// Панель автоперепроверки: видно, сколько постов ждёт подтверждения, и есть
// кнопка «проверить сейчас» для тех, кому не хочется ждать таймер. Прячется,
// когда очередь пуста, — вкладка не должна кричать о внутренних делах.
App._syncThumbRecheckBar = function (type) {
  const box = document.getElementById(type === 'likes' ? 'likes-recheck' : 'hides-recheck');
  if (!box || !this._thumbRecheck) return;
  const n = this._pendingThumbIds(type).length;
  box.hidden = n === 0;
  const label = box.querySelector('.pf-recheck-count');
  if (label) label.textContent = tf('pf.recheckCount', { n });
  const btn = box.querySelector('[data-recheck-thumbs]');
  if (btn) {
    btn.disabled = !!((this._thumbs && this._thumbs[type] || {}).rechecking);
    btn.textContent = this._thumbs && this._thumbs[type] && this._thumbs[type].rechecking
      ? t('pf.rechecking') : t('pf.recheckNow');
  }
};

// Глобальные триггеры. Сеть вернулась (online) — самое время переспросить всё
// накопленное, поэтому здесь force. Возврат фокуса к вкладке — обычный
// автоцикл с backoff. Всё это ленивое: подписка происходит один раз при
// загрузке скрипта, а сама перепроверка — только если очередь не пуста.
App.initThumbRecheck = function () {
  if (this._thumbRecheckInit || typeof window === 'undefined' || !window.addEventListener) return;
  this._thumbRecheckInit = true;
  this._loadThumbRecheck();
  window.addEventListener('online', () => this._kickThumbRecheck());
  if (typeof document !== 'undefined' && document.addEventListener) {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') {
        for (const type of ['likes', 'hides']) this.recheckThumbs(type);
      }
    });
  }
  for (const type of ['likes', 'hides']) {
    const btn = document.querySelector(`#${type === 'likes' ? 'likes' : 'hides'}-recheck [data-recheck-thumbs]`);
    if (btn) btn.addEventListener('click', () => this.recheckThumbs(type, { force: true }));
  }
};

// Планировщик: держим таймер только пока есть очередь, чтобы пустой таймер не
// жил вкладку зря. В тестах window.setInterval отсутствует — проверка на это же
// не даёт постороннему коду поднять фоновый таймер.
App._armThumbRecheckTimer = function () {
  if (this._thumbRecheck.timer || typeof window === 'undefined') return;
  if (typeof window.setInterval !== 'function') return;
  this._thumbRecheck.timer = window.setInterval(() => {
    if (typeof document !== 'undefined' && document.hidden) return;
    for (const type of ['likes', 'hides']) this.recheckThumbs(type);
  }, THUMB_RECHECK_MIN);
};

App._stopThumbRecheckTimer = function () {
  if (!this._thumbRecheck.timer || typeof window === 'undefined') return;
  if (typeof window.clearInterval === 'function') window.clearInterval(this._thumbRecheck.timer);
  this._thumbRecheck.timer = 0;
};

// Точка входа для событий браузера: переспрашиваем обе вкладки сразу и без
// backoff — смысл события «сеть вернулась» именно в том, чтобы дождаться её.
App._kickThumbRecheck = function () {
  for (const type of ['likes', 'hides']) this.recheckThumbs(type, { force: true });
};

App._appendThumbs = function (el, posts, type) {
  if (!el) return;
  const s = this._thumbs && this._thumbs[type];
  const frag = document.createDocumentFragment();
  posts.forEach((post, i) => {
    if (!post || post.id == null) return;
    const tile = this._buildThumbTile(post, i, type);
    if (s && s.tiles) s.tiles.push(tile);
    frag.appendChild(tile);
  });
  el.appendChild(frag);
  if (s) this._applyThumbOpts(type);
};

App._thumbSel = { likes: new Set(), hides: new Set() };
App._thumbSelMode = { likes: false, hides: false };

// ── Быстрые действия на плитке ────────────────────────────────────────────
// Лайк/скрытие идут через оптимистичные хелперы ленты: список профиля меняется
// сразу, при ошибке запроса состояние откатывается.
App.onThumbAction = function (type, post, act, tile) {
  const id = post && post.id;
  if (id == null || !act) return;
  if (act === 'select') { this.toggleThumbSelect(type, id, tile); return; }
  if (act === 'download') {
    if (typeof this.downloadPost !== 'function') return;
    this.downloadPost(post);
    this.showToast(t('pf.queuedOne'));
    return;
  }
  const fail = (err) => this.showToast(
    err && err.message ? tf('err.withMsg', { msg: err.message }) : t('err.generic'), 'error');
  if (act === 'like') {
    // Во вкладке «Лайки» кнопка снимает отметку, в «Скрытых» — ставит.
    const liked = this.optimisticLike(id, type !== 'likes');
    this.showToast(liked ? t('pf.likedBack') : t('pf.unliked'));
    // Явное состояние, а не toggle: запрос может попасть в оффлайн-очередь
    // и переиграться при восстановлении связи (типично для мобильных) —
    // переключение вернуло бы лайк обратно.
    API.post(`/like/${id}`, { liked }).then(() => {
      API.invalidate('/profile');
      if (!liked) this._removeThumb(type, id);
    }).catch(err => { this.optimisticLike(id, !liked); fail(err); });
    return;
  }
  if (act === 'hide') {
    // Во вкладке «Скрытые» кнопка возвращает пост, в «Лайках» — скрывает.
    const hidden = this.optimisticHide(id, type !== 'hides');
    this.showToast(hidden ? t('pf.hiddenNow') : t('pf.unhidden'));
    API.post(`/hide/${id}`, { hidden }).then(() => {
      API.invalidate('/profile');
      if (type === 'hides' && !hidden) this._removeThumb(type, id);
    }).catch(err => { this.optimisticHide(id, !hidden); fail(err); });
  }
};

// Плитка уходит из вкладки, когда отметка снята: id выбрасывается и из кэша
// вкладки, и из s.ids — иначе следующий renderThumbs вернул бы её обратно.
App._removeThumb = function (type, id) {
  const s = this._thumbs && this._thumbs[type];
  if (!s) return;
  const num = Number(id);
  const idOf = tile => Number(tile && tile.dataset && tile.dataset.pfId);
  if (s.tiles) {
    s.tiles.filter(tile => idOf(tile) === num).forEach(tile => { if (tile.remove) tile.remove(); });
    s.tiles = s.tiles.filter(tile => idOf(tile) !== num);
  }
  if (s.ids) s.ids = s.ids.filter(x => Number(x) !== num);
  if (s.posts) s.posts = s.posts.filter(p => Number(p.id) !== num);
  if (s.missing) s.missing = s.missing.filter(x => Number(x) !== num);
  const set = this._thumbSel && this._thumbSel[type];
  if (set) set.delete(num);
  this._updateThumbCount(type);
  this._syncThumbSelBar(type);
};

// ── Мульти-выбор ─────────────────────────────────────────────────────────
App.toggleThumbSelect = function (type, id, tile) {
  const set = this._thumbSel[type] || (this._thumbSel[type] = new Set());
  const num = Number(id);
  if (set.has(num)) set.delete(num); else set.add(num);
  if (tile && tile.classList) tile.classList.toggle('pf-checked', set.has(num));
  this._syncThumbSelBar(type);
};

App.setThumbSelectMode = function (type, on) {
  this._thumbSelMode[type] = !!on;
  const set = this._thumbSel[type] || (this._thumbSel[type] = new Set());
  if (!this._thumbSelMode[type]) set.clear();
  const el = type === 'likes' ? this.els.likesList : this.els.hidesList;
  if (el && el.classList) el.classList.toggle('pf-selecting', this._thumbSelMode[type]);
  const s = this._thumbs && this._thumbs[type];
  if (s && s.tiles) s.tiles.forEach(tile => {
    if (tile.classList) tile.classList.toggle('pf-checked', set.has(Number(tile.dataset && tile.dataset.pfId)));
  });
  this._syncThumbSelBar(type);
};

// «Выбрать все» берёт только видимые плитки: под активным фильтром скрытые
// недоступны для выделения, иначе действие «снять лайк» затронет невидимое.
App.selectAllThumbs = function (type, on) {
  const s = this._thumbs && this._thumbs[type];
  if (!s) return;
  const set = this._thumbSel[type] || (this._thumbSel[type] = new Set());
  s.tiles.forEach(tile => {
    if (tile.hidden) return;
    const id = Number(tile.dataset && tile.dataset.pfId);
    if (on) set.add(id); else set.delete(id);
    if (tile.classList) tile.classList.toggle('pf-checked', on);
  });
  this._syncThumbSelBar(type);
};

App._syncThumbSelBar = function (type) {
  const bar = document.getElementById(type === 'likes' ? 'likes-selbar' : 'hides-selbar');
  const n = ((this._thumbSel && this._thumbSel[type]) || new Set()).size;
  const on = !!(this._thumbSelMode && this._thumbSelMode[type]);
  if (bar) {
    bar.hidden = !on;
    const count = bar.querySelector('.pf-selbar-count');
    if (count) count.textContent = tf('batch.selected', { n });
    bar.querySelectorAll('[data-selact]').forEach(b => { b.disabled = n === 0; });
  }
  const tools = document.getElementById(type === 'likes' ? 'likes-tools' : 'hides-tools');
  const btn = tools && tools.querySelector('[data-act="selectMode"]');
  if (btn) {
    btn.classList.toggle('active', on);
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
    const lbl = btn.querySelector('.pf-tool-label');
    if (lbl) lbl.textContent = on ? t('pf.selectDone') : t('pf.select');
  }
};

// Действия над выделенными плитками: скачать, снять лайк, вернуть из скрытых,
// убрать недоступные из списка.
App.thumbSelAction = async function (type, act) {
  const s = this._thumbs && this._thumbs[type];
  if (!s) return;
  const set = this._thumbSel[type] || (this._thumbSel[type] = new Set());
  if (act === 'clearUnavailable') {
    const gone = (s.tiles || []).filter(tile => tile._pfPost && tile._pfPost.missing)
      .map(tile => Number(tile.dataset && tile.dataset.pfId)).filter(Number.isFinite);
    if (!gone.length) return;
    const ok = typeof this.confirmDialog === 'function'
      ? await this.confirmDialog({
        message: tf('pf.clearUnavailableConfirm', { n: gone.length }),
        okText: t('confirm.ok'),
        danger: true,
      })
      : true;
    if (!ok) return;
    gone.forEach(id => this._removeThumb(type, id));
    this._updateThumbMore(type);
    this.showToast(tf('pf.unavailableRemoved', { n: gone.length }));
    return;
  }
  const ids = Array.from(set).map(Number).filter(Number.isFinite);
  if (!ids.length) return;
  if (act === 'download') {
    const posts = (s.posts || []).filter(p => ids.includes(Number(p.id)));
    posts.forEach(p => this.downloadPost(p));
    if (posts.length) this.showToast(tf('pf.queuedN', { n: posts.length }));
    return;
  }
  if (act !== 'unlike' && act !== 'unhide') return;
  const like = act === 'unlike';
  set.clear();
  this._syncThumbSelBar(type);
  // Запросы параллельно: список бывает на сотни постов, последовательные POST
  // дали бы заметную паузу. Отметки снимаем сразу (оптимистично), неудачные
  // возвращаем в выделение, чтобы пользователь их увидел.
  const results = await Promise.all(ids.map(id =>
    (like ? API.post(`/like/${id}`, { liked: false }) : API.post(`/hide/${id}`, { hidden: false }))
      .then(() => true).catch(() => false)));
  let done = 0;
  results.forEach((ok, i) => {
    if (!ok) { set.add(ids[i]); return; }
    if (like) this.optimisticLike(ids[i], false); else this.optimisticHide(ids[i], false);
    this._removeThumb(type, ids[i]);
    done++;
  });
  API.invalidate('/profile');
  this._syncThumbSelBar(type);
  if (done) this.showToast(like ? t('pf.unliked') : t('pf.unhidden'));
};

App.openPostFromProfile = async function (post) {
  if (!post || post.id == null) return;
  const profile = this.state.profile || {};
  const likes = profile.liked_posts || [];
  const hides = profile.hidden_posts || [];
  const type = likes.includes(post.id) ? 'likes' : hides.includes(post.id) ? 'hides' : null;
  const list = type === 'likes' ? likes : type === 'hides' ? hides : null;
  if (!list || list.length === 0) {
    this._enterContext([post], { related: false });
    return;
  }
  const cache = (this._thumbs && this._thumbs[type]) || null;
  const have = new Map((cache ? (cache.posts || []) : []).map(p => [p.id, p]));
  const missing = list.filter(id => !have.has(id));
  let fetched = [];
  if (missing.length) {
    try {
      const data = await API.get(`/posts-by-ids?ids=${missing.join(',')}`);
      fetched = (data && data.posts) || [];
    } catch {  }
  }
  const byId = new Map(fetched.map(p => [p.id, p]));
  const ordered = list.map(id => have.get(id) || byId.get(id)).filter(Boolean);
  if (!ordered.length) { this._enterContext([post], { related: false }); return; }
  const idx = Math.max(0, ordered.findIndex(p => p.id === post.id));
  this._enterContext(ordered, { related: false, index: idx });
};

App._matchTag = function (tag) {
  const q = this._tagFilter || '';
  const idx = q ? tag.toLowerCase().indexOf(q.toLowerCase()) : -1;
  if (idx < 0) return esc(tag);
  return esc(tag.slice(0, idx)) + '<mark>' + esc(tag.slice(idx, idx + q.length)) + '</mark>' + esc(tag.slice(idx + q.length));
};

App.renderTagList = function (elId, tags, type) {
  const el = this.els[elId];
  if (!el) return;
  el.innerHTML = '';
  const q = this._tagFilter || '';
  if (q) tags = (tags || []).filter(tag => tag.toLowerCase().includes(q));
  if (!tags || !tags.length) {
    el.innerHTML = `<p class="profile-empty">${esc(t('tags.none'))}</p>`;
    return;
  }
  const isFav = type === 'fav';
  const activeCls = isFav ? 'fav' : 'hidden';
  tags.forEach(tag => {
    const d = document.createElement('div');
    d.className = 'profile-tag-item ' + activeCls;
    d.title = tag;
    let count = '';
    if (this._tagCounts && this._tagCounts[tag]) count = `<span class="tg-count">${this._tagCounts[tag]}</span>`;
    const ico = isFav ? icon('bookmark', 11) : icon('eye', 11);
    d.innerHTML = `${ico}<span>${this._matchTag(tag)}</span>${count}`;
    const btn = document.createElement('button');
    btn.className = isFav ? 'tg-unfav' : 'tg-unhide';
    btn.innerHTML = icon('x', 11);
    btn.title = isFav ? t('tags.unfav') : t('tags.unhide');
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      API.post(`/${isFav ? 'fav-tag' : 'hidden-tag'}`, { tag }).then(() => {
        API.invalidate('/profile');
        if (!isFav) { this.invalidateFeedCache(); this.loadPosts(true); }
        this.loadProfile();
      }).catch(() => {});
    });
    d.appendChild(btn);
    if (isFav) {
      d.title = tf('tags.search', { tag });
      d.addEventListener('click', (ev) => {
        if (ev.target.closest('button')) return;
        this.setSearchValue(tag);
        this.search(tag);
        this.toggleProfile();
      });
    } else {
      d.title = tf('tags.searchAvoid', { tag });
      d.addEventListener('click', (ev) => {
        if (ev.target.closest('button')) return;
        const cur = this.state.query;
        const newQ = cur ? `${cur} -${tag}` : `-${tag}`;
        this.setSearchValue(newQ);
        this.search(newQ);
        this.toggleProfile();
      });
    }
    el.appendChild(d);
  });
};

App.clearTagList = async function (type) {
  const key = type === 'fav' ? 'fav_tags' : 'hidden_tags';
  const label = type === 'fav' ? t('tags.favLabel') : t('tags.hiddenLabel');
  const tags = (this.state.profile && (this.state.profile[key] || [])) || [];
  if (!tags.length) {
    this.showToast(t('tags.nothingToClear'), 'error');
    return;
  }
  const ok = await this.confirmDialog({
    title: t('tags.clearTitle'),
    message: tf('tags.clearConfirm', { label, n: tags.length }),
    okText: t('btn.delete'),
    danger: true,
  });
  if (!ok) return;
  try {
    await API.post(type === 'fav' ? '/fav-tags/clear' : '/hidden-tags/clear');
    API.invalidate('/profile');
    if (type === 'hidden') this.invalidateFeedCache();
    await this.loadProfile();
    if (type === 'hidden') this.loadPosts(true);
    this.showToast(tf('tags.cleared', { n: tags.length, label }));
  } catch (err) {
    this.showToast(tf('err.withMsg', { msg: err.message }), 'error');
  }
};

App.savePreset = function () {
  const name = this.els.presetName.value.trim();
  if (!name) return;
  const query = this.state.query || '';
  if (!query) { this.showToast(t('preset.needSearch'), 'error'); return; }
  API.post('/preset', { name, query }).then(() => {
    this.els.presetName.value = '';
    API.invalidate('/profile');
    this.loadProfile();
    this.showToast(tf('preset.saved', { name }));
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.saveTagPreset = function (kind) {
  const p = this.state.profile || {};
  const fav = p.fav_tags || [];
  const hidden = p.hidden_tags || [];
  const list = kind === 'fav' ? fav : hidden;
  if (!list.length) {
    this.showToast(kind === 'fav' ? t('tags.noFav') : t('tags.noHidden'), 'error');
    return;
  }
  const label = kind === 'fav' ? t('tags.favHead') : t('tags.hiddenHead');
  const name = `${label} (${list.length})`;
  API.post('/preset', { name, kind: 'tags', hidden_tags: hidden, fav_tags: fav }).then(() => {
    API.invalidate('/profile');
    this.loadProfile();
    this.showToast(tf('preset.saved', { name }));
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.applyPreset = function (pr) {
  if ((pr.kind || 'query') !== 'query') return;
  this.setSearchValue(pr.query || '');
  this.search(pr.query || '');
};

App.applyPresetById = function (id, type) {
  if (type !== 'fav' && type !== 'hidden') return;
  API.post(`/preset/${id}/apply`, { type }).then(r => {
    API.invalidate('/profile');
    if (type === 'hidden') { this.invalidateFeedCache(); }
    this.loadProfile();
    if (type === 'hidden') this.loadPosts(true);
    const label = type === 'fav' ? 'tags.appliedFav' : 'tags.appliedHidden';
    this.showToast(r.applied > 0 ? tf(label, { n: r.applied }) : t('tags.setCleared'));
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.addTag = function (type) {
  const input = type === 'fav' ? this.els.favTagInput : this.els.hiddenTagInput;
  const tag = input.value.trim().toLowerCase().replace(/\s+/g, '_');
  if (!tag) return;
  const endpoint = type === 'fav' ? '/fav-tag' : '/hidden-tag';
  API.post(endpoint, { tag }).then(r => {
    input.value = '';
    API.invalidate('/profile');
    if (type === 'hidden') this.invalidateFeedCache();
    this.loadProfile();
    this.showToast(r.liked || r.hidden ? tf('tags.added', { tag }) : tf('tags.removed', { tag }));
    this.loadPosts(true);
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.exportPresets = function () {
  const a = document.createElement('a');
  a.href = '/api/presets/export';
  a.download = 'briefly-presets.json';
  a.click();
  this.showToast(t('preset.exported'));
};

App.importPresets = async function (ev) {
  const file = ev.target.files[0];
  if (!file) return;
  try {
    const text = await file.text();
    const data = JSON.parse(text);
    if (!data.presets || !Array.isArray(data.presets)) { throw new Error(t('err.badFormat')); }
    const r = await API.post('/presets/import', { presets: data.presets });
    this.loadProfile();
    this.showToast(tf('preset.imported', { n: r.imported }));
  } catch (err) { this.showToast(tf('err.withMsg', { msg: err.message }), 'error'); }
  ev.target.value = '';
};

App.toggleProfile = function () {
  const opening = !this.state.profileOpen;
  if (opening && !this.state.settingsOpen) this._panelReturnFocus = document.activeElement;
  const returnFocus = this._panelReturnFocus;
  this.state.profileOpen = opening;
  if (opening && this.state.settingsOpen) {
    this.state.settingsOpen = false;
    this.setPanelOpen(this.els.settingsPanel, false);
  }
  this.setPanelOpen(this.els.profilePanel, opening, opening ? null : returnFocus);
  this._syncPanels();
  if (opening) {
    this.applyPanelWidth();
    this.loadProfile();
  }
};

App.randomPost = async function () {
  try {
    const data = await API.get('/random');
    const p = (data && data.posts && data.posts[0]);
    if (!p) { this.showToast(t('pf.noRandom'), 'error'); return; }
    this._enterContext([{ ...p, _index: 0 }], { related: false });
  } catch (err) { this.showToast(tf('err.withMsg', { msg: err.message }), 'error'); }
};

App.renderPresetMenu = function () {
  const list = this.els.presetMenuList;
  if (!list) return;
  const presets = ((this.state.profile && this.state.profile.presets) || [])
    .filter(pr => (pr.kind || 'query') === 'query');
  if (!presets.length) {
    list.innerHTML = `<div class="preset-menu-empty">${esc(t('preset.none'))}</div>`;
    return;
  }
  const cur = this.state.query || '';
  list.innerHTML = presets.map(pr =>
    `<button class="preset-menu-item${(pr.query || '') === cur ? ' active' : ''}" data-query="${esc(pr.query || '')}"><span class="pm-name">${esc(pr.name)}</span><span class="pm-query">${esc(pr.query || t('preset.main'))}</span></button>`
  ).join('');
};

App.downloadAllLikes = async function () {
  const ids = (this.state.profile && this.state.profile.liked_posts) || [];
  if (!ids.length) { this.showToast(t('pf.noLikes'), 'error'); return; }
  const btn = this.els.btnLikesDownload;
  if (btn) { btn.disabled = true; btn.textContent = t('pf.queuing'); }
  try {
    const r = await API.post('/download-liked');
    this.showToast(tf('pf.queued', { done: r.queued, total: r.total }), 'success');
    if (btn) btn.textContent = t('btn.downloadLikes');
  } catch (err) {
    this.showToast(tf('err.withMsg', { msg: err.message }), 'error');
    if (btn) btn.textContent = t('btn.downloadLikes');
  }
  if (btn) btn.disabled = false;
};