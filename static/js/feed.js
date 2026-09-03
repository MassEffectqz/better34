import { App } from './state.js';
import { icon, esc, go } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

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
    this.els.modeBarText.textContent = tf('mode.recommend', { n: liked.length });
    this.els.modeBarRefresh.classList.remove('hidden');
    bar.classList.remove('hidden');
    this.els.sentinel.style.display = '';
    return;
  }
  const mode = this.state.displayMode;
  if (mode === 'search') {
    bar.classList.add('hidden');
  } else {
    const what = mode === 'likes' ? t('mode.likes') : mode === 'hides' ? t('mode.hides') : t('mode.collection');
    const count = this.state.displayIds.length;
    this.els.modeBarText.textContent = tf('mode.showing', { what, n: count });
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
      title: t('empty.enterTags'),
      subtitle: t('empty.startTyping'),
      actions: [{ key: 'random', label: t('menu.random') }],
    }));
    this.updateStatus();
  }
  this.pushState(this.state.query, null);
};

App.showGridMode = async function (type, idsOverride) {
  const ids = idsOverride ||
    (type === 'likes'
      ? (this.state.profile.liked_posts || [])
      : (this.state.profile.hidden_posts || []));
  if (!ids.length) return;
  // Инвалидируем in-flight loadPosts: их ответы не должны дописываться в лайки.
  this._feedSeq = (this._feedSeq || 0) + 1;
  this.toggleProfile();
  this.state.displayMode = type;
  this.state.displayIds = ids;
  this.state.viewerOpen = false;
  this.state.loading = true;
  this.setStatus(`…: ${ids.length}`);
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
      this.showToast(`${this.state.posts.length} / ${ids.length}${failed > 0 ? `, -${failed}` : ''}`, 'warning');
    }
    this.setStatus(String(this.state.posts.length));
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

