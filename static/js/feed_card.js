import { App } from './state.js';
import { icon, iconToNode } from './utils.js';
import { API } from './api.js';
import { t } from './i18n.js';

/** @this {AppType} */
App._hasHiddenTag = function (tags, hiddenTags) {
  if (!tags || !hiddenTags || !hiddenTags.length) return false;
  const tokens = tags.toLowerCase().split(/\s+/);
  return hiddenTags.some(ht => tokens.includes(ht.replace(/^[+-]+/, '')));
};

/** @this {AppType} */
App._bindCardSwipe = function (card, getIndex) {
  if (!this._isTouch()) return;
  // Горизонтальный свайп по карточке открывает пост и сразу переключает
  // вьювер на соседние — на телефоне это естественнее, чем искать стрелки.
  // Вертикальный жест не трогаем: это обычная прокрутка ленты.
  let sx = 0, sy = 0, active = false, horizontal = false;
  const dir = () => {
    const i = getIndex();
    if (this.state.viewerOpen) {
      this.navigateViewer(this._swipeDir || 1);
      return;
    }
    this.openViewer(i);
  };
  card.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1) return;
    sx = e.touches[0].clientX; sy = e.touches[0].clientY;
    active = true; horizontal = false;
  }, { passive: true });
  card.addEventListener('touchmove', (e) => {
    if (!active || e.touches.length !== 1) return;
    const dx = e.touches[0].clientX - sx;
    const dy = e.touches[0].clientY - sy;
    if (!horizontal) {
      // Ждём явного горизонтального намерения, иначе диагональ зачтём
      // за свайп и сломаем вертикальную прокрутку.
      if (Math.abs(dx) < 24 || Math.abs(dx) <= Math.abs(dy)) return;
      horizontal = true;
      clearTimeout(this._cardTapTimer);
      this._swipeDir = dx < 0 ? 1 : -1;
    }
    // Горизонтальный жест забираем у скролла, но только пока он явно горизонтальный.
    if (e.cancelable) e.preventDefault();
  }, { passive: false });
  const finish = () => {
    if (horizontal) {
      // Гасим click, который браузер всё равно выстрелит после touchend:
      // иначе пост откроется ещё раз поверх вьювера.
      this._suppressCardClick = true;
      setTimeout(() => { this._suppressCardClick = false; }, 350);
      dir();
    }
    active = false; horizontal = false;
  };
  card.addEventListener('touchend', finish);
  card.addEventListener('touchcancel', () => { active = false; horizontal = false; });
};

/** @this {AppType} */
App.getCardById = function (id) {
  const el = this.els.grid.querySelector(`.card-checkbox[data-id="${id}"]`);
  return el ? el.closest('.post-card') : null;
};

/** @this {AppType} */
App.getCardByIndex = function (idx) {
  return this.els.grid.querySelector(`[data-index="${idx}"]`);
};

