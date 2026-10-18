import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';
import './viewer_related.js';
import './viewer_preload.js';
import './viewer_zoom.js';
// i18n (#11): подписи слайдшоу зависят от текущего языка — вычисляем их в
// момент вызова, а не при загрузке модуля (иначе смена языка не применится).
const SS_PLAY_ICO = () => icon('play', 16, true) + ' ' + t('viewer.slideshow');
const SS_PAUSE_ICO = () => icon('pause', 16, true) + ' ' + t('viewer.slideshow');

App._clearHeroTransitions = function () {
  if (this._heroElements) {
    for (const el of this._heroElements) {
      try {
        if (el && el.style) el.style.viewTransitionName = '';
      } catch { /* noop */ }
    }
    this._heroElements.clear();
  }
  if (typeof document !== 'undefined' && typeof document.querySelectorAll === 'function') {
    try {
      const els = document.querySelectorAll('.post-card img, .viewer-content img, [style*="view-transition-name"]');
      if (els && typeof els.forEach === 'function') {
        els.forEach((el) => {
          if (el && el.style && el.style.viewTransitionName) {
            el.style.viewTransitionName = '';
          }
        });
      }
    } catch { /* noop */ }
  }
};

App._setHeroTransition = function (el) {
  if (!el || !el.style) return;
  el.style.viewTransitionName = 'hero-post';
  if (!this._heroElements) this._heroElements = new Set();
  this._heroElements.add(el);
};

App._removeHeroTransition = function (el) {
  if (!el || !el.style) return;
  el.style.viewTransitionName = '';
  if (this._heroElements) {
    this._heroElements.delete(el);
  }
};

App.withViewTransition = function (fn) {
  if (typeof document !== 'undefined' && typeof document.startViewTransition === 'function' && !(typeof this._trMotionOff === 'function' && this._trMotionOff())) {
    if (this._activeViewTransition && typeof this._activeViewTransition.skipTransition === 'function') {
      try {
        this._activeViewTransition.skipTransition();
      } catch { /* noop */ }
    }
    try {
      const vt = document.startViewTransition(fn);
      this._activeViewTransition = vt;
      if (vt) {
        if (typeof vt.ready?.catch === 'function') vt.ready.catch(() => {});
        if (typeof vt.updateCallbackDone?.catch === 'function') vt.updateCallbackDone.catch(() => {});
        if (typeof vt.finished?.catch === 'function') vt.finished.catch(() => {});
        if (typeof vt.finished?.finally === 'function') {
          vt.finished.finally(() => {
            if (this._activeViewTransition === vt) {
              this._activeViewTransition = null;
            }
          }).catch(() => {});
        }
      }
      return vt;
    } catch {
      return Promise.resolve(fn());
    }
  }
  return Promise.resolve(fn());
};

App.openViewer = function (index) {
  if (index < 0 || index >= this.state.posts.length) return;
  this.stopSlideshow();
  this._resetFullscreen();
  const main = document.getElementById('main');
  this.state._savedScrollTop = main ? main.scrollTop : 0;
  this.state.viewerIndex = index;
  this.state.viewerOpen = true;
  // Perf (#8) / Race (#1): сбрасываем кэш zoom-метрик (контейнер мог
  // изменить размер) и дедуп chips-запросов «родитель/дети».
  this._zoomMetricsCache = null;
  this._relChipsFor = null;
  this.els.viewer.setAttribute('role', 'dialog');
  this.els.viewer.setAttribute('aria-modal', 'true');
  this.els.viewer.setAttribute('aria-label', t('viewer.title'));
  if (typeof this.announce === 'function') {
    this.announce(tf('viewer.postOf', { i: index + 1, n: this.state.posts.length }));
  }
   // a11y (#4): фон скрываем от скринридеров (aria-hidden), а не просто скрываем.
   if (main) this.setAriaHidden(main, true);
   const post = this.state.posts[index];
   const cardImg = post ? document.querySelector(`.post-card[data-id="${post.id}"] img`) : null;
   this._clearHeroTransitions();
   if (cardImg) this._setHeroTransition(cardImg);
   const vt = this.withViewTransition(() => {
     this.els.viewer.classList.remove('hidden');
     this.renderViewer();
     if (cardImg) this._removeHeroTransition(cardImg);
     const vImg = this.els.viewerContent.querySelector('img');
     if (cardImg && vImg) this._setHeroTransition(vImg);
   });
   if (vt && typeof vt.finished?.finally === 'function') {
     vt.finished.finally(() => {
       this._clearHeroTransitions();
     }).catch(() => {});
   } else {
     this._clearHeroTransitions();
   }
   // focusable контейнер + trap Tab внутри хедера вьювера.
   this.els.viewerContent.setAttribute('tabindex', '-1');
   if (typeof this.trapFocus === 'function') {
      this._releaseViewerTrap = this.trapFocus(this.els.viewer, this.els.viewerClose);
   }
  this._showViewerHud();
  document.body.style.overflow = 'hidden';
  if (!this._zoomControlsBound) { this._zoomControlsBound = true; this._bindZoomControls(); }
  if (!this._viewerNewTabBound) { this._viewerNewTabBound = true; this._bindViewerNewTab(); }
  // Видео всегда открываем в полноэкранном режиме просмотра.
  if (post && post.file_type === 'video' && !this._viewerIsFullscreen()) {
    this.toggleFullscreen();
  }
  this._recMarkViewed(post ? post.id : null);
  this._markViewed(post ? post.id : null);
  this.scheduleRelated(post);
  if (post) this.pushState(this.state.query, post.id);
  // Предзагрузка соседей: скрытый <video> один, поэтому последним
  // запрашиваем следующего поста — его буфер важнее для листания вперёд.
  if (index - 1 >= 0) this.preloadNeighbor(this.state.posts[index - 1]);
  if (index + 1 < this.state.posts.length) this.preloadNeighbor(this.state.posts[index + 1]);
  this.initViewerTouch();
};

