import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';
import { decodeBlurHash } from './blurhash.js';
const VIEWER_X_ICO = icon('x', 9);
// i18n (#11): подписи слайдшоу зависят от текущего языка — вычисляем их в
// момент вызова, а не при загрузке модуля (иначе смена языка не применится).
const SS_PLAY_ICO = () => icon('play', 16, true) + ' ' + t('viewer.slideshow');
const SS_PAUSE_ICO = () => icon('pause', 16, true) + ' ' + t('viewer.slideshow');

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
   this.els.viewer.classList.remove('hidden');
   // focusable контейнер + trap Tab внутри хедера вьювера.
   this.els.viewerContent.setAttribute('tabindex', '-1');
   if (typeof this.trapFocus === 'function') {
      this._releaseViewerTrap = this.trapFocus(this.els.viewer, this.els.viewerClose);
   }
  this._showViewerHud();
  document.body.style.overflow = 'hidden';
  this.renderViewer();
  if (!this._zoomControlsBound) { this._zoomControlsBound = true; this._bindZoomControls(); }
  if (!this._viewerNewTabBound) { this._viewerNewTabBound = true; this._bindViewerNewTab(); }
  const post = this.state.posts[index];
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
  this.els.viewer.classList.add('hidden');
  // a11y (#4): возвращаем фону скринридеры + убираем диалог-атрибуты.
  const main = document.getElementById('main');
  if (main) this.setAriaHidden(main, false);
  this.els.viewer.removeAttribute('aria-modal');
  this.els.viewerRelated.classList.add('hidden');
  document.body.style.overflow = '';
  this.els.viewerContent.classList.remove('video-loading');
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
  try { localStorage.removeItem('briefly_zoom'); } catch {}
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

App.scheduleRelated = function (post) {
  const el = this.els.viewerRelated;
  if (!el) return;
  this._relToken = (this._relToken || 0) + 1;
  this._relPosts = [];
  this._relLoadedFor = null;
  el.classList.add('hidden');
  el.innerHTML = '';
  if (!post || !post.id) return;
  // В полноэкранном режиме подвал скрыт (viewer-foot не виден) — запрос
  // похожих не отправляем, подгрузим после выхода из полного экрана.
  if (this._viewerIsFullscreen()) return;
  clearTimeout(this._relTimer);
  this._relTimer = setTimeout(() => this.loadRelated(post), 350);
};

App.loadRelated = async function (post) {
  const el = this.els.viewerRelated;
  if (!el || !post || !post.id) return;
  // Страховка: таймер из scheduleRelated мог сработать уже в полном экране.
  if (this._viewerIsFullscreen()) return;
  const token = (this._relToken = (this._relToken || 0) + 1);
  this._relPosts = [];
  el.classList.add('hidden');
  el.innerHTML = '';
  const ac = new AbortController();
  this._relAbort = ac;
  try {
    const data = await API.get('/related?id=' + encodeURIComponent(post.id), { signal: ac.signal });
    if (token !== this._relToken || !this.state.viewerOpen || ac.signal.aborted) return;
    const cur = this.state.posts[this.state.viewerIndex];
    if (!cur || cur.id !== post.id) return;
    this._relLoadedFor = post.id;
    const posts = (data && data.posts) || [];
    if (!posts.length) {
      el.classList.remove('hidden');
      el.innerHTML = '<div class="rel-title">' + t('viewer.relatedEmpty') + '</div>';
      return;
    }
    this._relPosts = posts;
    el.classList.remove('hidden');
    const tagStr = (data.tags || []).map(esc).join(', ');
    // kind=preview: immutable-заголовок и cache-first в service worker.
    const prox = (u) => u ? '/api/proxy?url=' + encodeURIComponent(u) + '&kind=preview' : null;
    el.innerHTML =
      '<div class="rel-title">' + t('viewer.relatedTitle') + (tagStr ? ': <span style="text-transform:none;font-weight:600">' + tagStr + '</span>' : '') + '</div>' +
      '<div class="rel-row">' +
      posts.map((p, i) => {
        const thumb = p.downloaded ? ('/api/thumb/' + p.id) : prox(p.preview_url);
        // XSS (#7): p.id приходит из внешнего источника — экранируем (esc
        // безопасен и в атрибутном контексте). A11y (#13): rel-item — кнопка
        // с ролью и фокусом, иначе ряд «Похожие» недоступен с клавиатуры.
        const pid = esc(p.id);
        return `<div class="rel-item" role="button" tabindex="0" data-idx="${i}" aria-label="#${pid}" title="#${pid}">${thumb ? `<img src="${thumb}" alt="" loading="lazy" decoding="async">` : ''}<span class="rel-id">#${pid}</span></div>`;
      }).join('') +
      '</div>';
  } catch (err) {
    if (token !== this._relToken || !this.state.viewerOpen || ac.signal.aborted) return;
    const cur = this.state.posts[this.state.viewerIndex];
    if (!cur || cur.id !== post.id) return;
    el.classList.remove('hidden');
    el.innerHTML = '<div class="rel-title">' + t('viewer.relatedError') + '</div>';
  } finally {
    if (this._relAbort === ac) this._relAbort = null;
  }
};

