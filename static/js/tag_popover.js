// tag_popover.js — всплывашка по тегу: сколько постов в библиотеке, мини-сетка
// обложек и смежные теги. Всё считается локально по SQLite (GET
// /api/tags/:tag/preview) — ни сети, ни лишних запросов к бурам.
import { App } from './state.js';
import { esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

const HOVER_DELAY = 220;   // не мелькаем, пока курсор скользит по списку
const HIDE_DELAY = 260;    // даём время навестись на саму всплывашку
const CACHE_MAX = 60;      // сколько тегов держим в памяти

App._tagCache = new Map();
App._tagSourceCache = new Map();

// _tagCachePut кладёт ответ в кэш, выкидывая самый старый: Map хранит ключи в
// порядке вставки.
App._tagCachePut = function (cache, key, val) {
  cache.set(key, val);
  if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value);
  return val;
};

// tagRatingParam — тот же фильтр рейтинга, что у обычного поиска: превью с
// бору не должно показывать то, что пользователь запретил показывать.
const tagRatingParam = (app) =>
  (app.state && app.state.ratingFilter) ? '&rating=' + encodeURIComponent(app.state.ratingFilter) : '';

// fetchTagPreview с кэшем в памяти: повторные наведения на один тег (список
// тегов поста, подсказки) не должны снова идти в сервер.
//
// Запрос строго локальный и быстрый. Превью с бору едет отдельным запросом
// (fetchTagSourcePreview): раньше поиск в сети был частью этого ответа, и
// наведение на любой тег без скачанных постов висело на нем секундами.
App.fetchTagPreview = function (tag) {
  const key = String(tag || '').toLowerCase();
  if (!key) return Promise.resolve(null);
  const hit = this._tagCache.get(key);
  if (hit) return Promise.resolve(hit);
  // this.API — точка подмены для тестов.
  const api = this.API || API;
  const url = '/tags/' + encodeURIComponent(key) + '/preview?limit=6&related=8' + tagRatingParam(this);
  return api.get(url, { fresh: true })
    .then((d) => this._tagCachePut(this._tagCache, key, d))
    .catch(() => null);
};

// fetchTagSourcePreview — превью с активного бору по тегу. Медленный сетевой
// запрос, поэтому всплывашка не ждёт его: показывает локальную часть сразу и
// доклеивает этот блок, когда он придёт.
App.fetchTagSourcePreview = function (tag) {
  const key = String(tag || '').toLowerCase();
  if (!key) return Promise.resolve(null);
  const hit = this._tagSourceCache.get(key);
  if (hit) return Promise.resolve(hit);
  // Офлайн идти на бор бессмысленно: сервер всё равно ничего не достанет, а
  // лишний запрос только отложит пустую всплывашку.
  if (typeof navigator !== 'undefined' && navigator.onLine === false) return Promise.resolve(null);
  const api = this.API || API;
  const url = '/tags/' + encodeURIComponent(key) + '/source-preview?limit=6' + tagRatingParam(this);
  return api.get(url, { fresh: true })
    .then((d) => this._tagCachePut(this._tagSourceCache, key, d))
    .catch(() => null);
};

// ensureTagPopover создаёт (один раз) общий узел всплывашки.
App.ensureTagPopover = function () {
  if (this._tagPop) return this._tagPop;
  const el = document.createElement('div');
  el.className = 'tag-popover hidden';
  el.setAttribute('role', 'dialog');
  el.setAttribute('aria-live', 'polite');
  document.body.appendChild(el);
  this._tagPop = el;
  el.addEventListener('click', (ev) => this.onTagPopoverClick(ev));
  // Пока pointer над самой всплывашкой, уход с тега её не закрывает: курсор
  // часто успевает уйти с тега раньше, чем всплывашка появится, и без этого
  // подсказка мигала. Вернулся на тег — гасим отложенное скрытие.
  el.addEventListener('mouseenter', () => {
    this._tagPopHideTimer && clearTimeout(this._tagPopHideTimer);
    this._tagPopHideTimer = null;
  });
  el.addEventListener('mouseleave', () => this.scheduleHideTagPopover());
  return el;
};

App.onTagPopoverClick = function (ev) {
  const cover = ev.target.closest ? ev.target.closest('.tag-pop-cover') : null;
  if (cover && cover.dataset.post) {
    ev.preventDefault();
    this.hideTagPopover();
    this.openPostById(parseInt(cover.dataset.post, 10));
    return;
  }
  const rel = ev.target.closest ? ev.target.closest('.tag-pop-related') : null;
  if (rel && rel.dataset.tag) {
    ev.preventDefault();
    this.hideTagPopover();
    this.applySuggestion(rel.dataset.tag);
  }
};

App.hideTagPopover = function () {
  this._tagPopHideTimer && clearTimeout(this._tagPopHideTimer);
  this._tagPopHideTimer = null;
  this._tagPopAnchor = null;
  if (this._tagPop) this._tagPop.classList.add('hidden');
};