App.closeViewer = function (opts) {
  this.stopSlideshow();
  this._resetFullscreen();
  this._cancelPreloads();
  // Отменяем в-полёте запросы похожих/счётчиков тегов — вьюер закрыт.
  if (this._relAbort) { this._relAbort.abort(); this._relAbort = null; }
  if (this._countAbort) { this._countAbort.abort(); this._countAbort = null; }
  // Race (#2): отложенный loadRelated не должен стрелять уже после закрытия —
  // снимаем таймер scheduleRelated вместе с AbortController'ами.
  clearTimeout(this._relTimer);
  this._relTimer = null;
  this._relChipsToken = (this._relChipsToken || 0) + 1;
  if (this._relChipsAbort) { this._relChipsAbort.abort(); this._relChipsAbort = null; }
  if (this._touchCleanup) { this._touchCleanup(); this._touchCleanup = null; }
  // Останавливаем фоновую предзагрузку видео, чтобы не держать скачивание.
  this._stashCurrentVideoTime();
  // Очищаем слушатели видео через AbortController (P1-14).
  const vCur = this.currentVideo();
  if (vCur) this._unbindVideoEvents(vCur);
  // Memory (#4): у ТЕКУЩЕГО видео тоже снимаем воспроизведение и буфер —
  // раньше src чистился только у preload-видео, а играющее видео могло
  // продолжать докачивать данные до GC.
  this._stopVideoEl(vCur);
  clearTimeout(this._preloadDwellTimer);
  this._preloadDwellTimer = null;
  if (this._preloadVideoEl) {
    try { this._preloadVideoEl.src = ''; } catch { /* noop */ }
    // Memory (#14): не оставляем скрытый <video> висеть в <body> на весь
    // жизненный цикл страницы — при следующем preloadVideo он создастся заново.
    try { this._preloadVideoEl.remove(); } catch { /* noop */ }
    this._preloadVideoEl = null;
  }
  this._preloadVideoUrl = null;
  this._unmuteHintEl = null;
  this._viewerErrorEl = null;
  const stack = this._ctxStack || [];
  if (this.state.relatedChain && stack.length) {
    const ctx = stack.pop();
    this.state.posts = ctx.posts || [];
    this.state.relatedChain = false;
    const idx = Math.min(ctx.index != null ? ctx.index : 0, this.state.posts.length ? this.state.posts.length - 1 : 0);
    this.state.focusedIndex = idx;
    this.state.viewerIndex = idx;
  }
  if (this._releaseViewerTrap) { this._releaseViewerTrap(); this._releaseViewerTrap = null; }
  this.state.viewerOpen = false;
  // Возврат из вьювера обратно в турнир: оверлей турнира остаётся живым под
  // вьювером (см. _trOpenPost), поэтому возвращаем его сюда. Без этого после
  // просмотра одного участника турнир был бы закрыт, и остальных не посмотреть.
  if (this._tournament && this._tournament.viewerOpen && typeof this._trReturnFromViewer === 'function') {
    this._trReturnFromViewer();
  }
  const curPost = this.state.posts[this.state.viewerIndex];
  const cardImg = curPost ? document.querySelector(`.post-card[data-id="${curPost.id}"] img`) : null;
  const vImg = this.els.viewerContent.querySelector('img');
  this._clearHeroTransitions();
  if (vImg && cardImg) {
    this._setHeroTransition(vImg);
  }
  const vt = this.withViewTransition(() => {
    if (vImg) this._removeHeroTransition(vImg);
    this.els.viewer.classList.add('hidden');
    if (vImg && cardImg) this._setHeroTransition(cardImg);
  });
  if (vt && typeof vt.finished?.finally === 'function') {
    vt.finished.finally(() => {
      this._clearHeroTransitions();
    }).catch(() => {});
  } else {
    this._clearHeroTransitions();
  }
  // a11y (#4): возвращаем фону скринридеры + убираем диалог-атрибуты.
  const main = document.getElementById('main');
  if (main) this.setAriaHidden(main, false);
  this.els.viewer.removeAttribute('aria-modal');
  this.els.viewerRelated.classList.add('hidden');
  document.body.style.overflow = '';
  this.els.viewerContent.classList.remove('video-loading');
  this.els.viewerContent.classList.remove('image-loading');
  this._hideBlurhashPlaceholder(this.els.viewerContent);
  if (typeof this._hidePreviewPlaceholder === 'function') {
    this._hidePreviewPlaceholder(this.els.viewerContent, true);
  }
  // Удаляем кнопку "1:1" (U5: накапливалась при каждом открытии вьюера).
  this.els.viewerContent.querySelectorAll('.viewer-orig').forEach(el => el.remove());
  this.els.viewerContent.innerHTML = '';
     if (this.state.posts.length) {
    this.state.focusedIndex = Math.min(this.state.viewerIndex, this.state.posts.length - 1);
    this.focusCard();
  }
  if (main && this.state._savedScrollTop != null) {
    main.scrollTop = this.state._savedScrollTop;
  } else {
    this.scrollToFocused();
  }
  this.state._savedScrollTop = null;
  // keepUrl: вызывающий сразу сам заменит URL (переход по тегу, на главную) —
  // историю не трогаем, иначе отложенный history.back() отменит новый pushState.
  if (!(opts && opts.keepUrl)) this._closeModalHistory();
  if (this._viewerTagChanged) {
    this._viewerTagChanged = false;
    this.loadPosts(true, null, true);
  }
  this._cancelPan();
  // Minor (#15): гасим и инерцию панорамирования — иначе висящий rAF-кадр
  // сделает один лишний panBy уже после закрытия вьюера.
  this._cancelInertia();
  // Perf (#8): метрики контейнера после закрытия невалидны.
  this._zoomMetricsCache = null;
  this._zoomActive = false; this._zoomScale = 1; this._zoomTx = 0; this._zoomTy = 0;
  if (this.els.zoomLabel) this.els.zoomLabel.textContent = '100%';
  try {
    localStorage.removeItem('better34_zoom');
    localStorage.removeItem('briefly_zoom');
  } catch {}
  if (this._touchCleanup) { this._touchCleanup(); }
};

// Memory (#4): замена <video> через innerHTML='' не останавливает элемент —
// браузер может продолжать докачивать буфер до GC. Явные pause + src=''.
App._stopVideoEl = function (v) {
  if (!v || v.tagName !== 'VIDEO') return;
  try { v.pause(); } catch { /* noop */ }
  try { v.removeAttribute('src'); v.load(); } catch { /* noop */ }
};

App._viewerIsFullscreen = function () {
  return !!(this.els.viewerContent && this.els.viewerContent.classList.contains('fullscreen'));
};



/**
 * Абсолютный URL текущего поста — то, что уходит в «Поделиться».
 *
 * Важно: ссылка должна быть абсолютной. Обсуждать пост имеет смысл с другого
 * устройства (телефон → мессенджер → десктоп), а относительный /post/123 там
 * просто не откроется. При схеме http/https идём от location.origin, иначе
 * (тесты, file://) отдаём путь как есть — копировать всё равно полезнее, чем
 * ничего.
 * @this {AppType}
 */
App.shareUrl = function () {
  const path = this.postUrl(this.state.query,
    (this.state.posts || [])[this.state.viewerIndex]?.id);
  const loc = (typeof window !== 'undefined' && window.location) || null;
  const origin = loc && loc.origin;
  if (!origin || origin === 'null') return path;
  try {
    return new URL(path, origin).href;
  } catch {
    return path;
  }
};