// Perf (#9): вместо 4 слушателей на каждый тег при каждом посте — 4
// делегированных слушателя на контейнер, навешиваются один раз.
App._bindViewerTagDelegation = function () {
  const host = this.els.viewerTags;
  if (!host || typeof host.addEventListener !== 'function' || host._tagDelegated) return;
  host._tagDelegated = true;
  // Тег хранится экспандо-свойством (надёжнее dataset для тестового DOM).
  const tagOf = (span) => (span && span._brieflyTag) || '';
  const hideTag = (span) => {
    const tag = tagOf(span);
    API.post('/hidden-tag', { tag }).then((r) => {
      API.invalidate('/profile');
      this.invalidateFeedCache();
      this.loadProfile();
      this._viewerTagChanged = true;
      span.classList.toggle('tag-hidden');
      this.showToast(r.hidden ? tf('viewer.tagHidden', { tag }) : tf('viewer.tagShown', { tag }));
    }).catch(() => {});
  };
  const favTag = (span) => {
    const tag = tagOf(span);
    API.post('/fav-tag', { tag }).then(() => {
      API.invalidate('/profile');
      this.invalidateFeedCache();
      this.loadProfile();
      span.classList.toggle('tag-fav');
    }).catch(() => {});
  };
  const openTag = (span) => {
    const tag = tagOf(span);
    const current = this.state.query;
    const newQ = current ? `${current} +${tag}` : `+${tag}`;
    this.closeViewer({ keepUrl: true });
    this.els.searchInput.value = newQ;
    this.search(newQ);
  };
  const inXBtn = (e) => !!(e.target.closest && e.target.closest('.tag-hide-btn'));
  const inTag = (e) => e.target.closest && e.target.closest('.viewer-tag');
  host.addEventListener('click', (e) => {
    const span = inTag(e);
    if (!span) return;
    if (inXBtn(e)) { hideTag(span); return; }
    openTag(span);
  });
  host.addEventListener('dblclick', (e) => {
    const span = inTag(e);
    if (!span || inXBtn(e)) return;
    e.stopPropagation();
    favTag(span);
  });
  host.addEventListener('contextmenu', (e) => {
    const span = inTag(e);
    if (!span) return;
    e.preventDefault();
    hideTag(span);
  });
  // A11y (#15): теги фокусируемы — Enter/Space активируют как клик.
  host.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const span = inTag(e);
    if (!span) return;
    e.preventDefault();
    if (inXBtn(e)) { hideTag(span); return; }
    openTag(span);
  });
};