App.loadPosts = async function (reset = true, restorePostId = null, forceRefresh = false) {
  if (this.state.loading) {
    if (reset) this._pendingReload = { reset, restorePostId };
    return;
  }
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
      ep = `/local?page=${this.state.page}&limit=${this.pageSize()}${this.state.query ? `&tags=${encodeURIComponent(this.state.query)}` : ''}${this.state.viewedFilter ? `&viewed=${this.state.viewedFilter}` : ''}`;
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
          ? this.renderEmptyState({ title: t('empty.noLocal'), subtitle: t('empty.noLocalHint'), actions: [{ key: 'random', label: t('menu.random') }] })
          : this.renderEmptyState({
              title: t('empty.nothing'),
              subtitle: t('empty.tryOther'),
              actions: (this.state.query || (this.state.profile && this.state.profile.hidden_tags && this.state.profile.hidden_tags.length))
                ? [{ key: 'reset', label: t('btn.resetFilters') }, { key: 'random', label: t('menu.random') }]
                : [{ key: 'random', label: t('menu.random') }],
            }));
      }
      this.state.loading = false; restoreTop(); this._finishFeedLoad(); this.updateStatus(); this._runPendingReload(); return;
    }
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
      const card = this.createPostCard(post);
      card.dataset.index = postIdx;
      frag.appendChild(card);
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
      this.els.grid.appendChild(this.renderEmptyState({ title: t('empty.error'), subtitle: msg, actions: [{ key: 'retry', label: t('btn.retry') }, { key: 'random', label: t('menu.random') }] }));
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
    // Прогрессирующая пауза больше не нужна: сервер отдаёт плотные страницы
    // совпадений. Оставляем минимальную паузу как страховку от циклов.
    const wait = (this._lastAutoLoad || 0) + 1000 - Date.now();
    if (wait > 0) {
      clearTimeout(this._autoRetryTimer);
      this._autoRetryTimer = setTimeout(() => this.maybeLoadMore(), wait + 60);
      return;
    }
    this._lastAutoLoad = Date.now();
    this.loadMore().catch(() => {});
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
  // Превью помечаем kind=preview: сервер отвечает immutable Cache-Control,
  // service worker кэширует их cache-first (превью неизменяемы по построению).
  const proxyThumb = u => `/api/proxy?url=${encodeURIComponent(u)}&kind=preview`;
  const isVideo = post.file_type === 'video';
  const isGif = post.file_type === 'gif';
  const thumbUrl = post.downloaded
    ? (isVideo
      ? (post.preview_url ? proxyThumb(post.preview_url) : `/api/thumb/${post.id}`)
      : `/api/thumb/${post.id}`)
    : (post.preview_url ? proxyThumb(post.preview_url) : `/api/thumb/${post.id}`);
  const mediaUrl = post.downloaded ? `/api/file/${post.id}` : (post.file_url ? proxyUrl(post.file_url) : '');
  const isFirstScreen = this.state.posts.length < this.pageSize() * 0.35;

  const cb = document.createElement('div');
  cb.className = 'card-checkbox' + (this.state.selected.has(post.id) ? ' checked' : '');
  cb.dataset.id = post.id;
  cb.addEventListener('click', (e) => { e.stopPropagation(); this.toggleSelect(post.id); });
  card.appendChild(cb);

  const wrap = document.createElement('div');
  wrap.className = 'thumb-wrap';
  const ar = post.width && post.height ? Math.min(post.width / post.height, 3) : null;
  if (ar) wrap.style.aspectRatio = `${ar}`;

  // Медиа собираем через DOM API: inline onerror/onload в HTML-строке —
  // вектор XSS (URL из внешнего API попадали в атрибуты) и барьер для CSP.
  const img = document.createElement('img');
  img.src = thumbUrl;
  img.alt = '';
  img.decoding = 'async';
  if (isVideo) img.className = 'video-preview';
  if (isFirstScreen) { img.loading = 'eager'; img.fetchPriority = 'high'; }
  else img.loading = 'lazy';
  img.addEventListener('load', () => img.classList.add('loaded'));
  img.addEventListener('error', () => {
    if (!img.dataset.err) {
      img.dataset.err = '1';
      img.src = `/api/thumb/${post.id}`;
      return;
    }
    const fb = document.createElement('div');
    fb.className = 'thumb-fallback';
    fb.style.aspectRatio = String(post.width && post.height ? Math.min(post.width / post.height, 3) : 1);
    img.replaceWith(fb);
  });

  if (isVideo) {
    const video = document.createElement('video');
    video.src = mediaUrl;
    // Скачанное видео лежит на локальном диске сервера: метаданные
    // (длительность/первый кадр) с него берутся дёшево — подгружаем сразу,
    // чтобы ховер стартовал без ожидания. Удалённые с CDN не трогаем.
    video.preload = post.downloaded ? 'metadata' : 'none';
    video.muted = true;
    video.loop = true;
    video.loading = 'lazy';
    video.className = 'video-source';
    const badge = document.createElement('span');
    badge.className = 'video-badge';
    badge.innerHTML = icon('play', 16, true);
    wrap.append(img, video, badge);
  } else if (isGif) {
    const badge = document.createElement('span');
    badge.className = 'gif-badge';
    badge.textContent = 'GIF';
    wrap.append(img, badge);
  } else {
    wrap.append(img);
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
      title: t('empty.noMatch'),
      subtitle: t('empty.changeFilters'),
      actions: [{ key: 'reset', label: t('btn.resetFilters') }, { key: 'random', label: t('menu.random') }],
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
  this.els.batchCount.textContent = tf('batch.selected', { n });
  this.els.batchBar.classList.toggle('active', n > 0);
  document.body.classList.toggle('batch-active', n > 0);
};

