import { App } from './state.js';
import { decodeBlurHash } from './blurhash.js';

App._cancelPreloads = function () {
  if (this._preloadAbortController) {
    this._preloadAbortController.abort();
    this._preloadAbortController = null;
  }
  clearTimeout(this._preloadDwellTimer);
  this._preloadDwellTimer = null;
  if (this._preloadVideoEl) {
    try { this._preloadVideoEl.src = ''; } catch {}
  }
};

App._warmImage = function (url) {
  if (!url) return;
  if (!this._preloadAbortController || this._preloadAbortController.signal.aborted) {
    this._preloadAbortController = new AbortController();
  }
  const signal = this._preloadAbortController.signal;
  if (typeof fetch === 'function') {
    fetch(url, { signal, priority: 'low', mode: 'no-cors' }).catch(() => {});
  } else {
    try {
      const img = new Image();
      img.decoding = 'async';
      img.fetchPriority = 'low';
      img.src = url;
    } catch {}
  }
};

// ── Blurhash-плейсхолдер (задача 4) ──────────────────────────────────────
// Пока полное изображение грузится, под ним рисуется размытая 32×32
// «подложка» из blurhash — пост «виден» сразу, без белого экрана.
App._showBlurhashPlaceholder = function (post, container) {
  this._hideBlurhashPlaceholder(container);
  if (!post || !post.blurhash) return;
  try {
    const data = decodeBlurHash(post.blurhash, 32, 32);
    if (!data) return;
    const canvas = document.createElement('canvas');
    canvas.id = 'viewer-blurhash';
    canvas.width = 32;
    canvas.height = 32;
    canvas.style.cssText = 'position:absolute;left:0;top:0;width:100%;height:100%;z-index:0;pointer-events:none;';
    canvas.getContext('2d').putImageData(data, 0, 0);
    container.insertBefore(canvas, container.firstChild);
  } catch { /* blurhash битый — просто остаётся спиннер */ }
};

App._hideBlurhashPlaceholder = function (container) {
  if (!container || typeof container.querySelector !== 'function') return;
  const c = container.querySelector('#viewer-blurhash');
  if (c && typeof c.remove === 'function') c.remove();
};

// URL превью/миниатюры для подложки вьювера и постера видео.
App._postThumbUrl = function (post) {
  if (!post) return '';
  if (post.downloaded && post.thumb_path) return `/api/thumb/${post.id}`;
  if (post.preview_url) {
    if (post.preview_url.startsWith('/api/proxy') || post.preview_url.startsWith('/api/thumb')) {
      return post.preview_url;
    }
    return `/api/proxy?url=${encodeURIComponent(post.preview_url)}&kind=preview`;
  }
  if (post.downloaded) return `/api/thumb/${post.id}`;
  if (post.sample_url) {
    if (post.sample_url.startsWith('/api/proxy')) return post.sample_url;
    return `/api/proxy?url=${encodeURIComponent(post.sample_url)}`;
  }
  if (post.id) return `/api/thumb/${post.id}`;
  return '';
};

// ── Превью-плейсхолдер во вьювере ───────────────────────────────────────
// Пока полное изображение грузится, во вьювере отображается его превью
// (которое уже закэшировано из ленты), предотвращая пустой/тёмный экран.
App._showPreviewPlaceholder = function (post, container) {
  this._hidePreviewPlaceholder(container, true);
  if (!post || !container) return;
  const thumbUrl = this._postThumbUrl(post);
  if (!thumbUrl) return;
  try {
    const preview = document.createElement('div');
    preview.id = 'viewer-preview';
    preview.className = 'viewer-preview';
    preview.style.backgroundImage = `url("${thumbUrl}")`;
    const loader = (typeof container.querySelector === 'function')
      ? container.querySelector('.viewer-loader')
      : null;
    if (loader && typeof container.insertBefore === 'function') {
      container.insertBefore(preview, loader);
    } else if (typeof container.appendChild === 'function') {
      container.appendChild(preview);
    }
  } catch { /* noop */ }
};

App._hidePreviewPlaceholder = function (container, immediate) {
  if (!container || typeof container.querySelector !== 'function') return;
  try {
    const p = container.querySelector('#viewer-preview');
    if (!p) return;
    if (immediate) {
      if (typeof p.remove === 'function') p.remove();
    } else {
      if (p.classList && typeof p.classList.add === 'function') {
        p.classList.add('fade-out');
      }
      setTimeout(() => {
        if (typeof p.remove === 'function') p.remove();
      }, 200);
    }
  } catch { /* noop */ }
};

// Дедуп (#17): единая точка расчёта URL медиа (раньше логика «downloaded /
// heavy-sample / оригинал» была продублирована в preloadImage и prefetchFull).
App._viewerMediaPlan = function (post) {
  if (!post) return { heavy: false, url: '' };
  const heavy = !post.downloaded && (post.file_size || 0) > 3 * 1024 * 1024 && post.sample_url;
  const url = post.downloaded
    ? `/api/file/${post.id}`
    : `/api/proxy?url=${encodeURIComponent(heavy ? post.sample_url : (post.file_url || ''))}`;
  return { heavy, url };
};