App._renderViewerTags = function () {
  const post = this.state.posts[this.state.viewerIndex];
  const viewerTags = this.els.viewerTags;
  if (!viewerTags) return;
  viewerTags.innerHTML = '';
  if (!post || !post.tags) return;
  this._bindViewerTagDelegation();
  const allTags = post.tags.split(' ').filter(Boolean);
  const isFav = t => this.state.profile.fav_tags && this.state.profile.fav_tags.includes(t);
  const isHid = t => this.state.profile.hidden_tags && this.state.profile.hidden_tags.includes(t);
  const qTokens = (this.state.query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const isQ = t => qTokens.some(tok => t.toLowerCase() === tok.replace(/^[+-]/, '').toLowerCase());
  const countSpans = [];
  const needCount = [];
  allTags.forEach(tag => {
    const span = document.createElement('span');
    span.className = 'viewer-tag' + (isFav(tag) ? ' tag-fav' : '') + (isHid(tag) ? ' tag-hidden' : '') + (isQ(tag) ? ' tag-query' : '');
    span._brieflyTag = tag;
    // A11y (#15): тег — интерактивный элемент: роль + фокус с клавиатуры.
    span.tabIndex = 0;
    try { span.setAttribute('role', 'button'); } catch { /* тестовый DOM без setAttribute */ }
    try { span.setAttribute('aria-label', tag); } catch { /* noop */ }
    const text = document.createElement('span');
    text.textContent = tag;
    span.appendChild(text);
    const countSpan = document.createElement('span');
    countSpan.className = 'tag-count';
    if (this._tagCounts && this._tagCounts[tag] != null) {
      countSpan.textContent = this._tagCounts[tag];
    } else {
      needCount.push(tag);
    }
    span.appendChild(countSpan);
    countSpans.push({ tag, el: countSpan });
    const xBtn = document.createElement('button');
    xBtn.className = 'tag-hide-btn';
    xBtn.innerHTML = VIEWER_X_ICO;
    // A11y (#12): SVG-иконка без текста — даём кнопке имя для скринридеров.
    xBtn.title = t('viewer.tagHide');
    try { xBtn.setAttribute('aria-label', t('viewer.tagHide')); } catch { /* тестовый DOM */ }
    span.appendChild(xBtn);
    viewerTags.appendChild(span);
  });
  // В полном экране подвал скрыт — счётчики не запрашиваем; они подтянутся
  // при выходе из полного экрана (там повторно вызывается _renderViewerTags).
  if (needCount.length && !this._viewerIsFullscreen()) {
    this._loadViewerTagCounts(needCount, countSpans);
  }
};

// _bindRelChipOpen — обработчики chip «родитель/дети»: Ctrl/Cmd+ЛКМ и средняя
// кнопка мыши работают как у обычной ссылки — пост открывается в новой вкладке
// (там его подхватит вьювер по /post/<id>); обычный клик — прежний переход.
App._bindRelChipOpen = function (chip, id) {
  const openNewTab = (ev) => {
    ev.preventDefault();
    this.openInNewTab(this.postUrl(this.state.query, id));
  };
  chip.addEventListener('click', (ev) => {
    if (this.isOpenInNewTabClick(ev)) { openNewTab(ev); return; }
    this._openPostById(id);
  });
  // auxclick: средняя кнопка мыши (button === 1); ЛКМ (button === 0) игнорируем.
  chip.addEventListener('auxclick', (ev) => {
    if (ev.button !== 1) return;
    openNewTab(ev);
  });
};

// _renderViewerRelations — chip-ряд «родитель/дети» для скачанных постов.
// Клик по chip открывает пост (в ленте, если он там есть, иначе поиском id:N),
// Ctrl/Cmd+ЛКМ или средняя кнопка — в новой вкладке (_bindRelChipOpen).
// Кнопка «↑ привязать» задаёт родителя текущему посту (danbooru-style связки).
App._renderViewerRelations = function (post) {
  const host = this.els.relations;
  if (!host) return;
  if (!post || !post.id) { host.classList.add('hidden'); return; }
  // Race (#1), дедуп: renderViewer вызывается и повторно для того же поста —
  // не дёргаем API, если chips уже отрисованы. Принудительное обновление
  // (после «привязать») сбрасывает _relChipsFor.
  if (this._relChipsFor === post.id) return;
  // Race (#1): token + AbortController + проверка актуальности поста после
  // await — как в loadRelated. Иначе ответ прошлого поста при быстрой
  // навигации перезапишет chips-ряд «родитель/дети» от чужого поста.
  const token = (this._relChipsToken = (this._relChipsToken || 0) + 1);
  if (this._relChipsAbort) { this._relChipsAbort.abort(); }
  const ac = new AbortController();
  this._relChipsAbort = ac;
  this._relChipsFor = post.id;
  const relevant = () => {
    if (token !== this._relChipsToken || !this.state.viewerOpen || ac.signal.aborted) return false;
    const cur = this.state.posts[this.state.viewerIndex];
    return !!(cur && cur.id === post.id);
  };
  API.get(`/posts/${post.id}/relations`, { signal: ac.signal }).then(d => {
    if (!relevant() || !d) return;
    host.innerHTML = '';
    const parent = d.parent;
    const children = d.children || [];
    if (parent || children.length) {
      host.classList.remove('hidden');
    } else {
      host.classList.add('hidden');
    }
    if (parent) {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'rel-chip rel-parent';
      chip.title = t('viewer.relParent');
      chip.textContent = `↑ #${parent.id}${parent.file_type ? ' · ' + parent.file_type : ''}`;
      this._bindRelChipOpen(chip, parent.id);
      host.appendChild(chip);
    }
    children.forEach(ch => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'rel-chip rel-child';
      chip.title = t('viewer.relChild');
      chip.textContent = `↓ #${ch.id}${ch.file_type ? ' · ' + ch.file_type : ''}`;
      this._bindRelChipOpen(chip, ch.id);
      host.appendChild(chip);
    });
    if (!parent && !children.length) {
      // Маленькая кнопка «привязать» — пост без семьи можно сделать ребёнком.
      const setBtn = document.createElement('button');
      setBtn.type = 'button';
      setBtn.className = 'rel-chip rel-set';
      setBtn.title = t('viewer.relSetHint');
      setBtn.textContent = t('viewer.relSetBtn');
      setBtn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        // A11y (#14): window.prompt блокирует страницу, не стилизуется и
        // выпадает из focus-trap — используем модальный promptDialog
        // (нативный prompt оставлен как fallback для тестового окружения).
        const ask = (typeof this.promptDialog === 'function')
          ? this.promptDialog({ title: t('viewer.parentPrompt'), okText: t('viewer.relSetHint') })
          : Promise.resolve(window.prompt(t('viewer.parentPrompt')));
        ask.then((input) => {
          if (!input) return;
          const pid = parseInt(input, 10);
          if (!Number.isFinite(pid) || pid <= 0) { this.showToast(t('viewer.badId'), 'error'); return; }
          API.post(`/posts/${post.id}/parent`, { parent_id: pid }).then(() => {
            this.showToast(tf('viewer.linked', { id: post.id, pid }));
            this._relChipsFor = null; // форс-обновление chips
            this._renderViewerRelations(post);
          }).catch(err => this.showToast(`${t('viewer.error')}: ${err.message}`, 'error'));
        });
      });
      host.appendChild(setBtn);
    }
  }).catch(() => {
    // Ошибка прошлого запроса не должна прятать chips текущего поста.
    if (!relevant()) return;
    host.classList.add('hidden');
  }).finally(() => {
    if (this._relChipsAbort === ac) this._relChipsAbort = null;
  });
};