/**
 * Текст для «Поделиться»: название тега, если в выдаче он есть, иначе id.
 * Без названия в мессенджере приходит голый URL — по нему пост не опознать.
 * @this {AppType}
 */
App.shareText = function () {
  const post = (this.state.posts || [])[this.state.viewerIndex];
  if (!post) return '';
  const q = String(this.state.query || '').trim();
  if (q) return q.replace(/_/g, ' ');
  const tags = String(post.tags || '').trim().split(/\s+/).filter(Boolean);
  return tags.slice(0, 3).join(', ') || `#${post.id}`;
};

/**
 * Копирование в буфер. execCommand — запасной путь: на http без
 * navigator.clipboard (кроме localhost) API недоступен, а буфер обмена есть
 * всегда. Обе попытки молча падают, если среда запрещает и то и другое.
 * @this {AppType}
 */
App._copyToClipboard = async function (text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* пробуем запасной путь */ }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    // Позиция вне экрана, но не display:none — иначе фокус не берётся и
    // execCommand в некоторых браузерах возвращает false.
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand && document.execCommand('copy');
    ta.remove();
    return !!ok;
  } catch {
    return false;
  }
};

/**
 * «Поделиться»: сначала нативный Web Share API (на телефоне это системное
 * меню — то, чего пользователь ждёт), при его отсутствии или отказе
 * копируем ссылку и говорим об этом.
 *
 * Промис navigator.share намеренно глотаем целиком: на десктопе без
 * поддержки он падает сразу, а на мобильном — когда пользователь закрыл
 * системный лист с крестиком. И то и другое не ошибка приложения.
 * @this {AppType}
 */
App.sharePost = async function () {
  const url = this.shareUrl();
  if (!url) return;
  const title = this.shareText();
  const nav = (typeof navigator !== 'undefined') ? navigator : null;
  if (nav && typeof nav.share === 'function') {
    try {
      await nav.share({ title, url });
      return;
    } catch (err) {
      // AbortError — пользователь закрыл лист сам. Молчим: придумывать
      // «ошибку» там, где человек отменил действие, раздражает.
      if (err && err.name === 'AbortError') return;
    }
  }
  const ok = await this._copyToClipboard(url);
  this.showToast(ok ? t('viewer.shareCopied') : t('viewer.shareFailed'), ok ? 'success' : 'error');
};

/**
 * Копирование изображения в буфер обмена (Async Clipboard API).
 * @this {AppType}
 */
App.copyCurrentImage = async function () {
  const post = this.state.posts[this.state.viewerIndex];
  if (!post) return;
  const img = this.els.viewerContent.querySelector('img');
  if (!img) {
    this.showToast(t('viewer.copyImageUnsupported'), 'info');
    return;
  }
  const nav = typeof navigator !== 'undefined' ? navigator : null;
  const win = typeof window !== 'undefined' ? window : null;
  if (!nav || !nav.clipboard || !nav.clipboard.write || !win || !win.ClipboardItem) {
    this.showToast(t('viewer.copyImageNotSupported'), 'error');
    return;
  }
  try {
    const res = await fetch(img.src);
    const blob = await res.blob();
    let pngBlob = blob;
    if (blob.type !== 'image/png' && typeof win.createImageBitmap === 'function') {
      const bitmap = await win.createImageBitmap(blob);
      const canvas = document.createElement('canvas');
      canvas.width = bitmap.width;
      canvas.height = bitmap.height;
      const ctx = canvas.getContext('2d');
      if (ctx) {
        ctx.drawImage(bitmap, 0, 0);
        pngBlob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/png'));
      }
    }
    await nav.clipboard.write([
      new win.ClipboardItem({ 'image/png': pngBlob }),
    ]);
    this.showToast(t('viewer.imageCopied'), 'success');
  } catch (err) {
    console.warn('copyCurrentImage failed:', err);
    this.showToast(t('viewer.copyImageFailed'), 'error');
  }
};

