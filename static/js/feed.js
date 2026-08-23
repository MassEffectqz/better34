import { App } from './state.js';
import { icon, esc, go } from './utils.js';
import { API } from './api.js';
const CHIP_X_ICO = icon('x', 10);
const CHIP_PLUS_ICO = icon('plus', 10);
const CHIP_MINUS_ICO = icon('minus', 10);
const CHIP_EYE_OFF_ICO = icon('eyeOff', 10);
const CHIP_STAR_ICO = icon('star', 10);

App.pageSize = function () {
  const w = window.innerWidth;
  if (w <= 450) return 20;
  if (w <= 700) return 30;
  if (w <= 1100) return 40;
  return 60;
};

App._hasHiddenTag = function (tags, hiddenTags) {
  if (!tags || !hiddenTags || !hiddenTags.length) return false;
  const tokens = tags.toLowerCase().split(/\s+/);
  return hiddenTags.some(ht => tokens.includes(ht.replace(/^[+-]+/, '')));
};

App.masonryCols = [];
App.masonryCount = 0;

App.masonryColumnCount = function () {
  const preset = this.state && this.state.gridCols;
  if (preset) return preset;
  const w = window.innerWidth;
  if (w <= 450) return 1;
  if (w <= 700) return 2;
  if (w <= 1100) return 3;
  if (w <= 1400) return 4;
  return 5;
};

App.ensureColumns = function () {
  const n = this.masonryColumnCount();
  if (this.masonryCount === n && this.masonryCols.length) return;
  this.masonryCount = n;
  this.masonryCols = [];
  for (let i = 0; i < n; i++) {
    const col = document.createElement('div');
    col.className = 'masonry-col';
    this.els.grid.appendChild(col);
    this.masonryCols.push(col);
  }
};

App.shortestCol = function () {
  if (!this.masonryCols.length) this.ensureColumns();
  let best = this.masonryCols[0];
  for (const col of this.masonryCols) {
    if (col.offsetHeight < best.offsetHeight) best = col;
  }
  return best;
};

App.clearGrid = function () {
  this.els.grid.innerHTML = '';
  this.masonryCols = [];
  this.masonryCount = 0;
};

App.initPullToRefresh = function () {
  const main = document.getElementById('main');
  if (!main || this._ptrInit) return;
  this._ptrInit = true;
  const ind = document.createElement('div');
  ind.className = 'ptr-indicator';
  ind.innerHTML = '<div class="ptr-spinner">' + icon('loader', 16) + '</div>';
  main.insertBefore(ind, main.firstChild);
  let startY = null, pull = 0;
  const reset = () => {
    startY = null; pull = 0;
    ind.classList.remove('active', 'refreshing');
    ind.style.removeProperty('--pull');
  };
  main.addEventListener('touchstart', (e) => {
    if (main.scrollTop > 0 || this.state.loading || this.state.viewerOpen) return;
    if (e.touches.length !== 1) return;
    startY = e.touches[0].clientY;
  }, { passive: true });
  main.addEventListener('touchmove', (e) => {
    if (startY == null) return;
    const dy = e.touches[0].clientY - startY;
    if (dy <= 0 || main.scrollTop > 0) { reset(); return; }
    e.preventDefault();
    pull = Math.min(dy * 0.45, 100);
    ind.style.setProperty('--pull', `${pull - 22}px`);
    ind.classList.add('active');
  }, { passive: false });
  main.addEventListener('touchend', () => {
    if (startY == null) return;
    if (pull >= 64) {
      pull = 0;
      ind.classList.add('refreshing');
      ind.style.setProperty('--pull', '26px');
      this.loadPosts(true, null, true).then(reset);
      return;
    }
    reset();
  });
};

App.rebuildMasonry = function () {
  const cards = [...this.els.grid.querySelectorAll('.post-card')];
  this.clearGrid();
  this.ensureColumns();
  for (const card of cards) this.shortestCol().appendChild(card);
};

App.getCardById = function (id) {
  const el = this.els.grid.querySelector(`.card-checkbox[data-id="${id}"]`);
  return el ? el.closest('.post-card') : null;
};

App.getCardByIndex = function (idx) {
  return this.els.grid.querySelector(`[data-index="${idx}"]`);
};

App.renderModeBar = function () {
  const bar = this.els.modeBar;
  if (this.state.recommendActive) {
    const liked = (this.state.profile && this.state.profile.liked_posts) || [];
    this.els.modeBarText.textContent = `Рекомендации по вашим лайкам (${liked.length})`;
    this.els.modeBarRefresh.classList.remove('hidden');
    bar.classList.remove('hidden');
    this.els.sentinel.style.display = '';
    return;
  }
  const mode = this.state.displayMode;
  if (mode === 'search') {
    bar.classList.add('hidden');
  } else {
    const label = mode === 'likes' ? 'Лайки' : 'Скрытые';
    const count = this.state.displayIds.length;
    this.els.modeBarText.textContent = `Показываю ${label.toLowerCase()} (${count})`;
    bar.classList.remove('hidden');
  }
  this.els.modeBarRefresh.classList.add('hidden');
  this.els.sentinel.style.display = mode !== 'search' ? 'none' : '';
};

