import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
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
  setNum('st-collections', colN);
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
  const list = ids || [];


  if (!list.length) {
    el.innerHTML = '';
    if (empty) empty.style.display = '';
    if (gridBtn) gridBtn.style.display = 'none';
    this._hideThumbMore(type);
    this._thumbs[type] = { key: '', ids: [], posts: [], loaded: 0, token: 0, missing: [] };
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
      this._appendThumbs(el, ex.posts);
      if (ex.missing && ex.missing.length) this._appendMissingThumbs(el, ex.missing);
      this._updateThumbMore(type);
      return;
    }
    if (!tabActive) { el.innerHTML = ''; return; }
  }

  this._thumbs[type] = { key, ids: list, posts: [], loaded: 0, token: 0, missing: [] };
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
  const ids = s.ids.slice(s.loaded, next);
  try {
    const data = await API.get(`/posts-by-ids?ids=${ids.join(',')}`);
    if (s.token !== token) return;
    const posts = (data && data.posts) || [];
    const found = new Set(posts.map(p => p.id).filter(Number.isFinite));
    posts.forEach(p => s.posts.push(p));
    s.loaded = next;
    const el = type === 'likes' ? this.els.likesList : this.els.hidesList;
    this._appendThumbs(el, posts);
    el.querySelectorAll('.pf-thumb-skeleton').forEach(s => s.remove());
    // Старые лайки, которых уже нет ни в локальной БД, ни на источнике:
    // показываем заглушку «недоступен», чтобы было видно, что id учитывался.
    const already = new Set(s.missing || []);
    const missing = ids.filter(id => !found.has(id) && !already.has(id));
    if (missing.length) {
      s.missing = [...(s.missing || []), ...missing];
      this._appendMissingThumbs(el, missing);
    }
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
  const srcs = pfCandidatesFor(post);
  const play = post.file_type === 'video'
    ? `<span class="pf-play">${icon('play', null, true)}</span>`
    : post.file_type === 'gif'
      ? '<span class="pf-gif">GIF</span>'
      : '';
  const dl = (post.downloadedAt || post.downloaded)
    ? `<span class="pf-dl" title="Скачано">${icon('check', null, true)}</span>`
    : '';
  const score = post.score ? `<span class="pf-score">${icon('star', null, true)}${post.score}</span>` : '';
  tile.innerHTML = `<img src="" alt="" loading="lazy" decoding="async"><div class="pf-fallback">#${post.id}<span class="pf-err"></span></div>${play}${dl}<div class="pf-open"><span class="pf-open-icon">${icon('externalLink')}</span></div><div class="pf-bar"><span>#${post.id}</span>${score}</div>`;
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
        if (errEl) errEl.textContent = 'недоступно';
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
    // Ctrl/Cmd+ЛКМ — как у ссылки: пост открывается в новой вкладке.
    if (this.isOpenInNewTabClick(e)) {
      e.preventDefault();
      this.openInNewTab(this.postUrl(this.state.query, post.id));
      return;
    }
    this.openPostFromProfile(post);
  });
  return tile;
};

// Заглушки для лайков/скрытий, которых нет ни локально, ни на источнике
// (пост удалён/CDN отдаёт 404): пользователь видит, что id учтён в профиле.
App._appendMissingThumbs = function (el, ids) {
  if (!el || !ids || !ids.length) return;
  const frag = document.createDocumentFragment();
  ids.forEach(id => {
    const tile = document.createElement('div');
    tile.className = 'pf-thumb pf-broken pf-missing';
    tile.title = `Пост #${id} недоступен на источнике`;
    tile.textContent = '';
    tile.appendChild((() => {
      const fb = document.createElement('div');
      fb.className = 'pf-fallback';
      fb.textContent = `#${id} · недоступен`;
      return fb;
    })());
    frag.appendChild(tile);
  });
  el.appendChild(frag);
};

App._appendThumbs = function (el, posts) {
  if (!el) return;
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
  if (!el) return;
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