App._openPostById = function (id) {
  if (!id) return;
  const idx = this.state.posts.findIndex(p => p.id === id);
  if (idx >= 0) { this._gotoViewerIndex(idx, 0); return; }
  const q = `id:${id}`;
  this.closeViewer({ keepUrl: true });
  this.els.searchInput.value = q;
  this.search(q);
};

App._loadViewerTagCounts = function (tags, spans) {
  if (!tags || !tags.length || !spans || !spans.length) return;
  // Режим «Все сайты»: выдача смешанная, а счётчики считает только
  // дефолтный источник — не показываем вводящие в заблуждение числа.
  if (this.state.activeProvider === 'all') return;
  if (!this._tagCounts) this._tagCounts = {};
  if (this._countAbort) { this._countAbort.abort(); this._countAbort = null; }
  const ac = new AbortController();
  this._countAbort = ac;
  const q = tags.join(',');
  API.get(`/tag-counts?tags=${encodeURIComponent(q)}`, { signal: ac.signal }).then(d => {
    if (ac.signal.aborted || !d || !d.counts) return;
    // Копируем по ключам с фильтром: Object.assign на json-объекте может
    // протащить ключ "__proto__" из ответа (prototype pollution).
    for (const k of Object.keys(d.counts)) {
      if (k === '__proto__' || k === 'constructor' || k === 'prototype') continue;
      this._tagCounts[k] = d.counts[k];
    }
    this._saveTagCounts();
    spans.forEach(({ tag, el }) => {
      const c = this._tagCounts[tag];
      if (c != null) el.textContent = c;
    });
  }).catch(() => {}).finally(() => {
    if (this._countAbort === ac) this._countAbort = null;
  });
};
App._tagCountsCacheKey = 'briefly_tag_counts_v2'; // v2: сброс старого кэша с фальшивыми нулями (баг prefix-only GetTagCount)

App._loadTagCounts = function () {
  try {
    const c = JSON.parse(localStorage.getItem(this._tagCountsCacheKey) || 'null');
    if (c && c.counts && Date.now() - c.ts < 12 * 60 * 60 * 1000) {
      this._tagCounts = Object.assign({}, c.counts);
      return;
    }
  } catch {}
  this._tagCounts = {};
};

