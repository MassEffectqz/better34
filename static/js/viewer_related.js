import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

const VIEWER_X_ICO = icon('x', 9);

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

  let savedMode = 'all';
  try { savedMode = localStorage.getItem('briefly_rel_mode') || 'all'; } catch {}
  let savedRating = '';
  try { savedRating = localStorage.getItem('briefly_rel_rating') || ''; } catch {}

  this._relCurrentMode = savedMode;
  this._relCurrentRating = savedRating;

  clearTimeout(this._relTimer);
  this._relTimer = setTimeout(() => this.loadRelated(post, savedMode, savedRating), 350);
};

App.loadRelated = async function (post, mode, rating) {
  const el = this.els.viewerRelated;
  if (!el || !post || !post.id) return;
  // Страховка: таймер из scheduleRelated мог сработать уже в полном экране.
  if (this._viewerIsFullscreen()) return;

  if (mode === undefined || mode === null) {
    mode = this._relCurrentMode;
    if (!mode) {
      try { mode = localStorage.getItem('briefly_rel_mode') || 'all'; } catch { mode = 'all'; }
    }
  }
  if (rating === undefined || rating === null) {
    rating = this._relCurrentRating;
    if (rating === undefined || rating === null) {
      try { rating = localStorage.getItem('briefly_rel_rating') || ''; } catch { rating = ''; }
    }
  }
  this._relCurrentMode = mode;
  this._relCurrentRating = rating;

  const token = (this._relToken = (this._relToken || 0) + 1);
  this._relPosts = [];
  if (this._relAbort) {
    this._relAbort.abort();
    this._relAbort = null;
  }
  const ac = new AbortController();
  this._relAbort = ac;
  try {
    let url = '/related?id=' + encodeURIComponent(post.id) + '&mode=' + encodeURIComponent(mode);
    if (rating) url += '&rating=' + encodeURIComponent(rating);
    const data = await API.get(url, { signal: ac.signal });
    if (token !== this._relToken || !this.state.viewerOpen || ac.signal.aborted) return;
    const cur = this.state.posts[this.state.viewerIndex];
    if (!cur || cur.id !== post.id) return;
    this._relLoadedFor = post.id;
    const posts = (data && data.posts) || [];
    this._relPosts = posts;
    el.classList.remove('hidden');

    const activeMode = (data && data.mode) || mode || 'all';
    const activeRating = (data && data.rating !== undefined) ? data.rating : rating;
    const tagList = (data && data.tags) || [];
    const tagStr = tagList.map(esc).join(', ');
    const tagQuery = tagList.join(' ');

    let tabsHtml = '<div class="rel-tabs rel-mode-tabs" role="tablist" aria-label="Similar modes">' +
      `<button type="button" class="rel-tab ${activeMode === 'all' ? 'active' : ''}" data-mode="all" role="tab" aria-selected="${activeMode === 'all'}">${t('viewer.relatedAll')}</button>`;
    if (data && (data.has_artist || activeMode === 'artist')) {
      tabsHtml += `<button type="button" class="rel-tab ${activeMode === 'artist' ? 'active' : ''}" data-mode="artist" role="tab" aria-selected="${activeMode === 'artist'}">${t('viewer.relatedArtist')}</button>`;
    }
    if (data && (data.has_character || activeMode === 'char')) {
      tabsHtml += `<button type="button" class="rel-tab ${activeMode === 'char' ? 'active' : ''}" data-mode="char" role="tab" aria-selected="${activeMode === 'char'}">${t('viewer.relatedChar')}</button>`;
    }
    tabsHtml += `<button type="button" class="rel-tab ${activeMode === 'visual' ? 'active' : ''}" data-mode="visual" role="tab" aria-selected="${activeMode === 'visual'}">${t('viewer.relatedVisual')}</button>` +
      '</div>';

    const ratingHtml = '<div class="rel-tabs rel-rating-tabs" role="radiogroup" aria-label="Rating filter">' +
      `<button type="button" class="rel-tab ${!activeRating ? 'active' : ''}" data-rating="" role="radio" aria-checked="${!activeRating}">${t('viewer.relatedRatingAll')}</button>` +
      `<button type="button" class="rel-tab ${activeRating === 'sfw' ? 'active' : ''}" data-rating="sfw" role="radio" aria-checked="${activeRating === 'sfw'}">${t('viewer.relatedRatingSfw')}</button>` +
      `<button type="button" class="rel-tab ${activeRating === 'nsfw' ? 'active' : ''}" data-rating="nsfw" role="radio" aria-checked="${activeRating === 'nsfw'}">${t('viewer.relatedRatingNsfw')}</button>` +
      '</div>';

    const searchBtnHtml = tagQuery
      ? `<button type="button" class="rel-search-btn" data-tags="${esc(tagQuery)}" title="${t('viewer.relatedSearchHint')}">${icon('search', 13)} <span class="rel-tag-highlight">${tagStr}</span></button>`
      : '';

    const headHtml = '<div class="rel-head">' +
      '<div class="rel-title-group">' +
        `<span class="rel-title">${t('viewer.relatedTitle')}</span>` +
        searchBtnHtml +
      '</div>' +
      '<div class="rel-controls">' + tabsHtml + ratingHtml + '</div>' +
      '</div>';

    if (!posts.length) {
      el.innerHTML = headHtml + '<div class="rel-empty">' + t('viewer.relatedEmpty') + '</div>';
      return;
    }

    // kind=preview: immutable-заголовок и cache-first в service worker.
    const prox = (u) => u ? '/api/proxy?url=' + encodeURIComponent(u) + '&kind=preview' : null;
    el.innerHTML =
      headHtml +
      '<div class="rel-row">' +
      posts.map((p, i) => {
        const thumb = p.downloaded ? ('/api/thumb/' + p.id) : prox(p.preview_url);
        const pid = esc(p.id);
        const sim = (p.similarity != null && p.similarity > 0)
          ? `<span class="rel-sim">${p.similarity}%</span>`
          : '';
        let badge = '';
        if (p.match_type === 'artist') {
          badge = `<span class="rel-badge badge-artist" title="${t('viewer.relatedArtist')}">art</span>`;
        } else if (p.match_type === 'character') {
          badge = `<span class="rel-badge badge-char" title="${t('viewer.relatedChar')}">char</span>`;
        } else if (p.match_type === 'visual') {
          badge = `<span class="rel-badge badge-visual" title="${t('viewer.relatedVisual')}">vis</span>`;
        }
        const tipExtra = (p.matched_tags && p.matched_tags.length)
          ? ' · ' + esc(p.matched_tags.slice(0, 3).join(', '))
          : '';
        return `<div class="rel-item" role="button" tabindex="0" data-idx="${i}" aria-label="#${pid}" title="#${pid}${tipExtra}">${thumb ? `<img src="${thumb}" alt="" loading="lazy" decoding="async">` : ''}${sim}${badge}<span class="rel-id">#${pid}</span></div>`;
      }).join('') +
      '</div>';
  } catch {
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
    this.setSearchValue(newQ);
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

App.classifyTag = function (tag) {
  if (!tag) return 'general';
  const lower = tag.toLowerCase();
  if (lower.startsWith('artist:') || lower.endsWith('_(artist)') || lower.endsWith('_(style)')) {
    return 'artist';
  }
  if (lower.startsWith('character:') || lower.endsWith('_(cosplay)')) {
    return 'character';
  }
  if (lower.startsWith('copyright:') || lower.startsWith('series:')) {
    return 'copyright';
  }
  if (lower.startsWith('meta:') || /^(highres|absurdres|translated|commentary|pool:\d+|source:)/i.test(lower)) {
    return 'meta';
  }
  return 'general';
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
    const lvl = this.OddTags && this.OddTags.levelOf ? this.OddTags.levelOf(tag) : null;
    const cat = this.classifyTag(tag);
    // Цвет только у yellow/red. Уровень note — это «просто объяснение»:
    // тег выглядит как обычный, но с подсказкой.
    span.className = 'viewer-tag' + (isFav(tag) ? ' tag-fav' : '') + (isHid(tag) ? ' tag-hidden' : '') + (isQ(tag) ? ' tag-query' : '') + (lvl === 'yellow' || lvl === 'red' ? ' tag-odd-' + lvl : '') + (cat !== 'general' ? ' tag-cat-' + cat : '');
    span._brieflyTag = tag;
    // Всплывашка превью тега (tag_popover.js) цепляется к data-tag.
    span.dataset.tag = tag;
    // A11y (#15): тег — интерактивный элемент: роль + фокус с клавиатуры.
    span.tabIndex = 0;
    try { span.setAttribute('role', 'button'); } catch { /* тестовый DOM без setAttribute */ }
    try { span.setAttribute('aria-label', tag); } catch { /* noop */ }
    // Подсказка при наведении: только текст описания, без префикса
    // «Странный —» или «Метка —». Пользователь просил именно объяснение.
    const oddDesc = this.OddTags && this.OddTags.descOf ? this.OddTags.descOf(tag) : null;
    if (oddDesc) {
      span.title = oddDesc;
      try { span.setAttribute('data-odd', lvl); } catch { /* тестовый DOM */ }
    }
    const text = document.createElement('span');
    text.textContent = tag;
    span.appendChild(text);
    // Значок «?» в кружочке — признак, что у тега есть объяснение.
    // Ставится для всех уровней, включая note (без цвета).
    if (oddDesc) {
      const hint = document.createElement('span');
      hint.className = 'tag-hint';
      hint.textContent = '?';
      hint.title = oddDesc;
      // Скринридеру «?» ничего не даёт: объяснение уже висит в подсказке
      // самого тега, лишний символ только шумит.
      try { hint.setAttribute('aria-hidden', 'true'); } catch { /* тестовый DOM */ }
      span.appendChild(hint);
    }
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
      chip.innerHTML = `${icon('arrowUp', 11)} #${parent.id}${parent.file_type ? ' · ' + parent.file_type : ''}`;
      this._bindRelChipOpen(chip, parent.id);
      host.appendChild(chip);
    }
    children.forEach(ch => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'rel-chip rel-child';
      chip.title = t('viewer.relChild');
      chip.innerHTML = `${icon('arrowDown', 11)} #${ch.id}${ch.file_type ? ' · ' + ch.file_type : ''}`;
      this._bindRelChipOpen(chip, ch.id);
      host.appendChild(chip);
    });
    if (!parent && !children.length) {
      // Маленькая кнопка «привязать» — пост без семьи можно сделать ребёнком.
      const setBtn = document.createElement('button');
      setBtn.type = 'button';
      setBtn.className = 'rel-chip rel-set';
      setBtn.title = t('viewer.relSetHint');
      setBtn.innerHTML = `${icon('arrowUp', 11)} ${esc(t('viewer.relSetBtn').replace(/^[↑+]\s*/, ''))}`;
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
  this.setSearchValue(q);
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

/**
 * Кнопка «Открыть на источнике»: URL кладём в data-url, при отсутствии
 * источника кнопку прячем (формат Post.Source может быть неизвестен).
 * Возвращает URL или null.
 * @this {AppType}
 */
App._syncSourceButton = function (post) {
  const btn = this.els.viewerSource;
  if (!btn) return null;
  const url = this.sourcePostUrl(post);
  if (url) {
    btn.classList.remove('hidden');
    btn.dataset.url = url;
  } else {
    btn.classList.add('hidden');
    delete btn.dataset.url;
  }
  return url;
};