App.renderViewer = function (force) {
  const post = this.state.posts[this.state.viewerIndex];
  if (!post) return;
  const { viewerContent, viewerInfo, viewerProgress, viewerLoader, ssSpeedInput } = this.els;

  this._hideViewerMediaError();
  this._removeUnmuteHint();
  // Кнопка «1:1» от прошлого поста не должна доживать до текущего.
  const staleOrig = viewerContent.querySelector('.viewer-orig');
  if (staleOrig && typeof staleOrig.remove === 'function') staleOrig.remove();

  const isVideo = post.file_type === 'video';
  if (this.els.viewerPip) {
    this.els.viewerPip.classList.toggle('hidden', !isVideo);
  }
  if (this.els.viewerCopyImage) {
    this.els.viewerCopyImage.classList.toggle('hidden', isVideo);
  }
  // Perf (#8): метрики контейнера могли измениться (новое медиа) — сброс кэша.
  this._zoomMetricsCache = null;
  // Дедуп (#17): единая точка расчёта URL медиа (раньше логика «downloaded /
  // heavy-sample / оригинал» была продублирована в preloadImage и prefetchFull).
  const plan = this._viewerMediaPlan(post);
  const heavyImage = plan.heavy;
  const fileUrl = plan.url;
  const proxyMedia = u => `/api/proxy?url=${encodeURIComponent(u)}`;
  const originalUrl = (!post.downloaded && post.file_url && heavyImage) ? proxyMedia(post.file_url) : null;
  // Превью для постера видео: пока файл буферизуется, вместо чёрного
  // экрана показываем ту же картинку, что и в карточке ленты.
  const videoPosterUrl = (typeof this._postThumbUrl === 'function')
    ? this._postThumbUrl(post)
    : (post.preview_url ? `/api/proxy?url=${encodeURIComponent(post.preview_url)}` : `/api/thumb/${post.id}`);
  let resolvedFileUrl = fileUrl;
  try {
    const base = (typeof window !== 'undefined' && window.location) ? window.location.href : '';
    if (base) resolvedFileUrl = new URL(fileUrl, base).href;
  } catch {}

  if (force) {
    // Принудительный ререндер (кнопка «Повторить») — пересобираем медиа-элемент.
    // Memory (#4): старый <video> снимаем с воспроизведения ДО удаления из DOM.
    const stale = viewerContent.querySelector('video');
    if (stale && stale.tagName === 'VIDEO') {
      if (typeof this._stashCurrentVideoTime === 'function') this._stashCurrentVideoTime();
      this._unbindVideoEvents(stale);
      this._stopVideoEl(stale);
    }
    viewerContent.innerHTML = '';
    viewerContent.appendChild(viewerLoader);
  }

  let mediaEl = viewerContent.querySelector('img, video');
  const isLoading = force || !mediaEl || mediaEl.src !== resolvedFileUrl;
  viewerLoader.classList.toggle('active', isLoading);

  if (isVideo) {
    let adopted = false;
    if (!mediaEl || mediaEl.tagName !== 'VIDEO') {
      const pre = this._adoptPreloadedVideo(resolvedFileUrl);
      viewerContent.innerHTML = '';
      viewerContent.appendChild(viewerLoader);
      if (pre) {
        // Сосед уже предзагружался — используем его буфер как есть.
        pre.poster = videoPosterUrl;
        this._applyVideoPrefs(pre);
        this._bindVideoEvents(pre);
        viewerContent.appendChild(pre);
        mediaEl = pre;
        adopted = true;
      } else {
        const v = document.createElement('video');
        v.controls = true; v.autoplay = true; v.loop = true;
        // iOS/Safari: без playsinline autoplay раскрывает видео на весь экран.
        v.playsInline = true;
        try { v.setAttribute('playsinline', ''); } catch { /* noop */ }
        v.poster = videoPosterUrl;
        this._applyVideoPrefs(v);
        this._bindVideoEvents(v);
        viewerContent.appendChild(v);
        mediaEl = v;
      }
    }
    if (!adopted && (force || mediaEl.src !== resolvedFileUrl)) {
      // Постер + полупрозрачный лоадер (см. .viewer-content.video-loading):
      // превью видно всю загрузку, спиннер лишь слегка затемняет его.
      mediaEl.poster = videoPosterUrl;
      viewerContent.classList.add('video-loading');
      mediaEl.src = fileUrl;
      this._applyVideoPrefs(mediaEl);
      mediaEl.addEventListener('loadedmetadata', () => this._resumeVideoPosition(post), { once: true });
      const onVideoReady = () => { viewerLoader.classList.remove('active'); viewerContent.classList.remove('video-loading'); };
      mediaEl.addEventListener('loadeddata', onVideoReady, { once: true });
      mediaEl.addEventListener('error', () => {
        onVideoReady();
        this._showViewerMediaError(post);
      }, { once: true });
      this._autoplayVideo(mediaEl);
    } else {
      viewerContent.classList.remove('video-loading');
      viewerLoader.classList.remove('active');
      if (adopted) {
        // Видео уже в буфере (метаданные скорее всего тоже) — позиция и запуск сразу.
        if (mediaEl.readyState >= 1) this._resumeVideoPosition(post);
        else mediaEl.addEventListener('loadedmetadata', () => this._resumeVideoPosition(post), { once: true });
        this._autoplayVideo(mediaEl);
      }
    }
  } else {
    viewerContent.classList.remove('video-loading');
    if (!mediaEl || mediaEl.tagName !== 'IMG') {
      viewerContent.innerHTML = '';
      viewerContent.appendChild(viewerLoader);
      const img = document.createElement('img');
      img.alt = '';
      img.draggable = false;
      img.fetchPriority = 'high';
      viewerContent.appendChild(img);
      mediaEl = img;
    }
    if (mediaEl.src !== resolvedFileUrl) {
      this._showBlurhashPlaceholder(post, viewerContent);
      if (typeof this._showPreviewPlaceholder === 'function') {
        this._showPreviewPlaceholder(post, viewerContent);
      }
      viewerContent.classList.add('image-loading');
      if (mediaEl.classList) mediaEl.classList.add('loading');
      mediaEl.fetchPriority = 'high';
      mediaEl.src = fileUrl;
      const loadPostId = post.id;
      const onImageDone = () => {
        const curPost = this.state.posts[this.state.viewerIndex];
        if (curPost && curPost.id !== loadPostId) return;
        viewerContent.classList.remove('image-loading');
        this._hideBlurhashPlaceholder(viewerContent);
        if (typeof this._hidePreviewPlaceholder === 'function') {
          this._hidePreviewPlaceholder(viewerContent);
        }
        viewerLoader.classList.remove('active');
        if (mediaEl.classList) {
          mediaEl.classList.remove('loading');
          mediaEl.classList.remove('fade-in');
          void mediaEl.offsetWidth;
          mediaEl.classList.add('fade-in');
        }
        this.applyZoomTransform();
      };
      mediaEl.addEventListener('load', onImageDone, { once: true });
      mediaEl.addEventListener('error', () => {
        const curPost = this.state.posts[this.state.viewerIndex];
        if (curPost && curPost.id !== loadPostId) return;
        viewerContent.classList.remove('image-loading');
        this._hideBlurhashPlaceholder(viewerContent);
        if (typeof this._hidePreviewPlaceholder === 'function') {
          this._hidePreviewPlaceholder(viewerContent);
        }
        viewerLoader.classList.remove('active');
        if (mediaEl.classList) mediaEl.classList.remove('loading');
        if (typeof this._showViewerMediaError === 'function') {
          this._showViewerMediaError(post);
        }
      }, { once: true });
    } else {
      viewerContent.classList.remove('image-loading');
      this._hideBlurhashPlaceholder(viewerContent);
      if (typeof this._hidePreviewPlaceholder === 'function') {
        this._hidePreviewPlaceholder(viewerContent, true);
      }
      viewerLoader.classList.remove('active');
      if (mediaEl.classList) mediaEl.classList.remove('loading');
    }
    if (heavyImage && originalUrl && !viewerContent.querySelector('.viewer-orig')) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'viewer-orig';
      btn.textContent = '1:1';
      btn.title = t('viewer.loadOriginal');
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        btn.disabled = true;
        btn.textContent = '…';
        viewerLoader.classList.add('active');
        const done = () => { viewerLoader.classList.remove('active'); btn.remove(); };
        const fail = () => { viewerLoader.classList.remove('active'); btn.disabled = false; btn.textContent = '1:1'; };
        mediaEl.addEventListener('load', done, { once: true });
        mediaEl.addEventListener('error', fail, { once: true });
        let resolved = originalUrl;
        try {
          const base = (typeof window !== 'undefined' && window.location) ? window.location.href : '';
          if (base) resolved = new URL(originalUrl, base).href;
        } catch {}
        mediaEl.src = resolved;
      });
      viewerContent.appendChild(btn);
    }
  }

  this.applyZoomTransform();

  // Панель лежит в .viewer-body (сиблинг viewerContent) — ищем её там,
  // иначе при каждом рендере создавался дубликат кнопки.
  const barHost = viewerContent.parentElement || viewerContent;
  let ssBar = null;
  if (typeof barHost.querySelector === 'function') {
    try { ssBar = barHost.querySelector('.slideshow-controls'); } catch { ssBar = null; }
  }
  if (!ssBar) {
    ssBar = document.createElement('div');
    ssBar.className = 'slideshow-controls';
    ssBar.innerHTML =
      `<select class="btn-ss ss-rate" title="${esc(t('viewer.rateVideo'))}" aria-label="${esc(t('viewer.rateVideo'))}">` +
      [0.5, 0.75, 1, 1.25, 1.5, 2].map(r => `<option value="${r}">${r}×</option>`).join('') +
      '</select>' +
      `<button type="button" class="btn-ss btn-pip" title="${esc(t('viewer.pip'))}">${icon('pip', 16)}</button>` +
      `<button type="button" class="btn-ss" id="slideshow-btn">${SS_PLAY_ICO()}</button>`;
    barHost.appendChild(ssBar);
    this.els.slideshowBtn = ssBar.querySelector('#slideshow-btn');
    if (this.els.slideshowBtn) this.els.slideshowBtn.addEventListener('click', () => this.toggleSlideshow());
    this.els.pipBtn = ssBar.querySelector('.btn-pip');
    if (this.els.pipBtn) this.els.pipBtn.addEventListener('click', () => this.togglePictureInPicture());
    this.els.ssRateSelect = ssBar.querySelector('.ss-rate');
    if (this.els.ssRateSelect) this.els.ssRateSelect.addEventListener('change', () => {
      const r = parseFloat(this.els.ssRateSelect.value);
      if (isFinite(r)) this.applyPlaybackRate(r);
    });
  }
  ssBar.classList.toggle('has-video', isVideo);
  this._syncRateSelect();
  ssSpeedInput.value = Math.round(this.state.slideshowSpeed / 1000);

  const isLiked = this.state.profile.liked_posts && this.state.profile.liked_posts.includes(post.id);
  viewerInfo.textContent = `${post.source ? post.source + ' · ' : ''}${post.id} · ${post.width||'?'}×${post.height||'?'} · ${post.file_type || '?'} · ${this.state.viewerIndex + 1}/${this.state.posts.length}`;
  this._syncSourceButton(post);

  this._renderViewerTags();
  this._renderViewerRelations(post);
  if (typeof this.renderViewerPalette === 'function') this.renderViewerPalette(post);

  this.els.viewerLike.innerHTML = isLiked
    ? icon('heart', 20, true)
    : icon('heart', 20);
  if (this.els.viewerLike && this.els.viewerLike.classList) {
    this.els.viewerLike.classList.toggle('liked', !!isLiked);
  }
  if (this.els.viewerLikeM) {
    this.els.viewerLikeM.innerHTML = this.els.viewerLike.innerHTML;
    if (this.els.viewerLikeM.classList) {
      this.els.viewerLikeM.classList.toggle('liked', !!isLiked);
    }
  }

  viewerProgress.textContent = post.downloaded ? t('viewer.downloaded') : t('viewer.pressX');
  this.updateNavButtons();
  if (typeof this.renderComments === 'function') this.renderComments(post);
  if (typeof this.renderCollectMenu === 'function') {
    const menu = this.els.collectMenu;
    if (menu && !menu.classList.contains('hidden')) menu.classList.add('hidden');
    this.renderCollectMenu(post.id).catch(() => {});
  }
};

