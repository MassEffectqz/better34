import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
App._thumbs = App._thumbs || {};

const pfProxy = u => `/api/proxy?url=${encodeURIComponent(u)}`;

const pfThumbFor = post => {
  if (post.downloaded) {
    return (post.file_type === 'video' && post.preview_url)
      ? pfProxy(post.preview_url)
      : `/api/thumb/${post.id}`;
  }
  return post.preview_url ? pfProxy(post.preview_url) : `/api/thumb/${post.id}`;
};

App.renderProfile = function () {
  const p = this.state.profile;
  this.els.profileAvatarImg.src = p.avatar || '';
  this.els.profileAvatarImg.style.display = p.avatar ? 'block' : 'none';
  this.els.profileNickname.value = p.nickname || '';
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
    el.innerHTML = '<option value="">Станд. (текущие)</option>' + presets.map(pr => {
      const lists = this._tagPresetLists(pr);
      const n = type === 'fav' ? lists.fav.length : lists.hidden.length;
      return `<option value="${esc(pr.id)}">${esc(pr.name)} — ${n} ${plural(n, 'тег', 'тега', 'тегов')}</option>`;
    }).join('');
    el.onchange = () => {
      const id = el.value;
      el.value = '';
      if (id) this.applyPresetById(id, type);
    };
  });
};

App.onTagFilter = function () {
  this._tagFilter = (this.els.tagFilter.value || '').toLowerCase().trim();
  this.renderTagLists();
  this._updateTagFilterCount();
};

App._updateTagFilterCount = function () {
  const el = this.els.tagFilterCount;
  if (!el) return;
  const p = this.state.profile || {};
  const q = this._tagFilter || '';
  const fav = (p.fav_tags || []).filter(t => !q || t.toLowerCase().includes(q));
  const hid = (p.hidden_tags || []).filter(t => !q || t.toLowerCase().includes(q));
  const total = (p.fav_tags || []).length + (p.hidden_tags || []).length;
  const shown = fav.length + hid.length;
  el.textContent = total ? (q ? `${shown} из ${total}` : `${total}`) : '';
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
  setNum('st-presets', presetN); setNum('tb-presets', presetN);
  setNum('st-likes', likeN); setNum('tb-likes', likeN);
  setNum('st-hides', hideN); setNum('tb-hides', hideN);
  setNum('st-tags', tagN); setNum('tb-tags', tagN);
  const tbCol = document.getElementById('tb-collections');
  if (tbCol) tbCol.textContent = colN;
};

