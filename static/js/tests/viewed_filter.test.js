// viewed_filter.test.js — фильтр «Все / Новое / Виденное»:
//  * в онлайн-ленте источника (в т.ч. gelbooru) параметр viewed уходит в /api/posts
//    (раньше он уходил только в /local, поэтому кнопки ничего не делали);
//  * пустое окно с флагом more догружается, без more — честное «пусто»;
//  * переключатель виден в обычной ленте и скрыт в режимах-сетках.
import { App } from '../state.js';
import { API } from '../api.js';
await import('../feed.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeClassList(initial) {
  const s = new Set(initial || []);
  return {
    contains: (c) => s.has(c),
    add: (c) => s.add(c),
    remove: (c) => s.delete(c),
    toggle(c, force) {
      const on = force != null ? !!force : !s.has(c);
      if (on) s.add(c); else s.delete(c);
      return on;
    },
  };
}

function makeEl() {
  return {
    style: {}, classList: makeClassList(), children: [],
    innerHTML: '', textContent: '',
    appendChild(c) { this.children.push(c); },
    addEventListener() {}, removeEventListener() {},
    querySelector() { return makeEl(); },
    querySelectorAll() { return []; },
    getBoundingClientRect() { return { top: 0, left: 0, width: 0, height: 0 }; },
    setAttribute() {}, removeAttribute() {},
  };
}

globalThis.window = { innerWidth: 1400, innerHeight: 800, addEventListener() {} };
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = {
  addEventListener() {}, removeEventListener() {},
  getElementById() { return null; },
  querySelector() { return null; },
  createElement: () => makeEl(),
  createDocumentFragment: () => ({ children: [], appendChild(c) { this.children.push(c); } }),
};
globalThis.requestAnimationFrame = () => 1;
globalThis.cancelAnimationFrame = () => {};

// Ответы /api/... задаёт тест; совпадение — по подстроке в URL.
let responses = [];
let calls = [];
API.get = async function (url) {
  calls.push(url);
  for (const r of responses) {
    if (url.includes(r.match)) return r.data;
  }
  return { posts: [] };
};
API.post = async function () { return {}; };
API.invalidate = function () {};

function makeToggle() {
  const btns = ['', '0', '1'].map(v => ({ dataset: { viewed: v }, classList: makeClassList() }));
  return {
    classList: makeClassList(),
    btns,
    querySelectorAll() { return this.btns; },
  };
}

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [], page: 1, hasMore: true, loading: false, viewerOpen: false,
    focusedIndex: -1, selected: new Set(), downloading: new Set(), downloadQueue: new Set(),
    query: '', isLocal: false, recommendActive: false, viewedFilter: '',
    displayMode: 'search', displayIds: [], minId: null, sortBy: null,
    autoDownload: false, ratingFilter: '',
    profile: { liked_posts: [], hidden_posts: [], hidden_tags: [], fav_tags: [] },
  };
  a.els = {
    grid: { innerHTML: '', appendChild() {}, querySelectorAll: () => [] },
    sentinel: { classList: makeClassList(), style: { display: '' } },
    searchClear: { classList: makeClassList() },
    viewedToggle: makeToggle(),
  };
  a.pageSize = () => 10;
  a._hasHiddenTag = () => false;
  a.createPostCard = () => ({ dataset: {} });
  a.shortestCol = () => ({ appendChild() {} });
  a.ensureColumns = function () {};
  a.clearGrid = function () {};
  a.showSkeletons = function () {};
  a.hideSkeletons = function () {};
  a.updateStatus = function () {};
  a.setStatus = function () {};
  a.renderModeBar = function () {};
  a.renderPosts = function () {};
  a.renderEmptyState = function (o) { a._empty = o; return makeEl(); };
  a.updateBatchBar = function () {};
  a.showToast = function () {};
  a.pushState = function () {};
  a._finishFeedLoad = function () {};
  a._runPendingReload = function () {};
  a._observeCardReveal = function () {};
  a.maybeLoadMore = function () {};
  a._prefetchNextPage = function () {};
  a._clearFeedCache = function () { a._cacheCleared = (a._cacheCleared || 0) + 1; };
  a.invalidateFeedCache = function () { a._cacheCleared = (a._cacheCleared || 0) + 1; };
  return a;
}

const post = (id) => ({ id, tags: 'a', file_type: 'jpg', width: 10, height: 10 });
const flush = () => new Promise((r) => setTimeout(r, 0));

console.log('Фильтр «Все/Новое/Виденное»\n');