App.navigateViewer = function (dir) {
  const stack = this._ctxStack || [];

  if (this.state.relatedChain) {
    const ni = this.state.viewerIndex + dir;
    if (ni < 0 || ni >= this.state.posts.length) {
      if (stack.length) { this._exitContext(dir); }
      return;
    }
    this._gotoViewerIndex(ni, dir);
    return;
  }

  const ni = this._nextVisibleIndex(this.state.viewerIndex, dir);
  if (ni < 0) {
    if (dir > 0 && this.state.hasMore && !this.state.loading && !this._ctxStack.length) {
      const from = this.state.viewerIndex;
      this.loadMore().then(() => {
        if (this.state.viewerOpen && this.state.viewerIndex === from) {
          const n2 = this._nextVisibleIndex(from, dir);
          if (n2 >= 0) this._gotoViewerIndex(n2, dir);
        }
      }).catch(() => {});
    }
    return;
  }
  this._gotoViewerIndex(ni, dir);
};

App._gotoViewerIndex = function (ni, dir) {
  this._cancelPreloads();
  this._stashCurrentVideoTime();
  if (this._activeViewTransition && typeof this._activeViewTransition.skipTransition === 'function') {
    try { this._activeViewTransition.skipTransition(); } catch { /* noop */ }
  }
  this._clearHeroTransitions();
  this.state.viewerIndex = ni;
  const post = this.state.posts[ni] || null;
  this._markViewed(post ? post.id : null);
  // URL следует за листанием: перезагрузка страницы или «поделиться ссылкой»
  // откроют именно текущий пост (без новой записи в истории).
  this.replaceState(this.state.query, post ? post.id : null);
  this.renderViewer();
  this.scheduleRelated(this.state.posts[ni]);
  if (dir > 0 && ni + 1 < this.state.posts.length) this.preloadNeighbor(this.state.posts[ni + 1]);
  else if (dir < 0 && ni - 1 >= 0) this.preloadNeighbor(this.state.posts[ni - 1]);
};

App._isExcludedPost = function (post) {
  if (!post) return true;
  const p = this.state.profile || {};
  if ((p.hidden_posts || []).includes(post.id)) return true;
  const hiddenTags = (p.hidden_tags || []).map(t => t.toLowerCase());
  if (hiddenTags.length && this._hasHiddenTag(post.tags, hiddenTags)) return true;
  return false;
};

App._nextVisibleIndex = function (from, dir) {
  const N = this.state.posts.length;
  for (let i = from + dir; i >= 0 && i < N; i += dir) {
    if (!this._isExcludedPost(this.state.posts[i])) return i;
  }
  return -1;
};

App._enterContext = function (posts, opts) {
  const o = opts || {};
  const related = !!(o.related);
  if (!(related && this.state.relatedChain)) {
    (this._ctxStack = this._ctxStack || []).push({ posts: this.state.posts, index: this.state.viewerIndex });
  }
  this.state.posts = (posts || []).map((p, i) => ({ ...p, _index: i }));
  this.state.focusedIndex = 0;
  this.state.relatedChain = related;
  const start = o.index != null
    ? Math.min(Math.max(0, o.index), this.state.posts.length ? this.state.posts.length - 1 : 0)
    : 0;
  this.openViewer(start);
};

App.openRelatedChain = function (post, relPosts) {
  this._enterContext([post].concat((relPosts || [])), { related: true });
};