App.renderPresets = function (presets) {
  const pl = this.els.presetsList;
  pl.innerHTML = '';
  const cur = this.state.query || '';
  const hint = this.els.presetCurrent;
  if (hint) {
    hint.innerHTML = cur
      ? `Сохраняется текущий поиск: <b>${esc(cur)}</b>`
      : 'Установите поиск и сохраните его как пресет';
  }
  presets = (presets || []).filter(pr => (pr.kind || 'query') === 'query');
  if (!presets.length) {
    pl.innerHTML = '<p class="profile-empty">Нет пресетов поиска. Наборы тегов сохраняются и применяются во вкладке «Теги»</p>';
    return;
  }
  presets.forEach((pr, i) => {
    const active = (pr.query || '') === cur;
    const d = document.createElement('div');
    d.className = 'preset-item' + (active ? ' active' : '');
    d.innerHTML = `
      <span class="preset-kind preset-kind-query">${icon('search', 13)}</span>
      <span class="preset-name">${esc(pr.name)}</span>
      <span class="preset-query">${esc(pr.query || 'главная')}</span>
      <button class="btn-icon btn-icon-sm pf-up" title="Переместить выше">${icon('arrowUp', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-down" title="Переместить ниже">${icon('arrowDown', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-edit" title="Переименовать">${icon('pencil', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-del" title="Удалить пресет">${icon('x', 14)}</button>`;
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
        title: 'Удалить пресет',
        message: `Удалить пресет <b>«${esc(pr.name)}»</b>?`,
        okText: 'Удалить',
        danger: true,
      });
      if (!ok) return;
      API.del(`/preset/${pr.id}`).then(() => { API.invalidate('/profile'); this.loadProfile(); this.showToast(`Пресет «${pr.name}» удалён`); }).catch(() => {});
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
        this.showToast('Пресет переименован');
      }).catch(err => {
        this.showToast(`Ошибка: ${err.message}`, 'error');
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

App.renderThumbs = function (type, ids) {
  if (!this._thumbs) this._thumbs = {};
  const el = this.els[type === 'likes' ? 'likesList' : 'hidesList'];
  const empty = this.els[type === 'likes' ? 'likesEmpty' : 'hidesEmpty'];
  const gridBtn = type === 'likes' ? this.els.btnLikesGrid : this.els.btnHidesGrid;
  const dlBtn = type === 'likes' ? this.els.btnLikesDownload : null;
  const list = ids || [];

  if (dlBtn) dlBtn.style.display = list.length ? '' : 'none';

  if (!list.length) {
    el.innerHTML = '';
    empty.style.display = '';
    gridBtn.style.display = 'none';
    this._hideThumbMore(type);
    this._thumbs[type] = { key: '', ids: [], posts: [], loaded: 0, token: 0 };
    return;
  }
  empty.style.display = 'none';
  gridBtn.style.display = '';

  const key = list.join(',');
  const tabEl = document.getElementById(type === 'likes' ? 'tab-likes' : 'tab-hides');
  const tabActive = !tabEl || tabEl.classList.contains('active');
  const ex = this._thumbs[type];

  if (ex && ex.key === key) {
    if (ex.loaded > 0 && tabActive) {
      el.innerHTML = '';
      this._appendThumbs(el, ex.posts);
      this._updateThumbMore(type);
      return;
    }
    if (!tabActive) { el.innerHTML = ''; return; }
  }

  this._thumbs[type] = { key, ids: list, posts: [], loaded: 0, token: 0 };
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
  if (moreBtn) { moreBtn.disabled = true; moreBtn.style.display = ''; moreBtn.innerHTML = '<span class="pf-more-spin"></span>Загрузка…'; }
  const next = Math.min(s.loaded + this._thumbsBatch, total);
  try {
    const data = await API.get(`/posts-by-ids?ids=${s.ids.slice(s.loaded, next).join(',')}`);
    if (s.token !== token) return;
    const posts = (data && data.posts) || [];
    posts.forEach(p => s.posts.push(p));
    s.loaded = next;
    const el = type === 'likes' ? this.els.likesList : this.els.hidesList;
    this._appendThumbs(el, posts);
    el.querySelectorAll('.pf-thumb-skeleton').forEach(s => s.remove());
    this._updateThumbMore(type);
  } catch (err) {
    if (s.token === token && moreBtn) { moreBtn.disabled = false; moreBtn.textContent = 'Ошибка — повторить'; }
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
  more.textContent = `Показать ещё (${left})`;
};

App._buildThumbTile = function (post, i) {
  const tile = document.createElement('div');
  tile.className = 'pf-thumb';
  tile.style.setProperty('--d', `${Math.min(i, 14) * 26}ms`);
  if (post.width && post.height) tile.style.aspectRatio = String(Math.min(post.width / post.height, 1.4));
  const src = pfThumbFor(post);
  const play = post.file_type === 'video'
    ? `<span class="pf-play">${icon('play', null, true)}</span>`
    : post.file_type === 'gif'
      ? '<span class="pf-gif">GIF</span>'
      : '';
  const dl = (post.downloadedAt || post.downloaded)
    ? `<span class="pf-dl" title="Скачано">${icon('check', null, true)}</span>`
    : '';
  const score = post.score ? `<span class="pf-score">${icon('star', null, true)}${post.score}</span>` : '';
  tile.innerHTML = `<img src="${src}" alt="" loading="lazy" decoding="async" onload="this.classList.add('loaded')"><div class="pf-fallback">#${post.id}</div>${play}${dl}<div class="pf-open"><span class="pf-open-icon">${icon('externalLink')}</span></div><div class="pf-bar"><span>#${post.id}</span>${score}</div>`;
  const img = tile.querySelector('img');
  if (img) img.addEventListener('error', () => { img.style.display = 'none'; }, { once: true });
  tile.addEventListener('click', () => this.openPostFromProfile(post));
  return tile;
};

App._appendThumbs = function (el, posts) {
  const frag = document.createDocumentFragment();
  posts.forEach((post, i) => {
    if (!post || post.id == null) return;
    frag.appendChild(this._buildThumbTile(post, i));
  });
  el.appendChild(frag);
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

App._matchTag = function (t) {
  const q = this._tagFilter || '';
  const idx = q ? t.toLowerCase().indexOf(q.toLowerCase()) : -1;
  if (idx < 0) return esc(t);
  return esc(t.slice(0, idx)) + '<mark>' + esc(t.slice(idx, idx + q.length)) + '</mark>' + esc(t.slice(idx + q.length));
};

App.renderTagList = function (elId, tags, type) {
  const el = this.els[elId];
  el.innerHTML = '';
  const q = this._tagFilter || '';
  if (q) tags = (tags || []).filter(t => t.toLowerCase().includes(q));
  if (!tags || !tags.length) {
    el.innerHTML = '<p class="profile-empty">Нет тегов</p>';
    return;
  }
  const isFav = type === 'fav';
  const activeCls = isFav ? 'fav' : 'hidden';
  tags.forEach(t => {
    const d = document.createElement('div');
    d.className = 'profile-tag-item ' + activeCls;
    d.title = t;
    let count = '';
    if (this._tagCounts && this._tagCounts[t]) count = `<span class="tg-count">${this._tagCounts[t]}</span>`;
    const ico = isFav ? icon('bookmark', 11) : icon('eye', 11);
    d.innerHTML = `${ico}<span>${this._matchTag(t)}</span>${count}`;
    const btn = document.createElement('button');
    btn.className = isFav ? 'tg-unfav' : 'tg-unhide';
    btn.innerHTML = icon('x', 11);
    btn.title = isFav ? 'Убрать из избранных' : 'Показать тег';
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      API.post(`/${isFav ? 'fav-tag' : 'hidden-tag'}`, { tag: t }).then(() => {
        API.invalidate('/profile');
        if (!isFav) { this.invalidateFeedCache(); this.loadPosts(true); }
        this.loadProfile();
      }).catch(() => {});
    });
    d.appendChild(btn);
    if (isFav) {
      d.title = `Поиск: ${t}`;
      d.addEventListener('click', (ev) => {
        if (ev.target.closest('button')) return;
        this.els.searchInput.value = t;
        this.search(t);
        this.toggleProfile();
      });
    } else {
      d.title = `Поиск без тега: -${t}`;
      d.addEventListener('click', (ev) => {
        if (ev.target.closest('button')) return;
        const cur = this.state.query;
        const newQ = cur ? `${cur} -${t}` : `-${t}`;
        this.els.searchInput.value = newQ;
        this.search(newQ);
        this.toggleProfile();
      });
    }
    el.appendChild(d);
  });
};

App.clearTagList = async function (type) {
  const key = type === 'fav' ? 'fav_tags' : 'hidden_tags';
  const label = type === 'fav' ? 'избранные' : 'скрытые';
  const tags = (this.state.profile && (this.state.profile[key] || [])) || [];
  if (!tags.length) {
    this.showToast('Нет тегов для очистки', 'error');
    return;
  }
  const ok = await this.confirmDialog({
    title: 'Очистка тегов',
    message: `Удалить все <b>${label}</b> теги (${tags.length})?`,
    okText: 'Удалить',
    danger: true,
  });
  if (!ok) return;
  try {
    await API.post(type === 'fav' ? '/fav-tags/clear' : '/hidden-tags/clear');
    API.invalidate('/profile');
    if (type === 'hidden') this.invalidateFeedCache();
    await this.loadProfile();
    if (type === 'hidden') this.loadPosts(true);
    this.showToast(`Удалено: ${tags.length} ${label} тегов`);
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
};

App.savePreset = function () {
  const name = this.els.presetName.value.trim();
  if (!name) return;
  const query = this.state.query || '';
  if (!query) { this.showToast('Сначала установите поиск', 'error'); return; }
  API.post('/preset', { name, query }).then(() => {
    this.els.presetName.value = '';
    API.invalidate('/profile');
    this.loadProfile();
    this.showToast(`Пресет «${name}» сохранён`);
  }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
};

App.saveTagPreset = function (kind) {
  const p = this.state.profile || {};
  const fav = p.fav_tags || [];
  const hidden = p.hidden_tags || [];
  const list = kind === 'fav' ? fav : hidden;
  if (!list.length) {
    this.showToast(kind === 'fav' ? 'Нет избранных тегов' : 'Нет скрытых тегов', 'error');
    return;
  }
  const label = kind === 'fav' ? 'Избранные' : 'Скрытые';
  const name = `${label} теги (${list.length})`;
  API.post('/preset', { name, kind: 'tags', hidden_tags: hidden, fav_tags: fav }).then(() => {
    API.invalidate('/profile');
    this.loadProfile();
    this.showToast(`Пресет «${name}» сохранён`);
  }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
};

App.applyPreset = function (pr) {
  if ((pr.kind || 'query') !== 'query') return;
  this.els.searchInput.value = pr.query || '';
  this.search(pr.query || '');
};

App.applyPresetById = function (id, type) {
  if (type !== 'fav' && type !== 'hidden') return;
  API.post(`/preset/${id}/apply`, { type }).then(r => {
    API.invalidate('/profile');
    if (type === 'hidden') { this.invalidateFeedCache(); }
    this.loadProfile();
    if (type === 'hidden') this.loadPosts(true);
    const label = type === 'fav' ? 'избранных' : 'скрытых';
    this.showToast(r.applied > 0 ? `Применено: ${r.applied} ${label} тегов` : 'Набор очищен');
  }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
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
    this.showToast(r.liked || r.hidden ? `Тег «${tag}» добавлен` : `Тег «${tag}» удалён`);
    this.loadPosts(true);
  }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
};

App.exportPresets = function () {
  const a = document.createElement('a');
  a.href = '/api/presets/export';
  a.download = 'briefly-presets.json';
  a.click();
  this.showToast('Пресеты экспортированы');
};

App.importPresets = async function (ev) {
  const file = ev.target.files[0];
  if (!file) return;
  try {
    const text = await file.text();
    const data = JSON.parse(text);
    if (!data.presets || !Array.isArray(data.presets)) { throw new Error('Неверный формат файла'); }
    const r = await API.post('/presets/import', { presets: data.presets });
    this.loadProfile();
    this.showToast(`Импортировано пресетов: ${r.imported}`);
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  ev.target.value = '';
};

App.bindProfileStats = function () {
  document.querySelectorAll('.stat[data-goto]').forEach(btn => {
    btn.addEventListener('click', () => {
      const tab = document.querySelector(`.profile-tab[data-tab="${btn.dataset.goto}"]`);
      if (tab) tab.click();
    });
  });
};

App.toggleProfile = function () {
  this.state.profileOpen = !this.state.profileOpen;
  if (this.state.profileOpen && this.state.settingsOpen) {
    this.state.settingsOpen = false;
    this.els.settingsPanel.classList.add('hidden');
  }
  this.els.profilePanel.classList.toggle('hidden', !this.state.profileOpen);
  this._syncPanels();
  if (this.state.profileOpen) {
    this.applyPanelWidth();
    this.loadProfile();
  }
};

App.randomPost = async function () {
  try {
    const data = await API.get('/random');
    const p = (data && data.posts && data.posts[0]);
    if (!p) { this.showToast('Случайный пост не найден', 'error'); return; }
    this._enterContext([{ ...p, _index: 0 }], { related: false });
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.renderPresetMenu = function () {
  const list = this.els.presetMenuList;
  if (!list) return;
  const presets = ((this.state.profile && this.state.profile.presets) || [])
    .filter(pr => (pr.kind || 'query') === 'query');
  if (!presets.length) {
    list.innerHTML = '<div class="preset-menu-empty">Нет пресетов поиска</div>';
    return;
  }
  const cur = this.state.query || '';
  list.innerHTML = presets.map(pr =>
    `<button class="preset-menu-item${(pr.query || '') === cur ? ' active' : ''}" data-query="${esc(pr.query || '')}"><span class="pm-name">${esc(pr.name)}</span><span class="pm-query">${esc(pr.query || 'главная')}</span></button>`
  ).join('');
};

App.downloadAllLikes = async function () {
  const ids = (this.state.profile && this.state.profile.liked_posts) || [];
  if (!ids.length) { this.showToast('Нет лайков', 'error'); return; }
  const btn = this.els.btnLikesDownload;
  if (btn) { btn.disabled = true; btn.textContent = 'Ставлю в очередь…'; }
  try {
    const r = await API.post('/download-liked');
    this.showToast(`В очередь: ${r.queued} из ${r.total} лайков`, 'success');
    if (btn) btn.textContent = 'Скачать все лайки';
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
    if (btn) btn.textContent = 'Скачать все лайки';
  }
  if (btn) btn.disabled = false;
};