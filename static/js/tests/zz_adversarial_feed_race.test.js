// zz_adversarial_feed_race.test.js — F2 (префетч страницы), F3 (гонка лент).
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
    contains(c) { return s.has(c); },
    add(c) { s.add(c); },
    remove(c) { s.delete(c); },
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
    addEventListener() {},
    querySelector() { return makeEl(); },
    querySelectorAll() { return []; },
  };
}

globalThis.window = { innerWidth: 1400, innerHeight: 800 };
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

const flush = () => new Promise(res => setTimeout(res, 10));

// Патчим реальный API.get: промисы резолвятся вручную по порядку.
let apiCalls = [];
API.get = async function (url) {
  const rec = { url, resolvers: [] };
  rec.promise = new Promise(res => rec.resolvers.push(res));
  apiCalls.push(rec);
  return rec.promise;
};
API.post = async function () { return {}; };
API.invalidate = function () {};

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [], page: 1, hasMore: true, loading: false, viewerOpen: false,
    focusedIndex: -1, selected: new Set(),
    query: '', isLocal: false, recommendActive: false,
    displayMode: 'search', displayIds: [], minId: null, sortBy: null,
    autoDownload: false, downloadQueue: new Set(), downloading: new Set(),
    profile: { liked_posts: [], hidden_posts: [], hidden_tags: [], fav_tags: [] },
  };
  a.els = {
    grid: { innerHTML: '', appendChild() {}, querySelectorAll: () => [] },
    sentinel: { classList: makeClassList(), style: { display: '' } },
    batchBar: { classList: makeClassList() }, batchCount: { textContent: '' },
    statusText: { textContent: '' },
    modeBar: { classList: makeClassList() },
    modeBarText: { textContent: '' }, modeBarRefresh: { classList: makeClassList() },
    searchClear: { classList: makeClassList() },
  };
  a.createPostCard = () => ({ dataset: {} });
  a.shortestCol = () => ({ appendChild() {} });
  a.ensureColumns = function () {};
  a.clearGrid = function () {};
  a.showSkeletons = function () {};
  a.hideSkeletons = function () {};
  a.updateStatus = function () {};
  a.setStatus = function () {};
  a.renderModeBar = function () {};
  a.renderFilterChips = function () {};
  a.renderPosts = function () {};
  a.toggleProfile = function () {};
  a.showToast = function () {};
  a.pushState = function () {};
  a._finishFeedLoad = function () {};
  a._runPendingReload = function () {};
  a._observeCardReveal = function () {};
  a.maybeLoadMore = function () {};
  a._clearFeedCache = function () {};
  return a;
}

console.log('feed prefetch/race (F2, F3) tests\n');

// F2: _prefetchNextPage всегда опережает реальную следующую страницу на 1.
// После первой загрузки state.page == 2, а префетч берёт page=3 — хотя
// следующий loadMore запросит page=2. Каждая страница скачивается дважды,
// а префетч никогда не ускоряет следующий лист.
{
  const a = makeApp();
  a.state.page = 2;
  a.els.sentinel = { style: { display: '' } };
  a._sentinelNearViewport = () => true;
  a._feedFailures = 0;
  apiCalls = [];
  a._prefetchNextPage();
  const prefetched = apiCalls.length ? apiCalls[0].url : '';
  check('F2: префетч должен брать СЛЕДУЮЩУЮ реальную страницу (page=2 при state.page=2)',
    prefetched.indexOf('page=2') >= 0, 'запрошен page=3 вместо page=2: ' + prefetched);
}

// F3: гонка showGridMode vs in-flight loadPosts. Пока лента «последних постов»
// до-гружает страницу N, пользователь открывает «Лайки»; showGridMode
// заменяет state.posts на лайки, а затем in-flight ответ дописывает в лайки
// посты из старого поискового запроса → перемешанная лента.
(async () => {
  const a = makeApp();
  a.state.profile.liked_posts = [1, 2, 3];
  apiCalls = [];

  const searchLoad = a.loadPosts(false); // in-flight, страница из поиска
  await flush();
  check('F3: loadPosts отправил запрос поиска', apiCalls.length === 1 && apiCalls[0].url.indexOf('/posts?') >= 0, JSON.stringify(apiCalls.map(c => c.url)));

  const gridLoad = a.showGridMode('likes'); // заменяет ленту лайками
  await flush();
  check('F3: showGridMode отправил /posts-by-ids', apiCalls.length === 2 && apiCalls[1].url.indexOf('/posts-by-ids') >= 0, JSON.stringify(apiCalls.map(c => c.url)));

  // лайки загрузились раньше, чем старый поисковый ответ
  apiCalls[1].resolvers[0]({ posts: [{ id: 1 }, { id: 2 }, { id: 3 }] });
  await gridLoad;
  check('F3: лента переключена на лайки (3 поста)', a.state.posts.length === 3, 'posts=' + a.state.posts.length);

  // теперь приходит СТАРЫЙ ответ поиска — дописывается в лайки
  apiCalls[0].resolvers[0]({ posts: [{ id: 500 }, { id: 501 }] });
  await searchLoad;
  const stale = a.state.posts.filter(p => p.id === 500 || p.id === 501).length;
  check('F3: устаревший ответ поиска НЕ должен попасть в лайки',
    stale === 0, 'в лайках появились посты из старого поиска: ' + a.state.posts.map(p => p.id).join(','));

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();