App._exitContext = function (dir) {
  const stack = this._ctxStack || [];
  if (!stack.length) { this.state.relatedChain = false; return false; }
  const ctx = stack.pop();
  const parent = ctx.posts || [];
  const base = parent.length
    ? Math.min(ctx.index != null ? ctx.index : 0, parent.length - 1)
    : 0;
  this.state.posts = parent;
  const target = dir < 0 ? base : this._nextVisibleIndex(base, 1);
  const final = target < 0 ? base : target;
  this.state.relatedChain = false;
  this.state.viewerIndex = final;
  this.state.focusedIndex = final;
  this.openViewer(final);
  return true;
};



App._isTouch = function () {
  if (this._isTouchVal == null) {
    this._isTouchVal = ('ontouchstart' in window) || (navigator.maxTouchPoints > 0);
  }
  return this._isTouchVal;
};

App._toggleViewerHud = function () {
  const v = this.els.viewer;
  if (!v) return;
  v.classList.toggle('viewer-hud-hidden');
};

App._showViewerHud = function () {
  const v = this.els.viewer;
  if (v) v.classList.remove('viewer-hud-hidden');
};

App._handleViewerDoubleTap = function (x, y) {
  const vc = this.els.viewerContent;
  if (vc.querySelector('video')) { this.toggleFullscreen(); return; }
  this._ensureZoomState();
  this._cancelInertia();
  if (this._zoomActive) {
    this._zoomActive = false;
  } else {
    this._zoomActive = true;
    this._zoomScale = 1; this._zoomTx = 0; this._zoomTy = 0;
    const rect = vc.getBoundingClientRect();
    this.zoomBy(2, x - rect.left, y - rect.top);
  }
  this._smoothZoom();
  this.applyZoomTransform();
  this._persistZoom();
};

App.initViewerTouch = function () {
  if (this._touchCleanup) this._touchCleanup();
  const vc = this.els.viewerContent;
  const wrap = this.els.viewerContainer || this.els.viewerContent.closest('.viewer-container') || this.els.viewerContent;
  const backdrop = this.els.viewer ? this.els.viewer.querySelector('.viewer-backdrop') : null;
  const isControl = (t) => {
    while (t && t !== wrap) {
      if (t.tagName === 'BUTTON' || t.tagName === 'INPUT' || t.tagName === 'SELECT' || t.tagName === 'TEXTAREA') return true;
      if (t.classList && (t.classList.contains('viewer-foot') || t.classList.contains('viewer-tags') || t.classList.contains('slideshow-controls') || t.classList.contains('viewer-mobile-actions'))) return true;
      t = t.parentElement;
    }
    return false;
  };
  const ptrs = new Map();
  const cur = { mode: null, sx: 0, sy: 0, lx: 0, ly: 0, dx: 0, dy: 0, ts: 0, lastT: 0, vx: 0, vy: 0, armed: false, pinchDist: 1 };
  let lastTap = 0, lastTapX = 0, lastTapY = 0, tapTimer = null;

  const media = () => vc.querySelector('img, video');

  const applyDrag = (dx, dy) => {
    const m = media();
    if (!m) return;
    m.style.transition = 'none';
    m.style.transform = `translate(${dx}px, ${dy}px)`;
    if (backdrop) backdrop.style.opacity = String(Math.max(0, 1 - dy / 550));
  };

  const resetDrag = () => {
    if (this._zoomActive) {
      this._smoothZoom();
      this.applyZoomTransform();
    } else {
      const m = media();
      if (m) { m.style.transition = 'transform 0.28s cubic-bezier(0.22, 0.8, 0.3, 1)'; m.style.transform = 'none'; }
    }
    if (backdrop) backdrop.style.opacity = '';
  };

  const canNav = (dir) => {
    if (this.state.relatedChain) {
      const ni = this.state.viewerIndex + dir;
      return ni >= 0 && ni < this.state.posts.length;
    }
    return this._nextVisibleIndex(this.state.viewerIndex, dir) >= 0;
  };

  const startDrag = (x, y) => {
    cur.sx = x; cur.sy = y; cur.lx = x; cur.ly = y;
    cur.dx = 0; cur.dy = 0; cur.vx = 0; cur.vy = 0; cur.armed = false;
    cur.ts = cur.lastT = Date.now();
    this._cancelInertia();
  };

  const onDown = (e) => {
    if (isControl(e.target)) return;
    // Ctrl/Cmd+ЛКМ — «открыть файл в новой вкладке»: жесты не запускаем,
    // иначе клик превратится в тап (скрытие HUD) или в панорамирование.
    if (this.isOpenInNewTabClick(e)) return;
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    clearTimeout(tapTimer);
    ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (ptrs.size === 2) {
      cur.mode = 'pinch';
      const [a, b] = [...ptrs.values()];
      cur.pinchDist = Math.hypot(a.x - b.x, a.y - b.y) || 1;
      resetDrag();
      try { wrap.setPointerCapture(e.pointerId); } catch {}
      return;
    }
    if (ptrs.size > 1) return;
    cur.mode = 'drag';
    startDrag(e.clientX, e.clientY);
    try { wrap.setPointerCapture(e.pointerId); } catch {}
  };

  const onMove = (e) => {
    if (!ptrs.has(e.pointerId)) return;
    ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (cur.mode === 'pinch') {
      if (ptrs.size < 2) return;
      const [a, b] = [...ptrs.values()];
      const nd = Math.hypot(a.x - b.x, a.y - b.y) || 1;
      const rect = vc.getBoundingClientRect();
      const ccx = (a.x + b.x) / 2 - rect.left;
      const ccy = (a.y + b.y) / 2 - rect.top;
      if (!this._zoomActive) { this._zoomActive = true; this._zoomScale = Math.max(this._zoomScale || 1, 2); this._zoomTx = 0; this._zoomTy = 0; this.applyZoomTransform(); this._persistZoom(); }
      this.zoomBy(nd / cur.pinchDist, ccx, ccy);
      cur.pinchDist = nd;
      e.preventDefault();
      return;
    }
    if (cur.mode !== 'drag') return;
    const now = Date.now();
    cur.dx = e.clientX - cur.sx; cur.dy = e.clientY - cur.sy;
    if (!cur.armed && (Math.abs(cur.dx) > 10 || Math.abs(cur.dy) > 10)) cur.armed = true;
    if (!cur.armed) return;
    const dt = Math.max(1, now - cur.lastT);
    cur.vx = (e.clientX - cur.lx) / dt;
    cur.vy = (e.clientY - cur.ly) / dt;
    cur.lastT = now;
    if (this._zoomActive) {
      const cdx = e.clientX - cur.lx, cdy = e.clientY - cur.ly;
      this.panBy(cdx, cdy);
      this._lastVel = { x: cdx, y: cdy };
    } else {
      let tx = cur.dx, ty = cur.dy;
      if (Math.abs(cur.dx) > Math.abs(cur.dy)) {
        ty = 0;
        if ((cur.dx > 0 && !canNav(-1)) || (cur.dx < 0 && !canNav(1))) tx = cur.dx * 0.3;
      } else if (cur.dy > 0) {
        tx = 0; ty = cur.dy * 0.55;
      } else {
        tx = 0; ty = cur.dy * 0.25;
      }
      applyDrag(tx, ty);
    }
    cur.lx = e.clientX; cur.ly = e.clientY;
    e.preventDefault();
  };

  const onUp = (e) => {
    if (!ptrs.has(e.pointerId)) return;
    ptrs.delete(e.pointerId);
    if (cur.mode === 'pinch') {
      if (ptrs.size === 1) {
        const p = [...ptrs.values()][0];
        cur.mode = 'drag';
        startDrag(p.x, p.y);
        resetDrag();
        return;
      }
      cur.mode = null;
      if (this._zoomActive && Math.abs((this._zoomScale || 1) - 1) < 0.05) {
        this._zoomActive = false;
        this.applyZoomTransform();
      }
      return;
    }
    if (cur.mode !== 'drag') return;
    cur.mode = null;
    if (!cur.armed) {
      const now = Date.now();
      if (now - lastTap < 300 && Math.hypot(e.clientX - lastTapX, e.clientY - lastTapY) < 50) {
        clearTimeout(tapTimer);
        lastTap = 0;
        this._handleViewerDoubleTap(e.clientX, e.clientY);
        return;
      }
      lastTap = now; lastTapX = e.clientX; lastTapY = e.clientY;
      clearTimeout(tapTimer);
      tapTimer = setTimeout(() => this._toggleViewerHud(), 300);
      return;
    }
    resetDrag();
    const dx = cur.dx, dy = cur.dy;
    if (this._zoomActive) {
      if (this._lastVel && (Math.abs(this._lastVel.x) > 1 || Math.abs(this._lastVel.y) > 1)) {
        this._startPanInertia(this._lastVel.x, this._lastVel.y);
      }
      return;
    }
    if (Math.abs(dx) > Math.abs(dy)) {
      const swipeNext = (dx < -90 || cur.vx < -0.6);
      const swipePrev = (dx > 90 || cur.vx > 0.6);
      if (swipeNext && (canNav(1) || (this.state.hasMore && !this.state.loading))) this.navigateViewer(1);
      else if (swipePrev && canNav(-1)) this.navigateViewer(-1);
    } else if (dy > 0 && (dy > 110 || cur.vy > 0.6)) {
      this.closeViewer();
    }
  };

  const onCancel = (e) => {
    if (ptrs.has(e.pointerId)) ptrs.delete(e.pointerId);
    if (ptrs.size === 0) {
      cur.mode = null;
      resetDrag();
    }
  };

  const onWheel = (e) => {
    if (!this._zoomActive) return;
    e.preventDefault();
    const rect = vc.getBoundingClientRect();
    const cx = e.clientX - rect.left;
    const cy = e.clientY - rect.top;
    this.zoomBy(e.deltaY < 0 ? 1.12 : 1 / 1.12, cx, cy);
  };

  wrap.addEventListener('pointerdown', onDown);
  wrap.addEventListener('pointermove', onMove);
  wrap.addEventListener('pointerup', onUp);
  wrap.addEventListener('pointercancel', onCancel);
  vc.addEventListener('wheel', onWheel, { passive: false });
  this._touchCleanup = () => {
    wrap.removeEventListener('pointerdown', onDown);
    wrap.removeEventListener('pointermove', onMove);
    wrap.removeEventListener('pointerup', onUp);
    wrap.removeEventListener('pointercancel', onCancel);
    vc.removeEventListener('wheel', onWheel);
    clearTimeout(tapTimer);
    this._touchCleanup = null;
  };
};

