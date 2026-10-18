import { App } from './state.js';
import { icon } from './utils.js';

/** @this {AppType} */
App.pageSize = function () {
  // U8: адаптируем pageSize к количеству колонок — на мобильных меньше постов,
  // на десктопе больше, чтобы заполнить сетку.
  const cols = this.masonryColumnCount();
  if (cols <= 1) return 15;
  if (cols <= 2) return 24;
  if (cols <= 3) return 36;
  if (cols <= 4) return 48;
  return 60;
};

App.masonryCols = [];
App.masonryCount = 0;

/** @this {AppType} */
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

/** @this {AppType} */
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

/** @this {AppType} */
App.shortestCol = function () {
  if (!this.masonryCols.length) this.ensureColumns();
  let best = this.masonryCols[0];
  for (const col of this.masonryCols) {
    if (col.offsetHeight < best.offsetHeight) best = col;
  }
  return best;
};

/** @this {AppType} */
App.clearGrid = function () {
  // Утечка: clearGrid вызывается часто (reset, showGridMode, sortBy) — карточки
  // удаляются из DOM, но IntersectionObserver держит на них сильные ссылки
  // (reveal + video pause). Отключаем наблюдение за удаляемыми, иначе утечка
  // на каждую перезагрузку ленты.
  if (this._revealObserver) {
    this.els.grid.querySelectorAll('.post-card').forEach(c => this._revealObserver.unobserve(c));
  }
  if (this._videoPauseObserver) {
    this.els.grid.querySelectorAll('.post-card').forEach(c => this._videoPauseObserver.unobserve(c));
  }
  if (this._recyclerObserver) {
    this.els.grid.querySelectorAll('.post-card').forEach(c => this._recyclerObserver.unobserve(c));
  }
  this.els.grid.innerHTML = '';
  this.masonryCols = [];
  this.masonryCount = 0;
};

/** @this {AppType} */
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
    if (main.scrollTop > 0) return;
    if (dy > 0 && main.scrollTop === 0) e.preventDefault();
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

/** @this {AppType} */
App.rebuildMasonry = function () {
  const cards = [...this.els.grid.querySelectorAll('.post-card')];
  const scrollEl = document.getElementById('main');
  const scrollTop = scrollEl ? scrollEl.scrollTop : 0;
  for (const col of this.masonryCols) col.remove();
  this.masonryCols = [];
  this.masonryCount = 0;
  this.ensureColumns();
  for (const card of cards) this.shortestCol().appendChild(card);
  if (scrollEl && scrollTop) requestAnimationFrame(() => { scrollEl.scrollTop = scrollTop; });
};

/** @this {AppType} */
App._observeCardReveal = function (card) {
  this._observeCardRecycler(card);
  if (!('IntersectionObserver' in window)) { card.classList.add('fresh'); return; }
  if (!this._revealObserver) {
    this._revealObserver = new IntersectionObserver((entries) => {
      entries.forEach(en => {
        if (!en.isIntersecting) return;
        const c = /** @type {HTMLElement} */ (en.target);
        this._revealObserver.unobserve(c);
        c.classList.remove('fresh');
        void c.offsetWidth;
        c.classList.add('fresh');
      });
    }, { rootMargin: '240px 0px' });
  }
  this._revealObserver.observe(card);
};

/**
 * Виртуализация ленты (Sliding Window DOM Recycler):
 * Для карточек, ушедших далеко за пределы экрана (>1200px),
 * временно выгружает декодированные растры изображений, снижая
 * потребление оперативной памяти и GPU. При возвращении
 * в зону видимости восстанавливает исходный src.
 * @this {AppType}
 * @param {HTMLElement} card
 */
App._observeCardRecycler = function (card) {
  if (!('IntersectionObserver' in window)) return;
  if (!this._recyclerObserver) {
    this._recyclerObserver = new IntersectionObserver((entries) => {
      entries.forEach(en => {
        const c = /** @type {HTMLElement} */ (en.target);
        const img = c.querySelector('img');
        if (!en.isIntersecting) {
          if (img && img.src && !img.dataset.origSrc && !img.src.startsWith('data:')) {
            img.dataset.origSrc = img.src;
            img.src = 'data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg"/>';
            c.classList.add('dom-recycled');
          }
        } else {
          if (img && img.dataset.origSrc) {
            img.src = img.dataset.origSrc;
            delete img.dataset.origSrc;
            c.classList.remove('dom-recycled');
          }
        }
      });
    }, { rootMargin: '1200px 0px' });
  }
  this._recyclerObserver.observe(card);
};

/** @this {AppType} */
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