App.scheduleHideTagPopover = function () {
  this._tagPopHideTimer && clearTimeout(this._tagPopHideTimer);
  this._tagPopHideTimer = setTimeout(() => this.hideTagPopover(), HIDE_DELAY);
};

// showTagPopover ставит всплывашку под элементом с тегом и наполняет её.
App.showTagPopover = function (tag, anchor) {
  if (!anchor || !anchor.isConnected) return;
  const el = this.ensureTagPopover();
  this._tagPopAnchor = anchor;
  el.classList.remove('hidden');
  el.innerHTML = '<div class="tag-pop-head">' + esc(String(tag)) + '…</div>';
  this._placeTagPopover(anchor);
  const paint = (d) => {
    // Пока грузили, могли навестись на другой тег или закрыть.
    if (this._tagPopAnchor !== anchor || !el.isConnected) return;
    el.innerHTML = this.renderTagPopoverBody(d);
    // Обязательно пересчитываем позицию: заглушка «тег…» занимала ~30px, а
    // реальное содержимое с обложками и смежными тегами — в разы выше. Без
    // этого всплывашка уезжала за нижний край экрана.
    this._placeTagPopover(anchor);
  };
  this.fetchTagPreview(tag).then((d) => {
    if (!d) {
      // Ответ не пришёл (сеть, 500) — рисуем честную пустоту. Раньше здесь
      // просто ничего не рисовалось, и подсказка навсегда оставалась «tag…».
      paint({ tag: tag, count: 0, posts: [], related: [] });
      return;
    }
    // Копия: d лежит в общем кэше, а пометка __searching — наша, личная.
    const data = Object.assign({}, d);
    const empty = !(data.posts || []).length;
    data.__searching = empty;
    paint(data);
    if (!empty) return;
    // На бор идём только там, где показывать нечего, и отдельным запросом:
    // он занимает секунды, и в общем ответе он задерживал бы всю всплывашку.
    this.fetchTagSourcePreview(tag).then((s) => {
      if (!s || this._tagPopAnchor !== anchor) return;
      data.__searching = false;
      data.source = (s.posts || []).length ? s : { site: s.site, posts: [] };
      paint(data);
    });
  });
};

// tagPopoverGrid — мини-сетка обложек. fromSource=true для превью с бура:
// такой пост не скачан, подпись и рамка другие, но он кликабелен — по клику
// открывается во вьюере (метаданные сервер уже записал в БД).
const tagPopoverGrid = (posts, fromSource) =>
  '<div class="tag-pop-covers' + (fromSource ? ' tag-pop-covers-src' : '') + '">' +
  posts.map((p) => {
    // У скачанных обложка ведёт на /api/thumb/:id (её кэширует SW, т.е. есть
    // офлайн), у найденных на бору — прямо на превью через прокси.
    const ar = p.width && p.height ? ' style="aspect-ratio:' + p.width + '/' + p.height + '"' : '';
    const title = fromSource
      ? t('tagPreview.sourceOpen', { id: p.id, site: p.site || '' })
      : t('tagPreview.openPost', { id: p.id });
    return '<button type="button" class="tag-pop-cover" data-post="' + p.id + '"' + ar +
      ' title="' + esc(title) + '">' +
      (p.thumb ? '<img loading="lazy" decoding="async" alt="" src="' + esc(p.thumb) + '">' : '') +
      '</button>';
  }).join('') + '</div>';

App.renderTagPopoverBody = function (d) {
  const head = '<div class="tag-pop-head">' + esc(d.tag) +
    (d.count ? ' <span class="tag-pop-count">' + esc(tf('tagPreview.posts', { n: d.count })) + '</span>' : '') +
    '</div>';
  // Показываем библиотеку; если по тегу у нас ничего нет — то, что нашлось
  // на бору, с честной пометкой, что это не скачанные посты.
  const src = d.source || null;
  const srcPosts = (src && src.posts) || [];
  const covers = (d.posts || []).length
    ? tagPopoverGrid(d.posts, false)
    : srcPosts.length
      ? '<div class="tag-pop-src-title">' + esc(tf('tagPreview.sourceTitle', { site: src.site })) +
        ' <span class="tag-pop-src-note">' + esc(t('tagPreview.sourceNote')) + '</span></div>' +
        tagPopoverGrid(srcPosts, true)
      : '<div class="vc-empty">' + esc(
        // Пока летит запрос на бор, пустое место занимает честная надпись:
        // тишина выглядит как зависшая подсказка.
        d.__searching ? t('tagPreview.searching')
          : src ? tf('tagPreview.sourceEmpty', { site: src.site })
            : t('tagPreview.noCovers')) + '</div>';
  const rel = (d.related || []).length
    ? '<div class="tag-pop-related-row"><span class="tag-pop-related-title">' +
      esc(t('tagPreview.related')) + '</span>' +
      d.related.map((r) => '<button type="button" class="tag-pop-related" data-tag="' + esc(r.tag) + '">' +
        esc(r.tag) + ' <span class="tag-pop-related-n">' + r.count + '</span></button>').join('') +
      '</div>'
    : '';
  return head + covers + rel;
};

