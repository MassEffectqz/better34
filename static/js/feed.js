import { App } from './state.js';
import { icon, esc, go } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';
import './feed_layout.js';
import './feed_card.js';

// Сколько подряд пустых листов готовы догрузить «Новое/Виденное», прежде чем
// признать выдачу исчерпанной. Окно сервера конечно, но при «Виденное» в
// большой ленте совпадений может не быть в первых окнах — даём запас.
const VIEWED_MAX_EMPTY_PAGES = 3;

/** @this {AppType} */
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
    const what = mode === 'likes' ? t('mode.likes') : mode === 'hides' ? t('mode.hides') : mode === 'similar' ? t('mode.similar') : t('mode.collection');
    const count = this.state.displayIds.length;
    this.els.modeBarText.textContent = tf('mode.showing', { what, n: count });
    bar.classList.remove('hidden');
  }
  this.els.modeBarRefresh.classList.add('hidden');
  this.els.sentinel.style.display = mode !== 'search' ? 'none' : '';
};

/** @this {AppType} */
App.clearMode = function () {
  this.state.displayMode = 'search';
  this.state.displayIds = [];
  this._similarSourceId = null;
  this.state.posts = [];
  this.state.page = 1;
  this.state.hasMore = true;
  this.clearGrid();
  this._clearFeedCache();
  this.renderModeBar();
  this.updateViewedToggle();
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

/** @this {AppType} */
App.showGridMode = async function (type, idsOverride) {
  const ids = idsOverride ||
    (type === 'likes'
      ? (this.state.profile.liked_posts || [])
      : (this.state.profile.hidden_posts || []));
  if (!ids.length) return;
  // Абортим in-flight /posts: его ответ не нужен и не должен дописываться в новый режим.
  if (this._feedAbort) this._feedAbort.abort();
  // Инвалидируем in-flight loadPosts: их ответы не должны дописываться в лайки.
  this._feedSeq = (this._feedSeq || 0) + 1;
  // Панель профиля закрываем, только если она открыта: при входе в режим по
  // глубокой ссылке (покупка /similar/<id>) панель не должна выезжать сама.
  if (this.state.profileOpen) this.toggleProfile();
  this.state.displayMode = type;
  this.state.displayIds = ids;
  // Лайки/скрытые/коллекция/«похожие» — список задан ids, фильтр по истории
  // просмотров тут неприменим: переключатель прячем (updateViewedToggle).
  this.updateViewedToggle();
  this.state.viewerOpen = false;
  this.state.loading = true;
  this.setStatus(`…: ${ids.length}`);
  this.showSkeletons(Math.min(ids.length, 20));
  try {
    // Список id бывает на сотни (вкладка «Лайки»), а сервер спрашивает
    // источник по пачке и остальное отдаёт в deferred — идём по нему следом,
    // попутно дорисовывая сетку, иначе грид вышел бы полупустым.
    const r = await this._fetchPostsByIds(ids, {
      onBatch: (got) => {
        this.state.posts = got.map((p, i) => ({ ...p, _index: i }));
        this.renderModeBar();
        this.renderPosts();
        this.setStatus(`${got.length} / ${ids.length}`);
      },
    });
    this.hideSkeletons();
    this.state.posts = r.posts.map((p, i) => ({ ...p, _index: i }));
    this.state.page = 1;
    this.state.hasMore = false;
    this.renderModeBar();
    this.renderPosts();
    const failed = ids.length - this.state.posts.length;
    if (failed > 0) {
      this.showToast(`${this.state.posts.length} / ${ids.length}${failed > 0 ? `, -${failed}` : ''}`, 'warning');
    }
    this.setStatus(String(this.state.posts.length));
    this.pushGridRoute(type);
  } catch (err) {
    this.hideSkeletons();
    this.state.posts = [];
    this.renderModeBar();
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
  this.state.loading = false;
};

/** @this {AppType} */
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

/** @this {AppType} */
App.hideSkeletons = function () {
  this.els.grid.querySelectorAll('.skeleton-card').forEach(s => s.remove());
};

/** @this {AppType} */
App._feedCacheKey = function () {
  if (this.state.recommendActive) return null;
  if (this.state.query || this.state.isLocal) return null;
  // Кэш листа — неотфильтрованная страница: с активным «Новое/Виденное»
  // он показывал бы посты, отобранные фильтром (и наоборот, скрывал бы
  // подходящие). При фильтре кэш ленты просто выключаем.
  if (this.state.viewedFilter) return null;
  return 'briefly_feed_cache';
};

/** @this {AppType} */
// setViewedFilter — единая точка смены «Все / Новое / Виденное»: состояние,
// подсветка кнопок, сброс счётчика пустых листов и перезагрузка ленты.
App.setViewedFilter = function (value) {
  const v = value || '';
  if (!this.els.viewedToggle) {
    this.state.viewedFilter = v;
    return;
  }
  this.state.viewedFilter = v;
  this.els.viewedToggle.querySelectorAll('.vt-btn').forEach((/** @type {HTMLElement} */ x) => {
    x.classList.toggle('active', x.dataset.viewed === v);
  });
  this._viewedSkip = 0;
  this.invalidateFeedCache();
  this.loadPosts(true, null, true);
};

/** @this {AppType} */
// updateViewedToggle — переключатель показываем в обычной ленте (локальной и
// онлайн-источника, включая gelbooru) и прячем в режимах-сетках (лайки,
// скрытые, коллекция, «похожие»): там список постов задан ids, фильтр по
// истории просмотров к нему неприменим.
App.updateViewedToggle = function () {
  if (!this.els.viewedToggle) return;
  const show = this.state.displayMode === 'search' && !this.state.recommendActive;
  this.els.viewedToggle.classList.toggle('hidden', !show);
};

/** @this {AppType} */
App._saveFeedCache = function () {
  const key = this._feedCacheKey();
  if (!key) return;
  try {
    localStorage.setItem(key, JSON.stringify({ ts: Date.now(), posts: this.state.posts.slice(0, this.pageSize()) }));
  } catch {}
};

/** @this {AppType} */
App._clearFeedCache = function () {
  try { localStorage.removeItem('briefly_feed_cache'); } catch {}
};

/** @this {AppType} */
App._finishFeedLoad = function () {
  clearTimeout(this._dlShowTimer);
  this.els.sentinel.classList.remove('loading');
  this._restorePendingScroll();
};

/** @this {AppType} */
App.renderEmptyState = function (opts) {
  const div = document.createElement('div');
  div.className = 'empty-state';
  div.style.cssText = 'flex:1 1 100%';
  div.innerHTML = `<h2>${esc(opts.title)}</h2>${opts.subtitle ? `<p>${esc(opts.subtitle)}</p>` : ''}` +
    `<div class="empty-actions">${(opts.actions || []).map(a =>
      `<button class="btn-primary btn-sm${a.danger ? ' btn-danger' : ''}" data-action="${esc(a.key)}">${esc(a.label)}</button>`
    ).join('')}</div>`;
  div.querySelectorAll('[data-action]').forEach(/** @param {HTMLElement} btn */ (btn) => {
    btn.addEventListener('click', () => {
      const act = btn.dataset.action;
      if (act === 'reset') this.resetFilters();
      else if (act === 'random') go(this.randomPost());
      else if (act === 'viewed-all') this.setViewedFilter('');
      else if (act === 'retry') this.loadPosts(true);
    });
  });
  return div;
};

/** @this {AppType} */
App.resetFilters = function () {
  const hadHiddenTags = (this.state.profile && this.state.profile.hidden_tags && this.state.profile.hidden_tags.length) > 0;
  this.state.query = '';
  this.setSearchValue('');
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

/** @this {AppType} */
App.loadPosts = async function (reset = true, restorePostId = null, forceRefresh = false) {
  // Страница друга перекрывает ленту (body.friend-profile-open прячет main), и
  // тогда сентинель внутри скрытого контейнера отдаёт нулевой rect —
  // _sentinelNearViewport() вечно true. Цикл loadPosts → maybeLoadMore →
  // loadPosts догружал гелбору по странице в секунду, пока открыт профиль друга
  // (у друга ушло 1800 постов вместо его лайков). Ленте под оверлеем грузиться
  // незачем, а closeFriendProfile состояние ленты не трогает.
  // Проверяем _friendProfile, а не класс на body: то же самое время жизни
  // (ставится в openFriendProfile, снимается в closeFriendProfile), но без
  // зависимости от DOM.
  if (this._friendProfile) return;
  if (this.state.loading) {
    if (reset) {
      this._pendingReload = { reset, restorePostId };
      // Абортим in-flight запрос: его ответ уже не нужен (P1-3 ранний выход).
      if (this._feedAbort) this._feedAbort.abort();
    }
    return;
  }
  const feedSeq = (this._feedSeq = (this._feedSeq || 0) + 1);
  const scrollEl = document.getElementById('main');
  const keepTop = forceRefresh && reset && scrollEl ? scrollEl.scrollTop : null;
  const restoreTop = () => { if (keepTop != null && scrollEl) scrollEl.scrollTop = keepTop; };
  this.state.loading = true;
  const mainEl = document.getElementById('main');
  if (mainEl) mainEl.setAttribute('aria-busy', 'true');
  if (this._feedAbort) this._feedAbort?.abort();
  this._feedAbort = new AbortController();
  clearTimeout(this._dlShowTimer);
  if (!reset) this._dlShowTimer = setTimeout(() => this.els.sentinel.classList.add('loading'), 150);
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
          const hiddenIds = new Set(/** @type {number[]} */ (this.state.profile.hidden_posts || []));
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
      if (mainEl) mainEl.setAttribute('aria-busy', 'false');
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
      // viewed уходит и в онлайн-выдачу: фильтр «Новое/Виденное» работает
      // по истории просмотров и не зависит от источника (gelbooru, rule34,
      // «все сайты»).
      const vp = this.state.viewedFilter ? `&viewed=${this.state.viewedFilter}` : '';
      ep = `/posts?page=${this.state.page}&limit=${this.pageSize()}${this.state.query ? `&tags=${encodeURIComponent(this.state.query)}` : ''}${rp}${vp}`;
    }
    if (this.state.minId && !this.state.recommendActive) ep += `&min_id=${this.state.minId}`;
    if (forceRefresh) ep += (ep.includes('?') ? '&' : '?') + 'v=' + Date.now();
    const data = await API.get(ep, { signal: this._feedAbort.signal });
    if (feedSeq !== this._feedSeq) return this._staleReturn(restoreTop); // лента заменена — ответ устарел
    if (!data) return this._staleReturn(restoreTop, () => this.hideSkeletons());
    this._feedFailures = 0;
    const posts = data.posts || [];
    const rawCount = posts.length;
    this.hideSkeletons();
    if (posts.length === 0) {
      // Пустой лист при активном «Новое/Виденное» ≠ конец выдачи: сервер
      // смотрит расширенное окно страниц, и в нём могло не оказаться ни
      // одного поста под фильтр. Он сообщает об этом флагом more — тогда
      // берём следующий лист сами (ограниченно, чтобы не крутить пустые
      // окна по кругу). Без флага more это честное «дальше нечего найти».
      if (this.state.viewedFilter && data.more && (this._viewedSkip = (this._viewedSkip || 0) + 1) <= VIEWED_MAX_EMPTY_PAGES) {
        this.state.page++;
        this.state.loading = false;
        if (mainEl) mainEl.setAttribute('aria-busy', 'false');
        this._finishFeedLoad();
        this.loadPosts(false).catch(() => {});
        return;
      }
      this.state.hasMore = false;
      if (this.state.posts.length === 0) {
        this.els.grid.innerHTML = '';
        this.els.grid.appendChild(this.state.viewedFilter
          ? this.renderEmptyState({
              title: t(this.state.viewedFilter === '1' ? 'viewed.seenEmpty' : 'viewed.newEmpty'),
              subtitle: t('viewed.empty'),
              actions: [{ key: 'viewed-all', label: t('viewed.all') }],
            })
          : this.state.isLocal
            ? this.renderEmptyState({ title: t('empty.noLocal'), subtitle: t('empty.noLocalHint'), actions: [{ key: 'random', label: t('menu.random') }] })
            : this.renderEmptyState({
                title: t('empty.nothing'),
                subtitle: t('empty.tryOther'),
                actions: (this.state.query || (this.state.profile && this.state.profile.hidden_tags && this.state.profile.hidden_tags.length))
                  ? [{ key: 'reset', label: t('btn.resetFilters') }, { key: 'random', label: t('menu.random') }]
                  : [{ key: 'random', label: t('menu.random') }],
              }));
      }
      this.state.loading = false;
      if (mainEl) mainEl.setAttribute('aria-busy', 'false');
      restoreTop(); this._finishFeedLoad(); this.updateStatus(); this._runPendingReload(); return;
    }
    this._viewedSkip = 0;
    const existingIds = new Set(this.state.posts.map(p => p.id));
    const hiddenIds = new Set(/** @type {number[]} */ (this.state.profile.hidden_posts || []));
    const hiddenTags = this.state.query
      ? (this.state.profile.hidden_tags || []).map(t => t.toLowerCase())
      : [];
    const frag = document.createDocumentFragment();
    let added = 0;
    posts.forEach((post) => {
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
  if (mainEl) mainEl.setAttribute('aria-busy', 'false');
  restoreTop();
  this._finishFeedLoad();
  this._runPendingReload();
  this._prefetchNextPage();
  this.maybeLoadMore();
};

/** @this {AppType} */
App._runPendingReload = function () {
  const pending = this._pendingReload;
  this._pendingReload = null;
  if (pending) this.loadPosts(pending.reset, pending.restorePostId);
};

// Гарантированный сброс состояния при устаревшем/пустом ответе: любой ранний
// выход из loadPosts проходит через него, иначе loading зависает и ломается
// pending-reload. Превращает хрупкую связность (зависимость от showGridMode
// для сброса loading) в единый контролируемый путь.
App._staleReturn = function (restoreTop, preCb) {
  if (preCb) preCb();
  this.hideSkeletons();
  restoreTop();
  this.state.loading = false;
  const mainEl = document.getElementById('main');
  if (mainEl) mainEl.setAttribute('aria-busy', 'false');
  this._finishFeedLoad();
  this._runPendingReload();
};

/** @this {AppType} */
App.scheduleFeedRefresh = function () {
  clearTimeout(this._feedRefreshTimer);
  this._feedRefreshTimer = setTimeout(() => {
    this._feedRefreshTimer = null;
    if (this.state.loading || this.state.viewerOpen || this.state.isLocal || this.state.query || this.state.recommendActive) return;
    this.loadPosts(true, null, true);
  }, 1500);
};

/** @this {AppType} */
App.loadMore = async function () { if (this.state.displayMode !== 'search') return; await this.loadPosts(false); };

/** @this {AppType} */
App._sentinelNearViewport = function (sent) {
  const sr = sent.getBoundingClientRect();
  const zone = Math.max(window.innerHeight * 2, 800);
  return sr.top < zone;
};

/** @this {AppType} */
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

/** @this {AppType} */
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
  let ep = `/posts?page=${next}&limit=${this.pageSize()}${this.state.query ? `&tags=${encodeURIComponent(this.state.query)}` : ''}${this.state.ratingFilter ? `&rating=${this.state.ratingFilter}` : ''}${this.state.viewedFilter ? `&viewed=${this.state.viewedFilter}` : ''}`;
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

App.renderPosts = function () {
  if (!this.state.posts.length) return;
  let display = [...this.state.posts];
  if (this.state.displayMode === 'search') {
    const hiddenIds = new Set(/** @type {number[]} */ (this.state.profile.hidden_posts || []));
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
  this.els.grid.querySelectorAll('.post-card').forEach(/** @param {HTMLElement} c */ (c) => {
    const id = parseInt(c.dataset.id || '', 10);
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
  // Комментарии источника подтягиваем в фоне: источник сам пометил посты
  // has_comments, кэшируем их тихо и по одному, чтобы к моменту открытия
  // поста обсуждение уже было собрано.
  if (typeof this.scheduleSourceCommentsPrefetch === 'function') {
    this.scheduleSourceCommentsPrefetch(this.state.posts);
  }
};

/** @this {AppType} */
App.toggleSelect = function (id, shiftKey) {
  // Shift+клик по чекбоксу — диапазон от последнего обычного клика до этого
  // (как в файловых менеджерах): выделение заменяется диапазоном, якорь
  // остаётся на прежнем клике — повторный Shift растягивает/сжимает его.
  const posts = this.state.posts;
  if (shiftKey && this._lastSelId != null && this._lastSelId !== id) {
    const a = posts.findIndex(p => p.id === this._lastSelId);
    const b = posts.findIndex(p => p.id === id);
    if (a !== -1 && b !== -1) {
      const from = Math.min(a, b), to = Math.max(a, b);
      this.state.selected.clear();
      for (let i = from; i <= to; i++) this.state.selected.add(posts[i].id);
      this.els.grid.querySelectorAll('.card-checkbox').forEach(c => {
        const el = /** @type {HTMLElement} */ (c);
        el.classList.toggle('checked', this.state.selected.has(Number(el.dataset.id)));
      });
      this.updateBatchBar();
      return;
    }
  }
  if (this.state.selected.has(id)) this.state.selected.delete(id);
  else this.state.selected.add(id);
  this._lastSelId = id;
  const card = this.getCardById(id);
  const cb = card ? card.querySelector('.card-checkbox') : null;
  if (cb) cb.classList.toggle('checked');
  this.updateBatchBar();
};

/** @this {AppType} */
App.clearSelection = function () {
  this.state.selected.clear();
  this._lastSelId = null;
  this.els.grid.querySelectorAll('.card-checkbox').forEach(c => c.classList.remove('checked'));
  this.updateBatchBar();
};

/** @this {AppType} */
App.updateBatchBar = function () {
  const n = this.state.selected.size;
  this.els.batchCount.textContent = tf('batch.selected', { n });
  this.els.batchBar.classList.toggle('active', n > 0);
  document.body.classList.toggle('batch-active', n > 0);
};

/** @this {AppType} */
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

/** @this {AppType} */
App.batchDownload = async function () {
  const ids = Array.from(this.state.selected);
  try {
    const r = await API.post('/download', { ids });
    this.showToast(`Поставлено в очередь: ${r.queued || ids.length}`);
    this.clearSelection();
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

// Русская плюрализация «пост/поста/постов» для подтверждений (как pf.tag*).
const pluralPosts = (n) => {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return t('batch.postOne');
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return t('batch.postFew');
  return t('batch.postMany');
};

/** @this {AppType} */
App.batchHide = async function () {
  const ids = Array.from(this.state.selected);
  if (!ids.length) return;
  const ok = await this.confirmDialog({
    title: t('batch.hideTitle'),
    message: tf('batch.hideMsg', { n: ids.length, word: pluralPosts(ids.length) }),
    okText: t('batch.hide'),
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
      try { results.set(id, await API.post(`/hide/${id}`, { hidden: true })); } catch { results.set(id, null); }
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
          await API.post(`/hide/${needUndo[ui++]}`, { hidden: false }).catch(() => {});
        }
      };
      return Promise.all(Array.from({ length: Math.min(6, needUndo.length) }, undoWorker));
    }).then(() => { API.invalidate('/profile'); this.loadProfile(); }).catch(() => {});
  });
  await all;
};

/** @this {AppType} */
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

/** @this {AppType} */
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
        <button type="button" class="btn-icon btn-icon-sm bc-close" title="${esc(t('btn.close'))}">${icon('x', 15)}</button>
      </div>
      <div class="batch-collect-new">
        <input type="text" class="bc-new-input" placeholder="${esc(t('collections.newPh'))}" maxlength="60" spellcheck="false">
        <button type="button" class="btn-primary btn-sm bc-create-btn" title="${esc(t('btn.create'))}">${icon('plus', 13)}</button>
      </div>
      <div class="batch-collect-list">${cols.length ? '' : `<p class="profile-empty">${esc(t('collections.empty'))}</p>`}</div>
    </div>`;
  const list = /** @type {HTMLElement} */ (overlay.querySelector('.batch-collect-list'));
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
        closeOverlay();
      } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
    });
    list.appendChild(b);
  });
  const newInput = /** @type {HTMLInputElement} */ (overlay.querySelector('.bc-new-input'));
  const create = async () => {
    const name = newInput.value.trim();
    if (!name) return;
    try {
      const d = await API.post('/collection', { name });
      API.invalidate('/profile');
      const r = await API.post(`/collection/${d.collection.id}/posts`, { ids });
      this.showToast(tf('batch.addedToCol', { n: (r && r.added) || ids.length }));
      this.clearSelection();
      closeOverlay();
    } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  };
  /** @type {HTMLElement} */ (overlay.querySelector('.bc-create-btn')).addEventListener('click', create);
  newInput.addEventListener('keydown', (/** @type {KeyboardEvent} */ ev) => {
    ev.stopPropagation();
    if (ev.key === 'Enter') { ev.preventDefault(); create(); }
  });
  let releaseTrap = null;
  if (typeof this.trapFocus === 'function') {
    releaseTrap = this.trapFocus(overlay, newInput);
  }
  const closeOverlay = () => {
    if (releaseTrap) { releaseTrap(); releaseTrap = null; }
    overlay.remove();
  };
  /** @type {HTMLElement} */ (overlay.querySelector('.bc-close')).addEventListener('click', closeOverlay);
  overlay.addEventListener('pointerdown', (e) => { if (e.target === overlay) closeOverlay(); });
  document.body.appendChild(overlay);
  setTimeout(() => { try { newInput.focus(); } catch {} }, 50);
};

/** @this {AppType} */
App.setStatus = function (msg) { this.els.statusText.textContent = msg; };
/** @this {AppType} */
App.updateStatus = function () {
  const mode = this.state.isLocal ? t('status.local') : (this.state.recommendActive ? t('status.recommend') : (this.state.query ? t('status.search') : t('status.latest')));
  this.setStatus(tf('status.posts', { n: this.state.posts.length, mode }));
};