App.updateNavButtons = function () {
  this.els.prevBtn.style.display = this.state.viewerIndex > 0 ? 'flex' : 'none';
  this.els.nextBtn.style.display = this.state.viewerIndex < this.state.posts.length - 1 ? 'flex' : 'none';
};

App.toggleSlideshow = function () {
  if (this.state.slideshowActive) this.stopSlideshow();
  else this.startSlideshow();
};

App.startSlideshow = function () {
  if (this.state.slideshowActive) return;
  this.state.slideshowActive = true;
  if (this.els.slideshowBtn) { this.els.slideshowBtn.classList.add('active'); this.els.slideshowBtn.innerHTML = SS_PAUSE_ICO(); }
  let loadingMore = false;
  const tick = () => {
    if (!this.state.viewerOpen || !this.state.slideshowActive) { this.stopSlideshow(); return; }
    if (this.state.viewerIndex < this.state.posts.length - 1) {
      this.navigateViewer(1);
    } else if (this.state.hasMore && !this.state.loading && !loadingMore && !this._ctxStack.length) {
      loadingMore = true;
      this.loadMore().then(() => {
        loadingMore = false;
        if (this.state.slideshowActive && this.state.viewerIndex < this.state.posts.length - 1) {
          this.navigateViewer(1);
        }
      }).catch(() => { loadingMore = false; });
    } else {
      this.navigateViewer(-this.state.viewerIndex);
    }
  };
  this._slideshowTimer = setInterval(tick, this.state.slideshowSpeed);
};

App.stopSlideshow = function () {
  this.state.slideshowActive = false;
  if (this.els.slideshowBtn) { this.els.slideshowBtn.classList.remove('active'); this.els.slideshowBtn.innerHTML = SS_PLAY_ICO(); }
  clearInterval(this._slideshowTimer);
  this._slideshowTimer = null;
};

App.optimisticLike = function (postId, liked) {
  const arr = this.state.profile.liked_posts || [];
  const has = arr.includes(postId);
  if (liked == null) liked = !has;
  if (liked && !has) { arr.push(postId); }
  else if (!liked && has) { arr.splice(arr.indexOf(postId), 1); }
  this.state.profile.liked_posts = arr;
  this.updateCardLike(postId, liked);
  this._scheduleRecommendRefresh();
  return liked;
};

App.optimisticHide = function (postId, hidden) {
  const arr = this.state.profile.hidden_posts || [];
  const has = arr.includes(postId);
  if (hidden == null) hidden = !has;
  if (hidden && !has) { arr.push(postId); }
  else if (!hidden && has) { arr.splice(arr.indexOf(postId), 1); }
  this.state.profile.hidden_posts = arr;
  if (hidden) this._recSendDislike(postId);
  return hidden;
};

App.toggleLikeCurrent = function () {
  const post = this.state.posts[this.state.viewerIndex];
  if (!post) return;
  const liked = this.optimisticLike(post.id);
  this._syncLikeIcons(liked);
  this.showToast(liked ? t('viewer.liked') : t('viewer.unliked'));
  // Явное состояние: переигровка оффлайн-очереди (потерянный ответ на
  // мобильной сети) не должна переключить лайк обратно.
  API.post(`/like/${post.id}`, { liked }).catch(() => {
    this.optimisticLike(post.id, !liked);
    this._syncLikeIcons(!liked);
    this.showToast(t('err.generic'), 'error');
  });
};