App.preloadImage = function (post) {
  if (!post || post.file_type === 'video') return;
  // Тяжёлая картинка открывается вьювером как sample (см. renderViewer) —
  // предзагружаем именно его, иначе прогреваем оригинал.

  const heavy = !post.downloaded && (post.file_size || 0) > 3 * 1024 * 1024 && post.sample_url;
  const url = post.downloaded
    ? `/api/file/${post.id}`
    : `/api/proxy?url=${encodeURIComponent(heavy ? post.sample_url : (post.file_url || ''))}`;
  this._warmImage(url);
};

App.prefetchFull = function (post) {
  if (!post || post.file_type === 'video') return;
  if (this._prefetchUrl === post.id) return;
  this._prefetchUrl = post.id;
  // Тяжёлая картинка открывается вьювером как sample (см. renderViewer) —
  // предзагружаем именно его, иначе прогреваем оригинал.

  const heavy = !post.downloaded && (post.file_size || 0) > 3 * 1024 * 1024 && post.sample_url;
  const url = post.downloaded
    ? `/api/file/${post.id}`
    : `/api/proxy?url=${encodeURIComponent(heavy ? post.sample_url : (post.file_url || ''))}`;
  this._warmImage(url);
};

App.preloadVideo = function (post) {
  if (!post || post.file_type !== 'video') return;
  // Экономия трафика: при включённой в системе экономии данных не предзагружаем.
  const conn = typeof navigator !== 'undefined' ? navigator.connection : null;
  if (conn && conn.saveData) return;
  const url = post.downloaded ? `/api/file/${post.id}` : `/api/proxy?url=${encodeURIComponent(post.file_url || '')}`;
  if (this._preloadVideoUrl === url) return;
  this._preloadVideoUrl = url;
  clearTimeout(this._preloadDwellTimer);
  let v = this._preloadVideoEl;
  if (!v) {
    v = document.createElement('video');
    v.id = 'viewer-video-preload';
    v.muted = true;
    v.playsInline = true;
    v.preload = 'metadata';
    v.style.cssText = 'position:absolute;width:1px;height:1px;opacity:0;pointer-events:none;';
    this._preloadVideoEl = v;
    try { document.body.appendChild(v); } catch { /* noop */ }
  }
  // Сразу — только метаданные (длительность/размер); полная буферизация
  // включается ниже, если пользователь задержался на соседнем посте.
  v.src = url;
  this._preloadDwellTimer = setTimeout(() => {
    this._preloadDwellTimer = null;
    if (this._preloadVideoUrl !== url || !this._preloadVideoEl) return;
    // Не конкурируем за канал: пока текущее видео вьюера буферизуется,
    // полную загрузку соседа откладываем (см. _schedulePreloadFull).
    const cur = this.currentVideo();
    if (cur && cur.readyState < 3) { this._schedulePreloadFull(url); return; }
    try {
      this._preloadVideoEl.preload = 'auto';
      this._preloadVideoEl.load();
    } catch { /* noop */ }
  }, App._preloadDwellMs || 1200);
};

// Отложенная полная буферизация соседа: ждём, пока текущее видео не
// догрузится (readyState >= HAVE_FUTURE_DATA), иначе два параллельных
// скачивания замедляют друг друга.
App._schedulePreloadFull = function (url) {
  clearTimeout(this._preloadDwellTimer);
  this._preloadDwellTimer = setTimeout(() => {
    this._preloadDwellTimer = null;
    if (this._preloadVideoUrl !== url || !this._preloadVideoEl) return;
    const cur = this.currentVideo();
    if (cur && cur.readyState < 3) { this._schedulePreloadFull(url); return; }
    try {
      this._preloadVideoEl.preload = 'auto';
      this._preloadVideoEl.load();
    } catch { /* noop */ }
  }, 1000);
};

// Забираем скрытый предзагружаемый <video>, если он как раз для этого поста:
// переиспользуем элемент вместе с уже скачанными байтами, поэтому переход
// на соседний пост стартует мгновенно (как ховер в ленте), без новой загрузки.
App._adoptPreloadedVideo = function (absUrl) {
  const pv = this._preloadVideoEl;
  clearTimeout(this._preloadDwellTimer);
  this._preloadDwellTimer = null;
  this._preloadVideoEl = null;
  this._preloadVideoUrl = null;
  if (!pv || !absUrl || pv.src !== absUrl) return null;
  try {
    pv.removeAttribute('style');
    pv.removeAttribute('id');
    pv.controls = true;
    pv.autoplay = true;
    pv.loop = true;
    pv.playsInline = true;
    try { pv.setAttribute('playsinline', ''); } catch { /* noop */ }
  } catch { return null; }
  return pv;
};

// Предзагрузка соседнего поста: видео — скрытым <video> (метаданные сразу,
// полная буферизация после задержки «на осмотр»), картинки — как раньше
// через new Image().
App.preloadNeighbor = function (post) {
  if (!post) return;
  if (post.file_type === 'video') this.preloadVideo(post);
  else this.preloadImage(post);
};