App._saveTagCounts = function () {
  // Memory (#5): кэш счётчиков не должен расти неограниченно — при переполнении
  // выбрасываем ключи с наименьшими счётчиками (иначе при долгом использовании
  // копятся десятки тысяч ключей и синхронная сериализация на каждый запрос).
  const tc = this._tagCounts;
  if (tc) {
    const keys = Object.keys(tc);
    const max = 4000;
    if (keys.length > max) {
      keys.sort((a, b) => (tc[a] || 0) - (tc[b] || 0));
      for (let i = 0; i < keys.length - max; i++) delete tc[keys[i]];
    }
  }
  try {
    localStorage.setItem(this._tagCountsCacheKey, JSON.stringify({ ts: Date.now(), counts: this._tagCounts }));
  } catch {}
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
  const videoPosterUrl = post.preview_url
    ? `/api/proxy?url=${encodeURIComponent(post.preview_url)}`
    : `/api/thumb/${post.id}`;
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
      viewerContent.appendChild(img);
      mediaEl = img;
    }
    if (mediaEl.src !== resolvedFileUrl) {
      this._showBlurhashPlaceholder(post, viewerContent);
      mediaEl.src = fileUrl;
      mediaEl.classList.remove('fade-in');
      void mediaEl.offsetWidth;
      mediaEl.classList.add('fade-in');
      mediaEl.addEventListener('load', () => {
        this._hideBlurhashPlaceholder(viewerContent);
        viewerLoader.classList.remove('active');
        this.applyZoomTransform();
      }, { once: true });
      mediaEl.addEventListener('error', () => this._hideBlurhashPlaceholder(viewerContent), { once: true });
    } else {
      this._hideBlurhashPlaceholder(viewerContent);
      viewerLoader.classList.remove('active');
    }
    if (heavyImage && originalUrl && !viewerContent.querySelector('.viewer-orig')) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'viewer-orig';
      btn.textContent = '1:1';
      btn.title = 'Догрузить оригинал (крупнее и тяжелее)';
      btn.style.cssText = 'position:absolute;top:10px;right:10px;z-index:6;padding:4px 10px;cursor:pointer;';
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
      '<select class="btn-ss ss-rate" title="Скорость видео" aria-label="Скорость видео">' +
      [0.5, 0.75, 1, 1.25, 1.5, 2].map(r => `<option value="${r}">${r}×</option>`).join('') +
      '</select>' +
      `<button type="button" class="btn-ss btn-pip" title="Картинка в картинке">${icon('pip', 16)}</button>` +
      `<button type="button" class="btn-ss" id="slideshow-btn">${SS_PLAY_ICO()}</button>`;
    barHost.appendChild(ssBar);
    this.els.slideshowBtn = ssBar.querySelector('#slideshow-btn');
    this.els.slideshowBtn.addEventListener('click', () => this.toggleSlideshow());
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

  this._renderViewerTags();
  this._renderViewerRelations(post);

  this.els.viewerLike.innerHTML = isLiked
    ? icon('heart', 20, true)
    : icon('heart', 20);
  if (this.els.viewerLikeM) this.els.viewerLikeM.innerHTML = this.els.viewerLike.innerHTML;

  viewerProgress.textContent = post.downloaded ? t('viewer.downloaded') : t('viewer.pressX');
  this.updateNavButtons();
  if (typeof this.renderComments === 'function') this.renderComments(post.id);
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
  this._stashCurrentVideoTime();
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

App._warmImage = function (url) {
  if (!url) return;
  try {
    const img = new Image();
    img.decoding = 'async';
    img.src = url;
  } catch {  }
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
  this.showToast(liked ? 'Лайкнут' : 'Лайк убран');
  API.post(`/like/${post.id}`).catch(() => {
    this.optimisticLike(post.id, !liked);
    this._syncLikeIcons(!liked);
    this.showToast('Ошибка', 'error');
  });
};

App._syncLikeIcons = function (liked) {
  const html = liked
    ? icon('heart', 20, true)
    : icon('heart', 20);
  this.els.viewerLike.innerHTML = html;
  if (this.els.viewerLikeM) this.els.viewerLikeM.innerHTML = html;
};