// ── 1. viewed уходит в онлайн-выдачу источника (gelbooru/rule34) ──────────
{
  const a = makeApp();
  calls = [];
  responses = [{ match: '/posts?', data: { posts: [post(1), post(2)] } }];
  a.state.viewedFilter = '0';
  await a.loadPosts(true);
  check('онлайн-лента: /posts получает viewed=0',
    calls.length === 1 && calls[0].includes('viewed=0'), JSON.stringify(calls));
}
{
  const a = makeApp();
  calls = [];
  responses = [{ match: '/posts?', data: { posts: [post(1)] } }];
  await a.loadPosts(true);
  check('без фильтра в URL нет viewed (дефолтная выдача не меняется)',
    calls.length === 1 && !calls[0].includes('viewed='), JSON.stringify(calls));
}
{
  const a = makeApp();
  calls = [];
  responses = [{ match: '/posts?', data: { posts: [post(1)] } }];
  a.state.isLocal = true;
  a.state.viewedFilter = '1';
  await a.loadPosts(true);
  check('локальная лента: viewed по-прежнему уходит в /local',
    calls.length === 1 && calls[0].startsWith('/local') && calls[0].includes('viewed=1'), JSON.stringify(calls));
}

// ── 2. Пустое окно + more → догружаем следующий лист ────────────────────
{
  const a = makeApp();
  calls = [];
  responses = [
    { match: 'page=1', data: { posts: [], more: true } },
    { match: 'page=2', data: { posts: [post(7)] } },
  ];
  a.state.viewedFilter = '0';
  await a.loadPosts(true);
  await flush();
  check('пустой лист с more=true догружает page=2',
    calls.length === 2 && calls[1].includes('page=2'), JSON.stringify(calls));
  check('посты догруженного листа попали в ленту',
    a.state.posts.length === 1 && a.state.posts[0].id === 7, JSON.stringify(a.state.posts.map(p => p.id)));
}
{
  const a = makeApp();
  calls = [];
  responses = [{ match: '/posts?', data: { posts: [] } }];
  a.state.viewedFilter = '0';
  await a.loadPosts(true);
  check('пустой лист без more — стоп (дальше искать незачем)', calls.length === 1, JSON.stringify(calls));
  check('пустой лист без more: hasMore=false', a.state.hasMore === false);
  check('пустое состояние объясняет фильтр, а не «нет постов»',
    !!a._empty && typeof a._empty.title === 'string' && a._empty.title.length > 0
      && a._empty.actions.some(x => x.key === 'viewed-all'), JSON.stringify(a._empty));
}

// ── 3. Видимость переключателя ──────────────────────────────────────────
{
  const a = makeApp();
  a.state.displayMode = 'search';
  a.updateViewedToggle();
  check('в обычной ленте переключатель виден (и для онлайн-источника)',
    !a.els.viewedToggle.classList.contains('hidden'));
  a.state.displayMode = 'likes';
  a.updateViewedToggle();
  check('в режиме-сетке (лайки) переключатель скрыт',
    a.els.viewedToggle.classList.contains('hidden'));
  a.state.displayMode = 'search';
  a.state.recommendActive = true;
  a.updateViewedToggle();
  check('в рекомендациях переключатель скрыт',
    a.els.viewedToggle.classList.contains('hidden'));
}

// ── 4. setViewedFilter: состояние, кнопки, сброс кэша, перезагрузка ─────
{
  const a = makeApp();
  let reloads = 0;
  a.loadPosts = function () { reloads++; };
  a.setViewedFilter('1');
  check('setViewedFilter пишет состояние', a.state.viewedFilter === '1');
  check('активной стала кнопка «Виденное»',
    a.els.viewedToggle.btns[2].classList.contains('active')
      && !a.els.viewedToggle.btns[0].classList.contains('active'));
  check('кэш ленты сброшен', a._cacheCleared === 1, String(a._cacheCleared));
  check('лента перезагружена', reloads === 1, String(reloads));
  a.setViewedFilter('');
  check('возврат к «Все» синхронизирует кнопки',
    a.state.viewedFilter === '' && a.els.viewedToggle.btns[0].classList.contains('active'));
}

// ── 5. Кэш неотфильтрованного листа не используется при активном фильтре ─
{
  const a = makeApp();
  a.state.viewedFilter = '0';
  check('с фильтром кэш листа выключен', a._feedCacheKey() === null, String(a._feedCacheKey()));
  a.state.viewedFilter = '';
  check('без фильтра кэш листа прежний', a._feedCacheKey() === 'briefly_feed_cache', String(a._feedCacheKey()));
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);