/** @this {AppType} */
App.createPostCard = function (post) {
  const card = document.createElement('div');
  const isQueued = this.state.downloading.has(post.id) || this.state.downloadQueue.has(post.id);
  card.className = 'post-card' + (post.downloaded ? ' downloaded' : '') + (isQueued ? ' queued' : '');
  card.dataset.id = post.id;
  card.setAttribute('tabindex', '0');
  card.setAttribute('role', 'article');

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
  const cardIdx = (typeof post._index === 'number')
    ? post._index
    : (this.state && Array.isArray(this.state.posts)
      ? (this.state.posts.findIndex(p => p.id === post.id) >= 0
        ? this.state.posts.findIndex(p => p.id === post.id)
        : this.state.posts.length)
      : 0);
  const pageSize = (typeof this.pageSize === 'function') ? this.pageSize() : 36;
  const isFirstScreen = cardIdx < Math.ceil(pageSize * 0.35);

  if (!isFirstScreen) {
    card.classList.add('cv-auto');
    const colWidth = (this.masonryCols && this.masonryCols[0] && this.masonryCols[0].offsetWidth)
      || (typeof window !== 'undefined' && window.innerWidth ? Math.round((window.innerWidth - 32) / ((typeof this.masonryColumnCount === 'function' ? this.masonryColumnCount() : 3) || 3)) : 0)
      || 300;
    const arVal = (post.width && post.height) ? Math.min(post.width / post.height, 3) : 1;
    const estHeight = Math.max(120, Math.min(900, Math.round(colWidth / (arVal || 1))));
    card.style.containIntrinsicSize = 'auto none auto ' + estHeight + 'px';
  }

  const cb = document.createElement('div');
  cb.className = 'card-checkbox' + (this.state.selected.has(post.id) ? ' checked' : '');
  cb.dataset.id = post.id;
  cb.addEventListener('click', (e) => { e.stopPropagation(); this.toggleSelect(post.id, e.shiftKey); });
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
  if (img.complete) img.classList.add('loaded');
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
  // XSS-фикс: поля file_type/score/id приходят из стороннего API (rule34) —
  // строим overlay DOM-апи вместо innerHTML + интерполяции (ранее вектор XSS).
  const left = document.createElement('div');
  left.className = 'post-overlay-left';
  const badge = document.createElement('span');
  badge.className = 'post-badge' + (post.downloaded ? ' dl-badge' : '');
  // DOM-апи вместо outerHTML-конкатенации: иконка + текст как отдельные узлы.
  badge.append(
    iconToNode(icon(post.downloaded ? 'check' : 'download', 12)),
    document.createTextNode(' ' + (post.file_type || '?'))
  );
  // Индикатор комментариев на самом источнике (dapi has_comments / кэш).
  // Числа у буров обычно нет — источник отдаёт лишь флаг, поэтому при
  // отсутствии счётчика показываем точку, а не выдуманное «1».
  if (typeof this.hasSourceComments === 'function' && this.hasSourceComments(post)) {
    const n = post.comment_count || 0;
    const sc = document.createElement('span');
    sc.className = 'src-comments-badge';
    sc.title = t('srcComments.badge');
    sc.setAttribute('aria-label', t('srcComments.badge'));
    sc.appendChild(iconToNode(icon('messageSquare', 12)));
    // Счётчик в отдельном узле: фоновой предзагрузке потом достаточно
    // обновить текст здесь, не перерисовывая иконку (updateCardCommentsBadge).
    const num = document.createElement('span');
    num.className = 'src-comments-n';
    num.textContent = n ? String(n) : '';
    sc.appendChild(num);
    badge.appendChild(sc);
  }
  left.appendChild(badge);
  overlay.appendChild(left);
  const right = document.createElement('div');
  right.className = 'post-overlay-right';
  const likeBtn = document.createElement('button');
  likeBtn.className = 'card-like-btn' + (isLiked ? ' liked' : '');
  likeBtn.dataset.id = String(post.id);
  likeBtn.title = t('card.like', { hotkey: 'Q' });
  likeBtn.appendChild(iconToNode(icon('heart', 14)));
  right.appendChild(likeBtn);
  const hideBtn = document.createElement('button');
  hideBtn.className = 'card-hide-btn' + (isHidden ? ' hidden' : '');
  hideBtn.dataset.id = String(post.id);
  hideBtn.title = t('card.hide', { hotkey: 'E' });
  hideBtn.appendChild(iconToNode(icon('heartOff', 14)));
  right.appendChild(hideBtn);
  const score = document.createElement('span');
  score.className = 'post-score';
  score.title = t('card.score');
  score.appendChild(iconToNode(icon('star', 11, true)));
  score.appendChild(document.createTextNode(String(post.score || 0)));
  right.appendChild(score);
  overlay.appendChild(right);
  card.appendChild(wrap);
  card.appendChild(overlay);

  let lastTap = 0, lastTapX = 0, lastTapY = 0, tapTimer = null;
  // Ctrl/Cmd+ЛКМ (и средняя кнопка мыши) работают как у обычной ссылки: пост
  // открывается в новой вкладке — там его подхватит вьювер по /post/<id>.
  const openInNewTab = (e) => {
    if (/** @type {HTMLElement} */ (e.target).closest('.card-checkbox')) return;
    e.preventDefault();
    this.openInNewTab(this.postUrl(this.state.query, post.id));
  };
  card.addEventListener('auxclick', (e) => {
    if (e.button !== 1) return;
    openInNewTab(e);
  });
  card.addEventListener('click', (e) => {
    if (/** @type {HTMLElement} */ (e.target).closest('.card-checkbox')) return;
    // После свайпа браузер досылает click — игнорируем, иначе пост откроется
    // второй раз поверх уже открытого вьювера.
    if (this._suppressCardClick) { e.preventDefault(); return; }
    if (this.isOpenInNewTabClick(e)) { openInNewTab(e); return; }
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
      tapTimer = setTimeout(() => this.openViewer(parseInt(card.dataset.index || '0', 10)), 250);
    } else {
      this.openViewer(parseInt(card.dataset.index || '0', 10));
    }
  });
  this._bindCardSwipe(card, () => parseInt(card.dataset.index || '0', 10));
  let videoHoverTimer;
  card.addEventListener('mouseenter', () => {
    this.state.hoveredIndex = parseInt(card.dataset.index || '0', 10);
    if (!isVideo && post.file_url && !post.downloaded) this.prefetchFull(post);
    const v = /** @type {HTMLVideoElement} */ (wrap.querySelector('.video-source'));
    const p = /** @type {HTMLElement} */ (wrap.querySelector('.video-preview'));
    if (!v) return;
    clearTimeout(videoHoverTimer);
    videoHoverTimer = setTimeout(() => {
      if (p) p.style.display = 'none';
      v.style.display = 'block';
      v.play().catch(() => {});
    }, 200);
  });
  card.addEventListener('mouseleave', () => {
    if (this.state.hoveredIndex === parseInt(card.dataset.index || '0', 10)) this.state.hoveredIndex = -1;
    clearTimeout(videoHoverTimer);
    const v = /** @type {HTMLVideoElement} */ (wrap.querySelector('.video-source'));
    const p = /** @type {HTMLElement} */ (wrap.querySelector('.video-preview'));
    if (v) { v.pause(); v.currentTime = 0; v.style.display = 'none'; if (p) p.style.display = ''; }
  });

  // Видео-превью, ушедшее из вьюпорта без mouseleave (тач-скролл,
  // перестановка карточек), останавливается общим наблюдателем.
  this.observeCardViewport(card);

  if (likeBtn) likeBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const liked = this.optimisticLike(post.id);
    likeBtn.classList.toggle('liked', liked);
    // Явное состояние — см. комментарий в App._hideWithUndo.
    API.post(`/like/${post.id}`, { liked }).catch(() => {
      this.optimisticLike(post.id, !liked);
      likeBtn.classList.toggle('liked', !liked);
    });
  });

  if (hideBtn) hideBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    this._hideWithUndo(post.id);
  });

  return card;
};