// batchZipDownload — отдаёт выбранные скачанные посты одним ZIP-архивом
// (браузерный download, без чтения в память). Нескачанные сервер пропускает.
App.batchZipDownload = function () {
  const ids = Array.from(this.state.selected);
  if (!ids.length) return;
  const a = document.createElement('a');
  a.href = `/api/download-zip?ids=${ids.join(',')}`;
  a.download = 'briefly.zip';
  document.body.appendChild(a);
  a.click();
  a.remove();
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

// batchLike — массовый лайк выделенного. Оптимистично обновляем карточки,
// серверный вызов одним POST /batch/like (в отличие от batchHide не дёргаем
// API по каждому посту). Тосты с «Отменить» для отката.
App.batchLike = async function () {
  const ids = Array.from(this.state.selected);
  if (!ids.length) return;
  this.clearSelection();
  ids.forEach(id => this.optimisticLike(id, true));
  API.post('/batch/like', { ids, liked: true }).then(r => {
    const n = (r && r.changed != null) ? r.changed : ids.length;
    this.showToastWithUndo(tf('batch.liked', { n }), () => {
      ids.forEach(id => this.optimisticLike(id, false));
      return API.post('/batch/like', { ids, liked: false }).catch(() => {});
    });
  }).catch(err => {
    ids.forEach(id => this.optimisticLike(id, false));
    this.showToast(`Ошибка: ${err.message}`, 'error');
  });
};

// batchCollect — добавить выделенные посты в коллекцию. Открывает меню выбора
// (список коллекций + создание новой); сервер добавляет всё одной кнопкой.
App.batchCollect = async function () {
  const ids = Array.from(this.state.selected);
  if (!ids.length) return;
  let cols = [];
  try {
    const d = await API.get('/collections', { fresh: true });
    cols = d.collections || [];
  } catch { /* меню останется пустым */ }

  const overlay = document.createElement('div');
  overlay.className = 'batch-collect-overlay';
  overlay.setAttribute('role', 'dialog');
  overlay.setAttribute('aria-modal', 'true');
  overlay.innerHTML = `
    <div class="batch-collect-panel">
      <div class="batch-collect-head">
        <span>${esc(t('batch.pickCollection'))} (${ids.length})</span>
        <button type="button" class="btn-icon btn-icon-sm bc-close" title="Закрыть">${icon('x', 15)}</button>
      </div>
      <div class="batch-collect-new">
        <input type="text" class="bc-new-input" placeholder="${esc(t('collections.newPh'))}" maxlength="60" spellcheck="false">
        <button type="button" class="btn-primary btn-sm bc-create-btn" title="${esc(t('btn.create'))}">${icon('plus', 13)}</button>
      </div>
      <div class="batch-collect-list">${cols.length ? '' : `<p class="profile-empty">${esc(t('collections.empty'))}</p>`}</div>
    </div>`;
  const list = overlay.querySelector('.batch-collect-list');
  cols.forEach(col => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'collect-menu-item';
    b.innerHTML = `<span class="cm-name">${esc(col.name)}</span><span class="cm-count">${col.count}</span>`;
    b.addEventListener('click', async () => {
      try {
        const r = await API.post(`/collection/${col.id}/posts`, { ids });
        this.showToast(tf('batch.addedToCol', { n: (r && r.added) || ids.length }));
        API.invalidate('/profile');
        this.clearSelection();
        overlay.remove();
      } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
    });
    list.appendChild(b);
  });
  overlay.querySelector('.bc-close').addEventListener('click', () => overlay.remove());
  overlay.addEventListener('pointerdown', (e) => { if (e.target === overlay) overlay.remove(); });
  const newInput = overlay.querySelector('.bc-new-input');
  const create = async () => {
    const name = newInput.value.trim();
    if (!name) return;
    try {
      const d = await API.post('/collection', { name });
      API.invalidate('/profile');
      const r = await API.post(`/collection/${d.collection.id}/posts`, { ids });
      this.showToast(tf('batch.addedToCol', { n: (r && r.added) || ids.length }));
      this.clearSelection();
      overlay.remove();
    } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  };
  overlay.querySelector('.bc-create-btn').addEventListener('click', create);
  newInput.addEventListener('keydown', (ev) => {
    ev.stopPropagation();
    if (ev.key === 'Enter') { ev.preventDefault(); create(); }
  });
  document.body.appendChild(overlay);
  setTimeout(() => { try { newInput.focus(); } catch {} }, 50);
};

App.setStatus = function (msg) { this.els.statusText.textContent = msg; };
App.updateStatus = function () {
  const mode = this.state.isLocal ? t('status.local') : (this.state.recommendActive ? t('status.recommend') : (this.state.query ? t('status.search') : t('status.latest')));
  this.setStatus(tf('status.posts', { n: this.state.posts.length, mode }));
};