App.clearMode = function () {
  this.state.displayMode = 'search';
  this.state.displayIds = [];
  this.state.posts = [];
  this.state.page = 1;
  this.state.hasMore = true;
  this.clearGrid();
  this._clearFeedCache();
  this.renderModeBar();
  if (this.state.query) {
    this.loadPosts(true);
  } else {
    this.clearGrid();
    this.els.grid.appendChild(this.renderEmptyState({
      title: 'Введите теги для поиска',
      subtitle: 'Начните печатать, чтобы найти посты',
      actions: [{ key: 'random', label: 'Случайный пост' }],
    }));
    this.updateStatus();
  }
  this.renderFilterChips();
  this.pushState(this.state.query, null);
};

App.showGridMode = async function (type) {
  const ids = type === 'likes'
    ? (this.state.profile.liked_posts || [])
    : (this.state.profile.hidden_posts || []);
  if (!ids.length) return;
  // Инвалидируем in-flight loadPosts: их ответы не должны дописываться в лайки.
  this._feedSeq = (this._feedSeq || 0) + 1;
  this.toggleProfile();
  this.state.displayMode = type;
  this.state.displayIds = ids;
  this.state.viewerOpen = false;
  this.state.loading = true;
  this.setStatus(`Загрузка ${type === 'likes' ? 'лайков' : 'скрытых'}: ${ids.length}...`);
  this.showSkeletons(Math.min(ids.length, 20));
  try {
    const data = await API.get(`/posts-by-ids?ids=${ids.join(',')}`);
    this.hideSkeletons();
    this.state.posts = data.posts || [];
    this.state.page = 1;
    this.state.hasMore = false;
    this.renderModeBar();
    this.renderPosts();
    const failed = ids.length - this.state.posts.length;
    if (failed > 0) {
      this.showToast(`Загружено ${this.state.posts.length} из ${ids.length}${failed > 0 ? `, ${failed} не найдены` : ''}`, 'warning');
    }
    this.setStatus(`${this.state.posts.length} ${type === 'likes' ? 'лайков' : 'скрытых'}`);
    this.pushState(this.state.query, null);
  } catch (err) {
    this.hideSkeletons();
    this.state.posts = [];
    this.renderModeBar();
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
  this.state.loading = false;
};

App.showSkeletons = function (count) {
  this.clearGrid();
  this.ensureColumns();
  const ars = [0.75, 0.67, 0.8, 0.6, 0.7, 0.85];
  for (let i = 0; i < count; i++) {
    const sk = document.createElement('div');
    sk.className = 'skeleton-card';
    sk.innerHTML = `<div class="skeleton-img" style="aspect-ratio:${ars[i % ars.length]}"></div><div class="skeleton-bar"></div><div class="skeleton-bar short"></div>`;
    this.shortestCol().appendChild(sk);
  }
};

App.hideSkeletons = function () {
  this.els.grid.querySelectorAll('.skeleton-card').forEach(s => s.remove());
};

App._feedCacheKey = function () {
  if (this.state.recommendActive) return null;
  if (this.state.query || this.state.isLocal) return null;
  return 'briefly_feed_cache';
};

App._saveFeedCache = function () {
  const key = this._feedCacheKey();
  if (!key) return;
  try {
    localStorage.setItem(key, JSON.stringify({ ts: Date.now(), posts: this.state.posts.slice(0, this.pageSize()) }));
  } catch {}
};

App._clearFeedCache = function () {
  try { localStorage.removeItem('briefly_feed_cache'); } catch {}
};

App._finishFeedLoad = function () {
  clearTimeout(this._dlShowTimer);
  this.els.sentinel.classList.remove('loading');
  this._restorePendingScroll();
};

App._observeCardReveal = function (card) {
  if (!('IntersectionObserver' in window)) { card.classList.add('fresh'); return; }
  if (!this._revealObserver) {
    this._revealObserver = new IntersectionObserver((entries) => {
      entries.forEach(en => {
        if (!en.isIntersecting) return;
        const c = en.target;
        this._revealObserver.unobserve(c);
        c.classList.remove('fresh');
        void c.offsetWidth;
        c.classList.add('fresh');
      });
    }, { rootMargin: '240px 0px' });
  }
  this._revealObserver.observe(card);
};

App._restorePendingScroll = function () {
  if (this._scrollRestored || this._pendingScrollTop == null) return;
  this._scrollRestored = true;
  const main = document.getElementById('main');
  const target = this._pendingScrollTop;
  this._pendingScrollTop = null;
  try { sessionStorage.removeItem('briefly_scroll_top'); } catch {}
  if (!main || target <= 0) return;
  const apply = () => { if (main.scrollTop === 0) main.scrollTop = target; };
  apply();
  let tries = 0;
  const retry = setInterval(() => {
    if (main.scrollTop > 0 || ++tries > 10) { clearInterval(retry); return; }
    apply();
  }, 500);
  window.addEventListener('load', () => { clearInterval(retry); apply(); }, { once: true });
};

App.renderEmptyState = function (opts) {
  const div = document.createElement('div');
  div.className = 'empty-state';
  div.style.cssText = 'flex:1 1 100%';
  div.innerHTML = `<h2>${esc(opts.title)}</h2>${opts.subtitle ? `<p>${esc(opts.subtitle)}</p>` : ''}` +
    `<div class="empty-actions">${(opts.actions || []).map(a =>
      `<button class="btn-primary btn-sm${a.danger ? ' btn-danger' : ''}" data-action="${esc(a.key)}">${esc(a.label)}</button>`
    ).join('')}</div>`;
  div.querySelectorAll('[data-action]').forEach(btn => {
    btn.addEventListener('click', () => {
      const act = btn.dataset.action;
      if (act === 'reset') this.resetFilters();
      else if (act === 'random') go(this.randomPost());
      else if (act === 'retry') this.loadPosts(true);
    });
  });
  return div;
};

App.resetFilters = function () {
  const hadHiddenTags = (this.state.profile && this.state.profile.hidden_tags && this.state.profile.hidden_tags.length) > 0;
  this.state.query = '';
  this.els.searchInput.value = '';
  this.els.searchClear.classList.remove('visible');
  if (hadHiddenTags) {
    API.post('/hidden-tags/clear').then(() => {
      API.invalidate('/profile');
      this.invalidateFeedCache();
      this.loadProfile();
    }).catch(() => {});
  }
  this.pushState('', null);
  this.loadPosts(true);
};

App.renderFilterChips = function (expanded = false) {
  const el = this.els.filterChips;
  if (!el || this.state.recommendActive) return;
  const tokens = (this.state.query || '').split(/\s+/).filter(Boolean);
  const hiddenTags = (this.state.profile && this.state.profile.hidden_tags) || [];
  const favTags = (this.state.profile && this.state.profile.fav_tags) || [];
  if (!tokens.length) {
    el.classList.add('hidden');
    el.innerHTML = '';
    return;
  }
  el.classList.remove('hidden');
  el.innerHTML = '';
  const groupLabel = (text) => {
    const l = document.createElement('span');
    l.className = 'fc-label';
    l.textContent = text;
    el.appendChild(l);
  };
  const MAX = 15;
  groupLabel('Запрос');
  tokens.forEach(tok => {
    const minus = tok.startsWith('-');
    const plus = tok.startsWith('+');
    const name = tok.replace(/^[+-]/, '');
    const chip = document.createElement('button');
    chip.className = 'fc-chip' + (minus ? ' fc-minus' : ' fc-plus');
    chip.innerHTML = `${minus ? CHIP_MINUS_ICO : plus ? CHIP_PLUS_ICO : ''}<span class="fc-name">${esc(name)}</span><span class="fc-x">${CHIP_X_ICO}</span>`;
    chip.title = `Убрать «${name}» из поиска`;
    chip.addEventListener('click', () => {
      const next = tokens.filter(t => t !== tok).join(' ');
      this.els.searchInput.value = next;
      this.search(next);
    });
    el.appendChild(chip);
  });
  const addGroup = (label, items, chipClass, ico, title, onClick) => {
    if (!items.length) return;
    groupLabel(label);
    const list = expanded ? items : items.slice(0, MAX);
    list.forEach(tag => {
      const chip = document.createElement('button');
      chip.className = `fc-chip ${chipClass}`;
      chip.innerHTML = `<span class="fc-ico">${ico}</span><span class="fc-name">${esc(tag)}</span><span class="fc-x">${CHIP_X_ICO}</span>`;
      chip.title = title;
      chip.addEventListener('click', onClick.bind(this, tag));
      el.appendChild(chip);
    });
    if (!expanded && items.length > MAX) {
      const more = document.createElement('button');
      more.className = 'fc-chip fc-more';
      more.innerHTML = `<span class="fc-name">Ещё ${items.length - MAX}…</span>`;
      more.addEventListener('click', () => this.renderFilterChips(true));
      el.appendChild(more);
    }
  };
  addGroup('Скрытые теги', hiddenTags, 'fc-hidden', CHIP_EYE_OFF_ICO, 'Показывать посты с этим тегом', (tag) => {
    API.post('/hidden-tag', { tag }).then(() => {
      API.invalidate('/profile');
      this.invalidateFeedCache();
      this.loadProfile();
      this.loadPosts(true);
    }).catch(() => {});
  });
  addGroup('Избранные', favTags, 'fc-fav', CHIP_STAR_ICO, 'Убрать из избранных', (tag) => {
    API.post('/fav-tag', { tag }).then(() => {
      API.invalidate('/profile');
      this.loadProfile();
    }).catch(() => {});
  });
};

App.loadPosts = async function (reset = true, restorePostId = null, forceRefresh = false) {
  if (this.state.loading) {
    if (reset) this._pendingReload = { reset, restorePostId };
    return;
  }
  // Новый поиск/сброс ленты — обнуляем счётчик «пустых» автодогрузок.
  if (reset) this._autoEmptyStreak = 0;
  const feedSeq = (this._feedSeq = (this._feedSeq || 0) + 1);
  const scrollEl = document.getElementById('main');
  const keepTop = forceRefresh && reset && scrollEl ? scrollEl.scrollTop : null;
  const restoreTop = () => { if (keepTop != null && scrollEl) scrollEl.scrollTop = keepTop; };
  this.state.loading = true;
  if (this._feedAbort) this._feedAbort.abort();
  this._feedAbort = new AbortController();
  clearTimeout(this._dlShowTimer);
  if (!reset) this._dlShowTimer = setTimeout(() => this.els.sentinel.classList.add('loading'), 400);
  if (reset) {
    this.state.posts = []; this.state.page = 1; this.state.hasMore = true;
    this.state.focusedIndex = -1; this.state.selected.clear();
    this._ctxStack = [];
    this._prefetchedPage = 0;
    this.state.relatedChain = false;
    this.clearGrid();
    if (forceRefresh) this.ensureColumns();
    this.state.downloadQueue.clear(); this.state.downloading.clear();
    const cacheKey = this._feedCacheKey();
    let usedCache = false;
    if (cacheKey && !forceRefresh) {
      try {
        const c = JSON.parse(localStorage.getItem(cacheKey) || 'null');
        if (c && Array.isArray(c.posts) && c.posts.length > 0 && Date.now() - c.ts < 60 * 1000) {
          this.state.posts = c.posts.map((p, i) => ({ ...p, _index: i }));
          this.state.hasMore = true;
          usedCache = true;
          this.ensureColumns();
          const hiddenIds = new Set(this.state.profile.hidden_posts || []);
          const hiddenTags = this.state.query
            ? (this.state.profile.hidden_tags || []).map(t => t.toLowerCase())
            : [];
          const frag = document.createDocumentFragment();
          this.state.posts.forEach((post, i) => {
            if (hiddenIds.has(post.id)) return;
            if (this._hasHiddenTag(post.tags, hiddenTags)) return;
            if (!this.passesFeedFilters(post)) return;
            const card = this.createPostCard(post);
            card.dataset.index = i;
            frag.appendChild(card);
          });
          const cards = Array.from(frag.children);
          cards.forEach(card => this.shortestCol().appendChild(card));
          cards.forEach(card => this._observeCardReveal(card));
          this.updateStatus();
        }
      } catch {}
    }
    this.updateBatchBar();
    if (usedCache) {
      if (restorePostId != null && !this.state.viewerOpen) {
        const idx = this.state.posts.findIndex(p => p.id === restorePostId);
        if (idx >= 0) this.openViewer(idx);
      }
      this.state.loading = false;
      this._finishFeedLoad();
      this._prefetchNextPage();
      this.scheduleFeedRefresh();
      this._runPendingReload();
      return;
    }
    if (!forceRefresh) {
      this.showSkeletons(20);
    }
  }
  try {
    let ep;
    if (this.state.recommendActive) {
      ep = `/recommend?page=${this.state.page}&limit=${this.pageSize()}`;
      const excl = (this._recViewed || []).join(',');
      if (excl) ep += `&exclude=${excl}`;
    } else if (this.state.isLocal) {
      ep = `/local?page=${this.state.page}&limit=${this.pageSize()}${this.state.query ? `&tags=${encodeURIComponent(this.state.query)}` : ''}`;
    } else {
      const rp = this.state.ratingFilter ? `&rating=${this.state.ratingFilter}` : '';
      ep = `/posts?page=${this.state.page}&limit=${this.pageSize()}${this.state.query ? `&tags=${encodeURIComponent(this.state.query)}` : ''}${rp}`;
    }
    if (this.state.minId && !this.state.recommendActive) ep += `&min_id=${this.state.minId}`;
    if (forceRefresh) ep += (ep.includes('?') ? '&' : '?') + 'v=' + Date.now();
    const data = await API.get(ep, { signal: this._feedAbort.signal });
    if (feedSeq !== this._feedSeq) return; // лента заменена (showGridMode/новый load) — ответ устарел
    if (!data) { this.hideSkeletons(); restoreTop(); this.state.loading = false; this._finishFeedLoad(); this._runPendingReload(); return; }
    this._feedFailures = 0;
    const posts = data.posts || [];
    const rawCount = posts.length;
    this.hideSkeletons();
    if (posts.length === 0) {
      this.state.hasMore = false;
      if (this.state.posts.length === 0) {
        this.els.grid.innerHTML = '';
        this.els.grid.appendChild(this.state.isLocal
          ? this.renderEmptyState({ title: 'Нет скачанных постов', subtitle: 'Скачайте посты — они появятся здесь', actions: [{ key: 'random', label: 'Случайный пост' }] })
          : this.renderEmptyState({
              title: 'Ничего не найдено',
              subtitle: 'Попробуйте другие теги или сбросьте фильтры',
              actions: (this.state.query || (this.state.profile && this.state.profile.hidden_tags && this.state.profile.hidden_tags.length))
                ? [{ key: 'reset', label: 'Сбросить фильтры' }, { key: 'random', label: 'Случайный пост' }]
                : [{ key: 'random', label: 'Случайный пост' }],
            }));
      }
      this.state.loading = false; restoreTop(); this._finishFeedLoad(); this.updateStatus(); this._runPendingReload(); return;
    }
    this.state.hasMore = rawCount >= this.pageSize();
    const existingIds = new Set(this.state.posts.map(p => p.id));
    const hiddenIds = new Set(this.state.profile.hidden_posts || []);
    const hiddenTags = this.state.query
      ? (this.state.profile.hidden_tags || []).map(t => t.toLowerCase())
      : [];
    const frag = document.createDocumentFragment();
    let added = 0;
    posts.forEach((post, i) => {
      if (existingIds.has(post.id)) return;
      if (hiddenIds.has(post.id)) return;
      if (this._hasHiddenTag(post.tags, hiddenTags)) return;
      existingIds.add(post.id);
      const postIdx = this.state.posts.length;
      this.state.posts.push({ ...post, _index: postIdx });
      added++;
      // Фильтр убирает карточку из отображения, но пост остаётся в state.
      if (!this.passesFeedFilters(post)) return;
    });
    this.state.page++;
    this.state.hasMore = rawCount > 0 && (rawCount >= this.pageSize() || added > 0);
    if (this.state.sortBy) {
      this.renderPosts();
    } else {
      const newCards = Array.from(frag.children);
      newCards.forEach(card => this.shortestCol().appendChild(card));
      newCards.forEach(card => this._observeCardReveal(card));
    }
    if (this.state.autoDownload && added > 0) {
      const ids = this.state.posts.slice(-added).filter(p => !p.downloaded).map(p => p.id);
      if (ids.length > 0) {
        ids.forEach(id => { this.state.downloadQueue.add(id); this.els.dlProgress.classList.remove('hidden'); });
        API.post('/download', { ids }).catch(() => { ids.forEach(id => this.state.downloadQueue.delete(id)); });
      }
    }
    if (this.state.posts.length > 0 && this.state.focusedIndex === -1) this.state.focusedIndex = 0;
    this.updateStatus();
    if (reset && this.state.page === 2 && !this.state.isLocal && this.state.query === '') this._saveFeedCache();
  } catch (err) {
    this.hideSkeletons();
    this._feedFailures = (this._feedFailures || 0) + 1;
    let msg = err.message;
    if (msg.includes('403')) msg = 'API вернул 403 — проверьте API key';
    else if (msg.includes('401') || msg.includes('Unauthorized')) msg = 'Ошибка авторизации API';
    else if (msg.includes('timeout') || msg.includes('Timeout')) msg = 'Таймаут API — rule34.xxx не отвечает';
    else if (msg.includes('Failed to fetch') || msg.includes('NetworkError')) msg = 'Сеть недоступна — не удалось подключиться к rule34.xxx';
    else if (msg.includes('parser')) msg = 'API вернул некорректные данные';
    else if (msg.includes('empty') || msg.includes('EOF')) msg = 'API вернул пустой ответ';
    else if (msg.length > 100) msg = msg.substring(0, 100) + '...';
    // Если лента уже что-то показывает (кэш/ранние страницы) — не стираем грид:
    // ошибка одного листа не должна уничтожать весь просмотр.
    if (this.state.posts.length > 0) {
      this.showToast(msg, 'error');
    } else {
      this.els.grid.innerHTML = '';
      this.els.grid.appendChild(this.renderEmptyState({ title: 'Ошибка запроса', subtitle: msg, actions: [{ key: 'retry', label: 'Повторить' }, { key: 'random', label: 'Случайный пост' }] }));
    }
  }
  if (restorePostId != null && !this.state.viewerOpen) {
    const idx = this.state.posts.findIndex(p => p.id === restorePostId);
    if (idx >= 0) this.openViewer(idx);
  }
  this.state.loading = false;
  restoreTop();
  this._finishFeedLoad();
  this._runPendingReload();
  this._prefetchNextPage();
  this.maybeLoadMore();
};

App._runPendingReload = function () {
  const pending = this._pendingReload;
  this._pendingReload = null;
  if (pending) this.loadPosts(pending.reset, pending.restorePostId);
};

App.scheduleFeedRefresh = function () {
  clearTimeout(this._feedRefreshTimer);
  this._feedRefreshTimer = setTimeout(() => {
    this._feedRefreshTimer = null;
    if (this.state.loading || this.state.viewerOpen || this.state.isLocal || this.state.query || this.state.recommendActive) return;
    this.loadPosts(true, null, true);
  }, 1500);
};

App.loadMore = async function () { if (this.state.displayMode !== 'search') return; await this.loadPosts(false); };

App._sentinelNearViewport = function (sent) {
  const sr = sent.getBoundingClientRect();
  const zone = Math.max(window.innerHeight * 2, 800);
  return sr.top < zone;
};

App.maybeLoadMore = function () {
  if (this.state.loading || !this.state.hasMore || this.state.displayMode !== 'search') return;
  if (this._feedFailures >= 3) return;
  const sent = this.els.sentinel;
  if (!sent || sent.style.display === 'none') return;
  if (!this._sentinelNearViewport(sent)) return;
  const fire = () => {
    if (this.state.loading || !this.state.hasMore || this.state.viewerOpen) return;
    if (sent.style.display === 'none') return;
    if (!this._sentinelNearViewport(sent)) return;
    // Прогрессирующая пауза: если под фильтр не попадает ни одна карточка
    // (короткий грид -> сентинел постоянно в зоне), автодогрузка иначе
    // молотит провайдер страницу за страницей без остановки.
    const streak = this._autoEmptyStreak || 0;
    const gap = Math.min(8000, 1000 * Math.pow(2, streak));
    if (Date.now() - (this._lastAutoLoad || 0) < gap) return;
    this._lastAutoLoad = Date.now();
    const before = this._displayCount || 0;
    this.loadMore().then(() => {
      const gained = (this._displayCount || 0) - before;
      this._autoEmptyStreak = gained > 0 ? 0 : Math.min(6, streak + 1);
    }).catch(() => {});
  };
  if ('requestIdleCallback' in window) {
    requestIdleCallback(fire, { timeout: 800 });
  } else {
    fire();
  }
};

App._prefetchNextPage = function () {
  if (this.state.loading || this.state.viewerOpen) return;
  if (this.state.recommendActive || this.state.isLocal || this.state.displayMode !== 'search') return;
  if (!this.state.hasMore || this._feedFailures >= 3) return;
  const sent = this.els.sentinel;
  if (!sent || sent.style.display === 'none') return;
  if (!this._sentinelNearViewport(sent)) return;
  // state.page уже указывает на следующую страницу к загрузке — префетчим её,
  // а не state.page+1 (иначе префетч никогда не покрывает следующий loadMore).
  const next = this.state.page;
  if (this._prefetchedPage >= next) return;
  this._prefetchedPage = next;
  let ep = `/posts?page=${next}&limit=${this.pageSize()}${this.state.query ? `&tags=${encodeURIComponent(this.state.query)}` : ''}${this.state.ratingFilter ? `&rating=${this.state.ratingFilter}` : ''}`;
  if (this.state.minId) ep += `&min_id=${this.state.minId}`;
  API.get(ep).catch(() => {});
};

// Тонкий прогресс-бар под хедером: активен, пока грузится лента.
(function () {
  const orig = App.loadPosts;
  App.loadPosts = async function (...args) {
    const bar = document.getElementById('feed-progress');
    App._progDepth = (App._progDepth || 0) + 1;
    if (bar) bar.classList.add('active');
    try { return await orig.apply(this, args); }
    finally { App._progDepth--; if (bar && !App._progDepth) bar.classList.remove('active'); }
  };
})();

App.createPostCard = function (post) {
  const card = document.createElement('div');
  const isQueued = this.state.downloading.has(post.id) || this.state.downloadQueue.has(post.id);
  card.className = 'post-card' + (post.downloaded ? ' downloaded' : '') + (isQueued ? ' queued' : '');
  card.dataset.id = post.id;

  const proxyUrl = u => `/api/proxy?url=${encodeURIComponent(u)}`;
  const isVideo = post.file_type === 'video';
  const isGif = post.file_type === 'gif';
  const thumbUrl = post.downloaded
    ? (isVideo
      ? (post.preview_url ? proxyUrl(post.preview_url) : `/api/thumb/${post.id}`)
      : `/api/thumb/${post.id}`)
    : (post.preview_url ? proxyUrl(post.preview_url) : `/api/thumb/${post.id}`);
  const mediaUrl = post.downloaded ? `/api/file/${post.id}` : (post.file_url ? proxyUrl(post.file_url) : '');
  const isFirstScreen = this.state.posts.length < this.pageSize() * 0.35;
  const imgAttrs = `alt="" decoding="async" ${isFirstScreen ? 'loading="eager" fetchpriority="high"' : 'loading="lazy"'}`;

  const cb = document.createElement('div');
  cb.className = 'card-checkbox' + (this.state.selected.has(post.id) ? ' checked' : '');
  cb.dataset.id = post.id;
  cb.addEventListener('click', (e) => { e.stopPropagation(); this.toggleSelect(post.id); });
  card.appendChild(cb);

  const wrap = document.createElement('div');
  wrap.className = 'thumb-wrap';
  const ar = post.width && post.height ? Math.min(post.width / post.height, 3) : null;
  if (ar) wrap.style.aspectRatio = `${ar}`;

  const fallbackUrl = `/api/thumb/${post.id}`;
  const par = post.width && post.height ? Math.min(post.width / post.height, 3) : 1;
  const ph = '<div class=&quot;thumb-fallback&quot; style=&quot;aspect-ratio:' + par + '&quot;></div>';
  const onerrorAttr = fallbackUrl
    ? `onerror="if(this.dataset.err){this.outerHTML='${ph}'}else{this.dataset.err='1';this.src='${fallbackUrl}'}"`
    : `onerror="this.outerHTML='${ph}'"`;


  if (isVideo) {
    wrap.innerHTML = `<img src="${thumbUrl}" ${imgAttrs} class="video-preview" onload="this.classList.add('loaded')" ${onerrorAttr}><video src="${mediaUrl}" preload="none" muted loop loading="lazy" class="video-source"></video><span class="video-badge">${icon('play', 16, true)}</span>`;
  } else if (isGif) {
    wrap.innerHTML = `<img src="${thumbUrl}" ${imgAttrs} onload="this.classList.add('loaded')" ${onerrorAttr}><span class="gif-badge">GIF</span>`;
  } else {
    wrap.innerHTML = `<img src="${thumbUrl}" ${imgAttrs} onload="this.classList.add('loaded')" ${onerrorAttr}>`;
  }

  const isLiked = this.state.profile.liked_posts && this.state.profile.liked_posts.includes(post.id);
  const isHidden = this.state.profile.hidden_posts && this.state.profile.hidden_posts.includes(post.id);
  const overlay = document.createElement('div');
  overlay.className = 'post-overlay';
  overlay.innerHTML = `
    <div class="post-overlay-left">
      <span class="post-badge ${post.downloaded ? 'dl-badge' : ''}">${post.downloaded ? icon('check', 12) : icon('download', 12)} ${post.file_type || '?'}</span>
    </div>
    <div class="post-overlay-right">
      <button class="card-like-btn ${isLiked ? 'liked' : ''}" data-id="${post.id}" title="Лайк (Q)">${icon('heart', 14)}</button>
      <button class="card-hide-btn ${isHidden ? 'hidden' : ''}" data-id="${post.id}" title="Скрыть (E)">${icon('heartOff', 14)}</button>
      <span class="post-score" title="Очки">${icon('star', 11, true)}${post.score || 0}</span>
    </div>`;
  card.appendChild(wrap);
  card.appendChild(overlay);

  let lastTap = 0, lastTapX = 0, lastTapY = 0, tapTimer = null;
  card.addEventListener('click', (e) => {
    if (e.target.closest('.card-checkbox')) return;
    const now = Date.now();
    if (this._isTouch()) {
      if (now - lastTap < 300 && Math.abs(e.clientX - lastTapX) < 40 && Math.abs(e.clientY - lastTapY) < 40) {
        clearTimeout(tapTimer);
        lastTap = 0;
        this._feedDoubleTapLike(post, card);
        return;
      }
      lastTap = now; lastTapX = e.clientX; lastTapY = e.clientY;
      clearTimeout(tapTimer);
      tapTimer = setTimeout(() => this.openViewer(parseInt(card.dataset.index)), 250);
    } else {
      this.openViewer(parseInt(card.dataset.index));
    }
  });
  let videoHoverTimer;
  card.addEventListener('mouseenter', () => {
    this.state.hoveredIndex = parseInt(card.dataset.index);
    if (!isVideo && post.file_url && !post.downloaded) this.prefetchFull(post);
    const v = wrap.querySelector('.video-source');
    const p = wrap.querySelector('.video-preview');
    if (!v) return;
    clearTimeout(videoHoverTimer);
    videoHoverTimer = setTimeout(() => {
      if (p) p.style.display = 'none';
      v.style.display = 'block';
      v.play().catch(() => {});
    }, 200);
  });
  card.addEventListener('mouseleave', () => {
    if (this.state.hoveredIndex === parseInt(card.dataset.index)) this.state.hoveredIndex = -1;
    clearTimeout(videoHoverTimer);
    const v = wrap.querySelector('.video-source');
    const p = wrap.querySelector('.video-preview');
    if (v) { v.pause(); v.currentTime = 0; v.style.display = 'none'; if (p) p.style.display = ''; }
  });

  const likeBtn = overlay.querySelector('.card-like-btn');
  likeBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const liked = this.optimisticLike(post.id);
    likeBtn.classList.toggle('liked', liked);
    API.post(`/like/${post.id}`).catch(() => {
      this.optimisticLike(post.id, !liked);
      likeBtn.classList.toggle('liked', !liked);
    });
  });

  // Видео-превью, ушедшее из вьюпорта без mouseleave (тач-скролл,
  // перестановка карточек), останавливается общим наблюдателем.
  this.observeCardViewport(card);

  const hideBtn = overlay.querySelector('.card-hide-btn');
  hideBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    this._hideWithUndo(post.id);
  });

  return card;
};

