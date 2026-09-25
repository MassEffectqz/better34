// viewed_marks.test.js — запись отметок «просмотрено»:
//  * быстрое листание больше не теряет посты (был один pending-id, который
//    перезаписывался — на сервер уходил только последний пост);
//  * отметки уходят пачкой через POST /api/views, хвост — при уходе со страницы;
//  * отметку можно снять, загруженное — отметить целиком.
import { App } from '../state.js';
import { API } from '../api.js';

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
    toggle(c, f) { const on = f != null ? !!f : !s.has(c); if (on) s.add(c); else s.delete(c); return on; },
  };
}
const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {}, innerWidth: 1400, innerHeight: 800 });
defG('document', { addEventListener() {}, getElementById: () => null, querySelector: () => null, createElement: () => ({ classList: makeClassList() }) });
defG('localStorage', {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
});

let posts = [];
let failNext = false;
API.post = async function (url, body) {
  posts.push({ url, body });
  if (url === '/views' && failNext) { failNext = false; throw new Error('offline'); }
  return {};
};
API.get = async function () { return { posts: [] }; };
API.invalidate = function () {};

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [], focusedIndex: -1, viewerOpen: false, viewerIndex: 0,
    viewedFilter: '', isLocal: false, downloadQueue: new Set(), selected: new Set(),
  };
  a.els = {};
  a.loadPosts = function () { a._reloads = (a._reloads || 0) + 1; };
  a.invalidateFeedCache = function () {};
  a.showToast = function (msg, kind) { a._toast = [msg, kind]; };
  a.closeViewer = function () { a.state.viewerOpen = false; a._closed = true; };
  return a;
}

const wait = (ms) => new Promise((r) => setTimeout(r, ms));
// Debounce реальный (1.5с) — ждём его, иначе тест ничего не проверит.
const FLUSH_WAIT = 1700;

console.log('Отметки «просмотрено»\n');

// ── 1. Быстрое листание: все посты, а не только последний ───────────────
{
  const a = makeApp();
  posts = [];
  for (const id of [1, 2, 3, 4, 5]) a._markViewed(id);   // 5 постов за 0 мс
  check('до отправки в очередь ничего не ушло', posts.length === 0, JSON.stringify(posts));
  await wait(FLUSH_WAIT);
  check('быстрое листание: одна пачка, а не по одному запросу', posts.length === 1, JSON.stringify(posts));
  check('отправка идёт батчем через /api/views', posts[0] && posts[0].url === '/views');
  const ids = (posts[0] && posts[0].body.ids) || [];
  check('в пачке все 5 постов, а не только последний',
    ids.length === 5 && ids.includes(1) && ids.includes(5), JSON.stringify(ids));
}

// ── 2. Дедуп за сессию и досылка хвоста ─────────────────────────────────
{
  const a = makeApp();
  posts = [];
  a._markViewed(9);
  a._markViewed(9);
  a._markViewed(9);
  await wait(FLUSH_WAIT);
  check('повторный показ того же поста не дублируется', posts.length === 1 && posts[0].body.ids.length === 1, JSON.stringify(posts));

  // Хвост очереди: отметили и ушли со страницы до истечения debounce.
  const b = makeApp();
  posts = [];
  b._markViewed(21);
  b._markViewed(22);
  b._flushViewed();               // как pagehide/visibilitychange
  check('pagehide досылает накопленное немедленно', posts.length === 1 && posts[0].body.ids.length === 2, JSON.stringify(posts));

  const c = makeApp();
  posts = [];
  c._flushViewed();
  check('пустая очередь не шлёт запрос', posts.length === 0);
}

// ── 3. Ошибка отправки не теряет отметки ────────────────────────────────
{
  const a = makeApp();
  posts = [];
  failNext = true;
  a._markViewed(31);
  await wait(FLUSH_WAIT);
  check('первая попытка упала', posts.length === 1);
  await wait(FLUSH_WAIT);
  check('после ошибки очередь уходит повторно', posts.length === 2, JSON.stringify(posts));
  check('повтор содержит тот же пост', posts[1].body.ids.includes(31), JSON.stringify(posts[1]));
  failNext = false;
}

// ── 4. Снятие отметки ──────────────────────────────────────────────────
{
  const a = makeApp();
  a.state.posts = [{ id: 5 }, { id: 6 }];
  a.state.viewerOpen = true;
  a.state.viewerIndex = 1;
  a.state.viewedFilter = '1';
  posts = [];
  await a.unmarkViewed();
  check('снятие отметки: forget для открытого поста',
    posts.length === 1 && posts[0].url === '/view/6/forget', JSON.stringify(posts));
  check('снятие отметки: вьювер закрыт, лента перезагружена',
    a._closed === true && a._reloads === 1, JSON.stringify({ closed: a._closed, reloads: a._reloads }));
}
{
  const a = makeApp();
  a.state.focusedIndex = 0;
  a.state.posts = [{ id: 8 }];
  posts = [];
  await a.unmarkViewed();
  check('без вьюера снимается отметка с выделенного в сетке',
    posts.length === 1 && posts[0].url === '/view/8/forget', JSON.stringify(posts));
  check('без активного фильтра лента не перезагружается', a._reloads === undefined, String(a._reloads));
}
{
  const a = makeApp();
  a.state.focusedIndex = -1;
  posts = [];
  await a.unmarkViewed();
  check('нечего снимать — тост и ноль запросов', posts.length === 0 && !!a._toast, JSON.stringify({ posts, toast: a._toast }));
}

// ── 5. «Отметить загруженное» ───────────────────────────────────────────
{
  const a = makeApp();
  a.state.posts = [{ id: 1 }, { id: 2 }, { id: 2 }];
  a.state.viewedFilter = '0';
  posts = [];
  await a.markLoadedViewed();
  check('загруженное отмечается батчем без дублей',
    posts.length === 1 && posts[0].url === '/views' && posts[0].body.ids.join() === '1,2', JSON.stringify(posts));
  check('после «отметить загруженное» лента перезагружается под фильтром', a._reloads === 1, String(a._reloads));
  const b = makeApp();
  b.state.posts = [];
  posts = [];
  await b.markLoadedViewed();
  check('пустая лента — тост, без запроса', posts.length === 0 && !!b._toast, JSON.stringify({ posts, toast: b._toast }));
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);