// Скрытие/показ поста с серверной синхронизацией и кнопкой «Отмена».
// Undo гарантированно снимает hide на сервере: ждём завершения исходного
// toggle и по фактическому ответу решаем, нужен ли компенсирующий POST
// (раньше undo до ответа сервера оставлял пост скрытым навсегда).
App._hideWithUndo = function (postId) {
  const hidden = this.optimisticHide(postId);
  const req = API.post(`/hide/${postId}`).catch(() => null);
  if (hidden) {
    this.removeHiddenFromFeed(postId);
    this.showToastWithUndo('Пост скрыт', () => {
      this.optimisticHide(postId, false);
      this.restoreHiddenPost(postId);
      req.then(res => {
        if (res && res.hidden) return API.post(`/hide/${postId}`);
        return null;
      }).then(() => { API.invalidate('/profile'); this.loadProfile(); }).catch(() => {});
    });
  } else {
    const rec = this._lastRemoved;
    if (rec && rec.post.id === postId) this.restoreHiddenPost(postId);
    this.showToast('Пост показан');
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
  API.post(`/like/${post.id}`).catch(() => {
    this.optimisticLike(post.id, !liked);
    this.showToast('Ошибка', 'error');
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
        title: 'Нет постов',
        subtitle: 'Попробуйте изменить фильтры',
        actions: [{ key: 'reset', label: 'Сбросить фильтры' }, { key: 'random', label: 'Случайный пост' }],
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
  if (!post || !post.file_url) { this.showToast('Нет файла для скачивания', 'error'); return; }
  const a = document.createElement('a');
  a.href = `/api/save/${post.id}`;
  a.download = `${post.id}.${post.file_type || 'bin'}`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  this.showToast(`Скачивание #${post.id} на устройство`);
};

App.toggleInvert = function () {
  const el = this.els.viewerContent.querySelector('img, video');
  if (!el) return;
  const inv = el.style.filter === 'invert(1)' ? '' : 'invert(1)';
  el.style.filter = inv;
  this.showToast(inv ? 'Инвертировано' : 'Инверсия снята');
};

App._ensureZoomState = function () {
  if (this._zoomScale == null) {
    let sc = 1, active = false;
    try { const s = JSON.parse(localStorage.getItem('briefly_zoom') || 'null'); if (s && typeof s.scale === 'number') { sc = s.scale; active = !!s.active; } } catch {}
    this._zoomScale = sc; this._zoomTx = 0; this._zoomTy = 0; this._zoomActive = active;
  }
};

App._maxZoom = function () { return 10; }; 

App._persistZoom = function () {
  try { localStorage.setItem('briefly_zoom', JSON.stringify({ active: this._zoomActive, scale: this._zoomScale || 1 })); } catch {}
};

App._cancelInertia = function () {
  if (this._inertiaRaf) { cancelAnimationFrame(this._inertiaRaf); this._inertiaRaf = null; }
};

App._smoothZoom = function () {
  this._zoomTransition = true;
  clearTimeout(this._zoomSmoothTimer);
  this._zoomSmoothTimer = setTimeout(() => { this._zoomTransition = false; }, 160);
};

App._zoomMetrics = function () {
  const vc = this.els.viewerContent;
  const W = vc.clientWidth || vc.parentElement.clientWidth || 1;
  const H = vc.clientHeight || vc.parentElement.clientHeight || 1;
  const img = this.els.viewerContent.querySelector('img');
  let nw = img ? (img.naturalWidth || 0) : 0;
  let nh = img ? (img.naturalHeight || 0) : 0;
  if (nw <= 0 || nh <= 0) { nw = W; nh = H; }
  const ar = nw / nh;
  let fw, fh;
  if (ar >= W / H) { fw = W; fh = W / ar; } else { fh = H; fw = H * ar; }
  return { W, H, fw, fh };
};

App._zoomMaxPan = function (s) {
  const { W, H, fw, fh } = this._zoomMetrics();
  return {
    maxTX: Math.max(0, (s * fw - W) / 2),
    maxTY: Math.max(0, (s * fh - H) / 2),
  };
};

App.applyZoomTransform = function () {
  this._ensureZoomState();
  const img = this.els.viewerContent.querySelector('img');
  if (!img) return;
  const { fw, fh } = this._zoomMetrics();
  const s = this._zoomActive ? (this._zoomScale || 1) : 1;
  const active = this._zoomActive && s > 1;
  const { maxTX, maxTY } = this._zoomMaxPan(s);
  if (!active) { this._zoomTx = 0; this._zoomTy = 0; }
  this._zoomTx = Math.min(maxTX, Math.max(-maxTX, this._zoomTx || 0));
  this._zoomTy = Math.min(maxTY, Math.max(-maxTY, this._zoomTy || 0));
  img.style.objectFit = 'contain';
  img.style.objectPosition = 'center';
  img.style.transformOrigin = 'center center';
  img.style.width = fw + 'px';
  img.style.height = fh + 'px';
  img.style.transition = this._zoomTransition ? 'transform 0.15s ease-out' : 'none';
  img.style.transform = active ? `translate(${this._zoomTx}px, ${this._zoomTy}px) scale(${s})` : 'none';
  img.style.cursor = active ? 'grab' : 'zoom-in';
  this._updateZoomHud(s, active);
  this._updateZoomLabel(s);
};

App._updateZoomHud = function (s, active) {
  if (!this.els.zThumbX || !this.els.zThumbY || !this.els.zoomHud) return;
  s = s || (this._zoomScale || 1);
  const { W, H, fw, fh } = this._zoomMetrics();
  const { maxTX, maxTY } = this._zoomMaxPan(s);
  let hx = 0.5, hy = 0.5;
  if (maxTX > 0) hx = 0.5 - (this._zoomTx || 0) / (2 * maxTX);
  if (maxTY > 0) hy = 0.5 - (this._zoomTy || 0) / (2 * maxTY);
  const pxW = Math.min(100, (W / (s * fw)) * 100);
  const pxH = Math.min(100, (H / (s * fh)) * 100);
  this.els.zThumbX.style.width = pxW + '%';
  this.els.zThumbX.style.left = Math.max(0, Math.min(100 - pxW, hx * 100 - pxW / 2)) + '%';
  this.els.zThumbY.style.height = pxH + '%';
  this.els.zThumbY.style.top = Math.max(0, Math.min(100 - pxH, hy * 100 - pxH / 2)) + '%';
  this.els.zoomHud.classList.toggle('on', !!active);
};

App._updateZoomLabel = function (s) {
  if (this.els.zoomLabel) this.els.zoomLabel.textContent = Math.round((s || this._zoomScale || 1) * 100) + '%';
};

App.panBy = function (dxPx, dyPx) {
  if (!this._zoomActive) return;
  this._ensureZoomState();
  const s = this._zoomScale || 1;
  const { maxTX, maxTY } = this._zoomMaxPan(s);
  this._zoomTx = Math.min(maxTX, Math.max(-maxTX, (this._zoomTx || 0) + dxPx));
  this._zoomTy = Math.min(maxTY, Math.max(-maxTY, (this._zoomTy || 0) + dyPx));
  this.applyZoomTransform();
};

App.zoomBy = function (factor, cx, cy) {
  this._ensureZoomState();
  if (!this._zoomActive) return;
  const { W, H } = this._zoomMetrics();
  const s = this._zoomScale;
  const ns = Math.min(this._maxZoom(), Math.max(1, s * factor));
  if (ns === s) return;
  const cxRel = (cx != null && isFinite(cx) ? cx : W / 2) - W / 2;
  const cyRel = (cy != null && isFinite(cy) ? cy : H / 2) - H / 2;
  this._zoomTx = (this._zoomTx || 0) * (ns / s) + cxRel * (1 - ns / s);
  this._zoomTy = (this._zoomTy || 0) * (ns / s) + cyRel * (1 - ns / s);
  this._zoomScale = ns;
  this._smoothZoom();
  this.applyZoomTransform();
  this._persistZoom();
};

App.zoomToEdge = function (axis, pos) {
  this._ensureZoomState();
  if (!this._zoomActive) return;
  const { maxTX, maxTY } = this._zoomMaxPan(this._zoomScale || 1);
  const val = pos ? (axis ? maxTY : maxTX) : (axis ? -maxTY : -maxTX);
  if (axis) this._zoomTy = val; else this._zoomTx = val;
  this.applyZoomTransform();
};

App.panPage = function (dir) {
  this._ensureZoomState();
  if (!this._zoomActive) return;
  const { H } = this._zoomMetrics();
  this.panBy(0, -dir * H * 0.6);
};

App._startPanInertia = function (vx, vy) {
  this._cancelInertia();
  const dec = 0.9;
  const tick = () => {
    if (Math.abs(vx) < 0.4 && Math.abs(vy) < 0.4) { this._inertiaRaf = null; return; }
    if (!this._zoomActive) { this._inertiaRaf = null; return; }
    this.panBy(vx, vy);
    vx *= dec; vy *= dec;
    this._inertiaRaf = requestAnimationFrame(tick);
  };
  this._inertiaRaf = requestAnimationFrame(tick);
};

App._panKeys = null;

App.DIR = { UP: 'up', DOWN: 'down', LEFT: 'left', RIGHT: 'right' };

App._setPanDirection = function (dir, pressed) {
  if (!this._panKeys) this._panKeys = new Set();
  if (pressed) this._panKeys.add(dir); else this._panKeys.delete(dir);
  this._updatePanVelocity();
};

App._updatePanVelocity = function () {
  const k = this._panKeys || new Set();
  if (!this._panVel) this._panVel = { x: 0, y: 0 };
  this._panVel.x = (k.has(App.DIR.RIGHT) ? 1 : 0) - (k.has(App.DIR.LEFT) ? 1 : 0);
  this._panVel.y = (k.has(App.DIR.DOWN) ? 1 : 0) - (k.has(App.DIR.UP) ? 1 : 0);
  if (this._panVel.x || this._panVel.y) {
    if (!this._panRaf) this._runPanLoop();
  } else if (this._panRaf) {
    cancelAnimationFrame(this._panRaf);
    this._panRaf = null;
  }
};

App._runPanLoop = function () {
  const tick = () => {
    if (!this._zoomActive || !this.state.viewerOpen) { this._panRaf = null; return; }
    const vx = this._panVel ? this._panVel.x : 0;
    const vy = this._panVel ? this._panVel.y : 0;
    if (!vx && !vy) { this._panRaf = null; return; }
    const { W, H } = this._zoomMetrics();
    const px = -vx * W * 0.01; 
    const py = -vy * H * 0.01;
    if (px || py) this.panBy(px, py);
    this._panRaf = requestAnimationFrame(tick);
  };
  this._panRaf = requestAnimationFrame(tick);
};

App.startPan = function (dx, dy) {
  if (!this._zoomActive) return;
  this._cancelInertia();
  if (dx) this._setPanDirection(dx > 0 ? App.DIR.RIGHT : App.DIR.LEFT, true);
  if (dy) this._setPanDirection(dy > 0 ? App.DIR.DOWN : App.DIR.UP, true);
};

App.stopPan = function (dir) {
  this._setPanDirection(dir, false);
};

App._cancelPan = function () {
  if (this._panRaf) { cancelAnimationFrame(this._panRaf); this._panRaf = null; }
  if (this._panKeys) this._panKeys.clear();
  if (this._panVel) { this._panVel.x = 0; this._panVel.y = 0; }
};

App._resetFullscreen = function () {
  this.els.viewerContent.classList.remove('fullscreen');
  this.els.viewerHead.style.display = '';
  this.els.viewerFoot.style.display = '';
  this.els.prevBtn.style.display = '';
  this.els.nextBtn.style.display = '';
};

App.toggleFullscreen = function () {
  const vc = this.els.viewerContent;
  if (!vc.classList.contains('fullscreen')) {
    vc.classList.add('fullscreen');
    this.els.viewerHead.style.display = 'none';
    this.els.viewerFoot.style.display = 'none';
    this.els.prevBtn.style.display = 'none';
    this.els.nextBtn.style.display = 'none';
    // Шапка и подвал (теги, «Похожие») скрыты — отменяем в-полёте запросы
    // похожих и счётчиков тегов, чтобы не расходовать лимиты API впустую.
    this._relToken = (this._relToken || 0) + 1;
    if (this._relAbort) { this._relAbort.abort(); this._relAbort = null; }
    if (this._countAbort) { this._countAbort.abort(); this._countAbort = null; }
  } else {
    vc.classList.remove('fullscreen');
    this.els.viewerHead.style.display = '';
    this.els.viewerFoot.style.display = '';
    this.updateNavButtons();
    // Восстанавливаем скрытое в полном экране: теги (при необходимости
    // догрузят счётчики) и похожие для текущего поста, если ещё не загружены.
    this._renderViewerTags();
    const post = this.state.posts[this.state.viewerIndex];
    if (post && post.id && this._relLoadedFor !== post.id) this.scheduleRelated(post);
  }
  this.applyZoomTransform();
};

App.toggleViewerZoom = function () {
  this._ensureZoomState();
  this._cancelInertia();
  if (this._zoomActive) {
    this._zoomActive = false;
  } else {
    this._zoomActive = true;
    this._zoomScale = Math.max(this._zoomScale || 1, 2);
    this._zoomTx = 0; this._zoomTy = 0; 
  }
  this._smoothZoom();
  this.applyZoomTransform();
  this._persistZoom();
};

App._bindZoomControls = function () {
  const bind = (id, fn) => { const el = this.els[id]; if (el) el.addEventListener('click', fn); };
  bind('zoomIn', () => { this._ensureZoomState(); if (!this._zoomActive) { this._zoomActive = true; } this.zoomBy(1.25); });
  bind('zoomOut', () => { if (this._zoomActive) this.zoomBy(1 / 1.25); });
  bind('zoomFit', () => { if (this._zoomActive) this.toggleViewerZoom(); else this.applyZoomTransform(); });
};

// Ctrl/Cmd+ЛКМ (и средняя кнопка) по медиа во вьювере — открыть файл в новой
// вкладке, как обычную ссылку. Обычный клик не перехватываем: он управляет
// HUD/зумом, а Ctrl+ЛКМ в браузере всегда означает «открыть в новой вкладке».
App._bindViewerNewTab = function () {
  const mediaOf = (e) => (e.target && e.target.closest ? e.target.closest('img, video') : null);
  const openFile = (e) => {
    const post = this.state.posts[this.state.viewerIndex];
    if (!post) return;
    e.preventDefault();
    this.openInNewTab(this._viewerMediaPlan(post).url);
  };
  this.els.viewerContent.addEventListener('click', (e) => {
    if (!this.isOpenInNewTabClick(e) || !mediaOf(e)) return;
    openFile(e);
  });
  this.els.viewerContent.addEventListener('auxclick', (e) => {
    if (e.button !== 1 || !mediaOf(e)) return;
    openFile(e);
  });
};