// Общий IO: пауза превью-видео карточек, покинувших вьюпорт.
App.initVideoPauseObserver = function () {
  if (this._videoPauseObserver || !('IntersectionObserver' in window)) return;
  this._videoPauseObserver = new IntersectionObserver((entries) => {
    entries.forEach((en) => {
      if (en.isIntersecting) return;
      const v = en.target.querySelector('.video-source');
      if (v && !v.paused) {
        v.pause();
        try { v.currentTime = 0; } catch { /* noop */ }
        v.style.display = 'none';
        const p = en.target.querySelector('.video-preview');
        if (p) p.style.display = '';
      }
    });
  }, { rootMargin: '80px' });
};

App.observeCardViewport = function (card) {
  this.initVideoPauseObserver();
  if (this._videoPauseObserver) this._videoPauseObserver.observe(card);
};

App._feedDoubleTapLike = function (post, card) {  const liked = this.optimisticLike(post.id);
  const btn = card.querySelector('.card-like-btn');
  if (btn) btn.classList.toggle('liked', liked);
  const burst = document.createElement('div');
  burst.className = 'like-burst' + (liked ? '' : ' unlike');
  burst.innerHTML = icon('heart', 40, true);
  card.appendChild(burst);
  setTimeout(() => { try { burst.remove(); } catch {} }, 750);
  API.post(`/like/${post.id}`).catch(() => {
    this.optimisticLike(post.id, !liked);
    if (btn) btn.classList.toggle('liked', !liked);
  });
};