/** @this {AppType} */
App.initVideoPauseObserver = function () {
  if (this._videoPauseObserver || !('IntersectionObserver' in window)) return;
  this._videoPauseObserver = new IntersectionObserver((entries) => {
    entries.forEach((en) => {
      if (en.isIntersecting) return;
      const target = /** @type {HTMLElement} */ (en.target);
      const v = /** @type {HTMLVideoElement} */ (target.querySelector('.video-source'));
      if (v && !v.paused) {
        v.pause();
        try { v.currentTime = 0; } catch { /* noop */ }
        v.style.display = 'none';
        const p = /** @type {HTMLElement} */ (target.querySelector('.video-preview'));
        if (p) p.style.display = '';
      }
    });
  }, { rootMargin: '80px' });
};

/** @this {AppType} */
App.observeCardViewport = function (card) {
  this.initVideoPauseObserver();
  if (this._videoPauseObserver) this._videoPauseObserver.observe(card);
};

/** @this {AppType} */
App._feedDoubleTapLike = function (post, card) {
  const liked = this.optimisticLike(post.id);
  const btn = card.querySelector('.card-like-btn');
  if (btn) btn.classList.toggle('liked', liked);
  const burst = document.createElement('div');
  burst.className = 'like-burst' + (liked ? '' : ' unlike');
  burst.innerHTML = icon('heart', 40, true);
  card.appendChild(burst);
  setTimeout(() => { try { burst.remove(); } catch {} }, 750);
  API.post(`/like/${post.id}`, { liked }).catch(() => {
    this.optimisticLike(post.id, !liked);
    if (btn) btn.classList.toggle('liked', !liked);
  });
};

/** @this {AppType} */
// updateCardCommentsBadge обновляет индикатор комментариев источника на уже
// отрисованной карточке: фоновой предзагрузке пришло точное число, и вместо
// точки («есть, но неизвестно сколько») стоит показать его.
App.updateCardCommentsBadge = function (post) {
  const id = post && post.id;
  if (!id) return;
  const card = this.els.grid && this.els.grid.querySelector('.post-card[data-id="' + id + '"]');
  if (!card) return;
  const n = post.comment_count || 0;
  if (!n) return;
  // querySelector отдаёт Element: сужаем до HTMLElement, иначе .title недоступен.
  let el = /** @type {HTMLElement|null} */ (card.querySelector('.src-comments-badge'));
  if (!el) {
    // Бейдж не рисовали, когда флага не было: дорисовываем в то же место,
    // что и при создании карточки.
    const badge = card.querySelector('.post-badge');
    if (!badge) return;
    el = document.createElement('span');
    el.className = 'src-comments-badge';
    el.title = t('srcComments.badge');
    el.setAttribute('aria-label', t('srcComments.badge'));
    el.appendChild(iconToNode(icon('messageSquare', 12)));
    badge.appendChild(el);
  }
  let num = /** @type {HTMLElement|null} */ (el.querySelector('.src-comments-n'));
  if (!num) {
    num = document.createElement('span');
    num.className = 'src-comments-n';
    el.appendChild(num);
  }
  num.textContent = String(n);
};