App._placeTagPopover = function (anchor) {
  const el = this._tagPop;
  if (!el) return;
  const r = anchor.getBoundingClientRect();
  const vw = window.innerWidth || document.documentElement.clientWidth;
  const vh = window.innerHeight || document.documentElement.clientHeight;
  // Меряем в скрытом состоянии на нулевой позиции: иначе getBoundingClientRect
  // вернёт размер с учётом старой координаты, а при переносе на край экрана —
  // размер «в воздухе».
  el.classList.remove('hidden');
  el.style.left = '0px';
  el.style.top = '0px';
  el.style.visibility = 'hidden';
  const box = el.getBoundingClientRect();
  // Высота не должна вылезать за экран: ограничение и прокрутка заданы в CSS,
  // но и тут подстраховываемся.
  const h = Math.min(box.height, vh - 16);
  const w = Math.min(box.width, vw - 16);

  let left = r.left;
  if (left + w > vw - 8) left = vw - w - 8;
  if (left < 8) left = 8;

  // Сначала пробуем под тегом, затем над ним. Если не влезает ни там, ни там
  // (короткое окно, длинный список) — прижимаем к верху, внутри прокрутка.
  const gap = 6;
  let top = r.bottom + gap;
  if (top + h > vh - 8) {
    const above = r.top - h - gap;
    top = above >= 8 ? above : 8;
  }
  el.style.left = Math.round(left) + 'px';
  el.style.top = Math.round(top) + 'px';
  el.style.visibility = 'visible';
};

// bindTagPopover — одна делегированная подписка на документ: работает и для
// подсказок поиска, и для тегов в вьюере, и для будущих мест с data-tag.
App.bindTagPopover = function () {
  if (this._tagPopBound) return;
  this._tagPopBound = true;
  const show = (target) => {
    const el = target && target.closest ? target.closest('[data-tag]') : null;
    if (!el) return;
    const tag = el.dataset.tag;
    if (!tag || tag === this._tagPopTag) return;
    this._tagPopTag = tag;
    this._tagPopTimer && clearTimeout(this._tagPopTimer);
    this._tagPopTimer = setTimeout(() => this.showTagPopover(tag, el), HOVER_DELAY);
  };
  const leave = (ev) => {
    const from = ev.target;
    // Уход МЫШИ с самого тега — только если мы уходим наружу. Если цель или
    // relatedTarget лежит внутри всплывашки, закрывать её рано: пользователь
    // просто перевёл курсор с тега на обложку, и подсказка гасла у него под
    // носом. Наружу от неё мы уходим через её собственный mouseleave.
    const inPop = (n) => !!(n && n.closest && n.closest('.tag-popover'));
    if (inPop(from) || inPop(ev.relatedTarget)) return;
    const el = from && from.closest ? from.closest('[data-tag]') : null;
    if (!el) return;
    const to = ev.relatedTarget;
    if (to && el.contains && el.contains(to)) return;
    this._tagPopTimer && clearTimeout(this._tagPopTimer);
    this._tagPopTag = null;
    this.scheduleHideTagPopover();
  };
  document.addEventListener('mouseover', (ev) => show(ev.target), true);
  document.addEventListener('focusin', (ev) => show(ev.target), true);
  document.addEventListener('mouseout', leave, true);
  document.addEventListener('focusout', leave, true);
  // Клик по самому тегу (переход к поиску) и Esc закрывают всплывашку.
  document.addEventListener('click', (ev) => {
    if (ev.target && ev.target.closest && ev.target.closest('[data-tag]')) this.hideTagPopover();
  }, true);
  document.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') this.hideTagPopover();
  });
  // Скролл и ресайз ломают позицию — прячем, а не пересчитываем. Скролл
  // ВНУТРИ всплывашки (сетка обложек прокручивается) её не закрывает: событие
  // всплывает до window, и раньше подсказка гасла ровно при долистывании.
  window.addEventListener('scroll', (ev) => {
    if (ev.target && ev.target.closest && ev.target.closest('.tag-popover')) return;
    this.hideTagPopover();
  }, true);
  window.addEventListener('resize', () => this.hideTagPopover());
};

// openPostById открывает пост по id: он может прийти из обложки тега, которого
// нет в текущей выдаче, тогда подгружаем его по id.
App.openPostById = async function (id) {
  if (!id) return;
  const posts = this.state.posts || [];
  const idx = posts.findIndex((p) => p.id === id);
  if (idx >= 0) {
    this.openViewer(idx);
    return;
  }
  try {
    const d = await (this.API || API).get('/posts-by-ids?ids=' + encodeURIComponent(id), { fresh: true });
    const got = (d && d.posts) || [];
    if (!got.length) {
      this.showToast(t('tagPreview.postMissing'), 'info');
      return;
    }
    this.state.posts = got.map((p, i) => ({ ...p, _index: i }));
    this.state.page = 1;
    this.openViewer(0);
  } catch { /* пост недоступен — молча закрываем */ }
};