// Клиентские фильтры ленты (тип/очки/разрешение): не влияют на пагинацию —
// только на отображение уже загруженных страниц.
App.passesFeedFilters = function (p) {
  const f = this.state.feedFilters || {};
  if (f.type === 'video' && p.file_type !== 'video') return false;
  if (f.type === 'gif' && p.file_type !== 'gif') return false;
  if (f.type === 'image' && (p.file_type === 'video' || p.file_type === 'gif')) return false;
  if (f.minScore > 0 && (p.score || 0) < f.minScore) return false;
  if (f.minWidth > 0 && p.width && p.width < f.minWidth) return false;
  if (f.minHeight > 0 && p.height && p.height < f.minHeight) return false;
  return true;
};

App.feedFiltersActive = function () {
  const f = this.state.feedFilters || {};
  return (f.type && f.type !== 'all') || f.minScore > 0 || f.minWidth > 0 || f.minHeight > 0;
};

App.applyFeedFilters = function (list) {
  const out = this.feedFiltersActive() ? list.filter(p => this.passesFeedFilters(p)) : list;
  this._displayCount = out.length;
  return out;
};

App.renderPosts = function () {
  if (!this.state.posts.length) return;
  let display = [...this.state.posts];
  if (this.state.displayMode === 'search') {
    const hiddenIds = new Set(this.state.profile.hidden_posts || []);
    display = display.filter(p => !hiddenIds.has(p.id));
    const sortBy = this.state.sortBy;
    if (sortBy === 'popularity' || sortBy === 'score') display.sort((a, b) => (b.score || 0) - (a.score || 0));
    else if (sortBy === 'id_desc') display.sort((a, b) => b.id - a.id);
    else if (sortBy === 'id_asc') display.sort((a, b) => a.id - b.id);
    else if (sortBy === 'size') display.sort((a, b) => (b.file_size || 0) - (a.file_size || 0));
  }
  display = this.applyFeedFilters(display);
  // Переиспользуем уже созданные карточки: перенос узла внутри документа
  // сохраняет загруженные картинки и листенеры — при активном sortBy каждая
  // подгруженная страница больше не пересоздаёт весь грид.
  const byId = new Map();
  this.els.grid.querySelectorAll('.post-card').forEach(c => {
    const id = parseInt(c.dataset.id, 10);
    if (id) byId.set(id, c);
  });
  this.clearGrid();
  this.ensureColumns();
  if (display.length === 0) {
    this.els.grid.innerHTML = '';
    this.els.grid.appendChild(this.renderEmptyState({
      title: 'Нет постов под фильтры',
      subtitle: 'Попробуйте изменить фильтры',
      actions: [{ key: 'reset', label: 'Сбросить фильтры' }, { key: 'random', label: 'Случайный пост' }],
    }));
    this.updateStatus();
    return;
  }
  const idxById = new Map(this.state.posts.map((p, i) => [p.id, i]));
  display.forEach(post => {
    const card = byId.get(post.id) || this.createPostCard(post);
    byId.delete(post.id);
    card.dataset.index = idxById.get(post.id) || 0;
    this.shortestCol().appendChild(card);
  });
  byId.forEach(card => card.remove());
  this.updateStatus();
};