App._syncLikeIcons = function (liked) {
  const html = liked
    ? icon('heart', 20, true)
    : icon('heart', 20);
  this.els.viewerLike.innerHTML = html;
  if (this.els.viewerLike && this.els.viewerLike.classList) {
    this.els.viewerLike.classList.toggle('liked', !!liked);
  }
  if (this.els.viewerLikeM) {
    this.els.viewerLikeM.innerHTML = html;
    if (this.els.viewerLikeM.classList) {
      this.els.viewerLikeM.classList.toggle('liked', !!liked);
    }
  }
};

// Скрытие/показ поста с серверной синхронизацией и кнопкой «Отмена».
// Undo гарантированно снимает hide на сервере: ждём завершения исходного
// запроса и по фактическому ответу решаем, нужен ли компенсирующий POST
// (раньше undo до ответа сервера оставлял пост скрытым навсегда).
// Запросы идут с ЯВНЫМ состоянием (hidden: true/false), а не toggle'ом:
// оффлайн-очередь на мобильной сети переиграла бы переключение.
App._hideWithUndo = function (postId) {
  const hidden = this.optimisticHide(postId);
  const req = API.post(`/hide/${postId}`, { hidden }).catch(() => null);
  if (hidden) {
    this.removeHiddenFromFeed(postId);
    this.showToastWithUndo(t('viewer.postHidden'), () => {
      this.optimisticHide(postId, false);
      this.restoreHiddenPost(postId);
      req.then(res => {
        if (res && res.hidden) return API.post(`/hide/${postId}`, { hidden: false });
        return null;
      }).then(() => { API.invalidate('/profile'); this.loadProfile(); }).catch(() => {});
    });
  } else {
    const rec = this._lastRemoved;
    if (rec && rec.post.id === postId) this.restoreHiddenPost(postId);
    this.showToast(t('viewer.postShown'));
  }
  this.invalidateFeedCache();
};

App.toggleHideCurrent = function () {
  const post = this.state.posts[this.state.viewerIndex];
  if (!post) return;
  const hiddenBefore = this.state.profile.hidden_posts && this.state.profile.hidden_posts.includes(post.id);
  this._hideWithUndo(post.id);
  if (!hiddenBefore) {
    if (this.state.viewerIndex >= this.state.posts.length) {
      this.closeViewer();
    } else {
      this.renderViewer();
    }
  }
};

App.feedToggleLike = function (post) {
  const liked = this.optimisticLike(post.id);
  API.post(`/like/${post.id}`, { liked }).catch(() => {
    this.optimisticLike(post.id, !liked);
    this.showToast(t('err.generic'), 'error');
  });
};

App.feedToggleHide = function (post) {
  this._hideWithUndo(post.id);
};

App.removeCardFromGrid = function (postId, instant) {
  const card = this.getCardById(postId);
  if (!card) return;
  const doRemove = () => {
    card.remove();
    this.els.grid.querySelectorAll('.post-card').forEach(c => {
      const cb = c.querySelector('.card-checkbox');
      const id = cb ? cb.dataset.id : null;
      const ni = id != null ? this.state.posts.findIndex(p => p.id === Number(id)) : -1;
      c.dataset.index = ni;
    });
    this.updateStatus();
    if (!this.els.grid.querySelector('.post-card')) {
      this.els.grid.innerHTML = '';
      this.els.grid.appendChild(this.renderEmptyState({
        title: t('empty.noPosts'),
        subtitle: t('empty.changeFilters'),
        actions: [{ key: 'reset', label: t('btn.resetFilters') }, { key: 'random', label: t('menu.random') }],
      }));
    }
  };
  if (instant) { doRemove(); return; }
  card.classList.add('fade-out');
  setTimeout(doRemove, 250);
};

App.removeHiddenFromFeed = function (postId) {
  const idx = this.state.posts.findIndex(p => p.id === postId);
  if (idx === -1) return;
  const removed = this.state.posts[idx];
  this.state.posts.splice(idx, 1);
  this._lastRemoved = { post: removed, idx };
  if (this.state.viewerIndex > idx) this.state.viewerIndex--;
  if (!this.state.profile.hidden_posts) this.state.profile.hidden_posts = [];
  if (!this.state.profile.hidden_posts.includes(postId)) {
    this.state.profile.hidden_posts.push(postId);
  }
  this.reindexPosts();
  this.removeCardFromGrid(postId);
};

App.reindexPosts = function () {
  this.state.posts.forEach((p, i) => { p._index = i; });
};

App.restoreHiddenPost = function (postId) {
  const rec = this._lastRemoved;
  if (!rec || rec.post.id !== postId) { this.loadPosts(true); return; }
  this._lastRemoved = null;
  this.state.posts.splice(rec.idx, 0, rec.post);
  this.reindexPosts();
  if (this.state.viewerOpen) {
    if (this.state.viewerIndex >= rec.idx) this.state.viewerIndex++;
    this.renderViewer();
  } else {
    this.renderPosts();
  }
};

App.invalidateFeedCache = function () {
  this._clearFeedCache();
  API.invalidate('/posts');
  API.invalidate('/local');
  API.invalidate('/suggest-local');
};

App.updateCardLike = function (postId, liked) {
  const card = this.getCardById(postId);
  if (card) {
    const btn = card.querySelector('.card-like-btn');
    if (btn) btn.classList.toggle('liked', liked);
    return;
  }
  this.els.grid.querySelectorAll('.post-card').forEach(c => {
    const btn = c.querySelector('.card-like-btn');
    if (btn && parseInt(btn.dataset.id) === postId) btn.classList.toggle('liked', liked);
  });
};

App.downloadCurrent = function () { const p = this.state.posts[this.state.viewerIndex]; if (p) this.downloadPost(p); };

App.downloadPost = function (post) {
  if (!post || !post.file_url) { this.showToast(t('viewer.noFile'), 'error'); return; }
  const a = document.createElement('a');
  a.href = `/api/save/${post.id}`;
  a.download = `${post.id}.${post.file_type || 'bin'}`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  this.showToast(tf('viewer.downloading', { id: post.id }));
};

App.toggleInvert = function () {
  const el = this.els.viewerContent.querySelector('img, video');
  if (!el) return;
  const inv = el.style.filter === 'invert(1)' ? '' : 'invert(1)';
  el.style.filter = inv;
  this.showToast(inv ? t('viewer.inverted') : t('viewer.uninverted'));
};