App.toggleSelect = function (id) {
  if (this.state.selected.has(id)) this.state.selected.delete(id);
  else this.state.selected.add(id);
  const card = this.getCardById(id);
  const cb = card ? card.querySelector('.card-checkbox') : null;
  if (cb) cb.classList.toggle('checked');
  this.updateBatchBar();
};

App.clearSelection = function () {
  this.state.selected.clear();
  this.els.grid.querySelectorAll('.card-checkbox').forEach(c => c.classList.remove('checked'));
  this.updateBatchBar();
};

App.updateBatchBar = function () {
  const n = this.state.selected.size;
  this.els.batchCount.textContent = `Выбрано: ${n}`;
  this.els.batchBar.classList.toggle('active', n > 0);
  document.body.classList.toggle('batch-active', n > 0);
};

App.batchDownload = async function () {
  const ids = Array.from(this.state.selected);
  try {
    const r = await API.post('/download', { ids });
    this.showToast(`Поставлено в очередь: ${r.queued || ids.length}`);
    this.clearSelection();
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.batchHide = async function () {
  const ids = Array.from(this.state.selected);
  if (!ids.length) return;
  const ok = await this.confirmDialog({
    title: 'Скрыть посты',
    message: `Скрыть <b>${ids.length}</b> ${ids.length === 1 ? 'пост' : 'постов'}? Их можно будет вернуть кнопкой «Отмена».`,
    okText: 'Скрыть',
    danger: true,
  });
  if (!ok) return;
  this.clearSelection();
  ids.forEach(id => { this.optimisticHide(id, true); this.removeHiddenFromFeed(id, true); });
  this.invalidateFeedCache();
  // Пул с ограничением параллелизма: последовательные await растягивали
  // скрытие сотен постов на минуты, а полный залп упирается в лимиты браузера.
  const results = new Map();
  let cursor = 0;
  const worker = async () => {
    while (cursor < ids.length) {
      const id = ids[cursor++];
      try { results.set(id, await API.post(`/hide/${id}`)); } catch { results.set(id, null); }
    }
  };
  const all = Promise.all(Array.from({ length: Math.min(6, ids.length) }, worker));
  this.showToastWithUndo(`Скрыто: ${ids.length}`, () => {
    ids.forEach(id => this.optimisticHide(id, false));
    if (this.state.displayMode === 'search') this.loadPosts(true);
    // Снимаем hide только у тех, кого сервер успел реально скрыть.
    all.then(() => {
      const needUndo = ids.filter(id => { const r = results.get(id); return r && r.hidden; });
      if (!needUndo.length) return null;
      let ui = 0;
      const undoWorker = async () => {
        while (ui < needUndo.length) {
          await API.post(`/hide/${needUndo[ui++]}`).catch(() => {});
        }
      };
      return Promise.all(Array.from({ length: Math.min(6, needUndo.length) }, undoWorker));
    }).then(() => { API.invalidate('/profile'); this.loadProfile(); }).catch(() => {});
  });
  await all;
};

App.setStatus = function (msg) { this.els.statusText.textContent = msg; };
App.updateStatus = function () {
  const mode = this.state.isLocal ? 'локально' : (this.state.recommendActive ? 'рекомендации' : (this.state.query ? 'поиск' : 'последние посты'));
  let shown = String(this.state.posts.length);
  if (this.feedFiltersActive() && this._displayCount != null) {
    shown = `${this._displayCount}/${this.state.posts.length}`;
  }
  this.setStatus(`${shown} постов · ${mode}`);
  this.renderFilterChips();
};
