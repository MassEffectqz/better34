// similar_deeplink.test.js — глубокая ссылка /similar/<id>:
//  * parseLocation разбирает /similar/<id> (и не ломает остальные маршруты);
//  * openSimilarById открывает режим, пишет /similar/<id> в историю;
//  * popstate: вход в режим по ссылке, идемпотентность, выход назад;
//  * Ctrl/Cmd+ЛКМ и средняя кнопка по «Похожие (по картинке)» — новая вкладка.
import { App } from '../state.js';
import { API } from '../api.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── стабы окружения ──────────────────────────────────────────────────────
function makeClassList() {
  const s = new Set();
  return {
    contains: (c) => s.has(c),
    add: (c) => s.add(c),
    remove: (c) => s.delete(c),
    toggle(c, f) { const on = f != null ? !!f : !s.has(c); if (on) s.add(c); else s.delete(c); return on; },
  };
}

function makeEl(tag) {
  const el = {
    tagName: String(tag || 'div').toUpperCase(),
    children: [], dataset: {}, style: {}, listeners: {},
    className: '', textContent: '', innerHTML: '', value: '',
    classList: makeClassList(),
    appendChild(c) { this.children.push(c); return c; },
    setAttribute() {}, removeAttribute() {}, remove() {}, focus() {}, blur() {}, click() {},
    addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); },
    removeEventListener() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
    closest() { return null; },
    getBoundingClientRect() { return { top: 0, left: 0, width: 100, height: 100 }; },
    play() { return Promise.resolve(); }, pause() {},
  };
  if (el.tagName === 'TEMPLATE') el.content = { firstChild: null };
  return el;
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });

defG('document', {
  getElementById: () => null, querySelector: () => null, querySelectorAll: () => [],
  createElement: (tag) => makeEl(tag), createTextNode: () => ({ nodeType: 3, textContent: '' }),
  createDocumentFragment: () => ({ children: [], appendChild(c) { this.children.push(c); } }),
  addEventListener() {}, removeEventListener() {}, body: { appendChild() {} },
});
defG('window', { addEventListener() {}, innerWidth: 1400, innerHeight: 800 });
defG('localStorage', {
  _s: {}, getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); }, removeItem(k) { delete this._s[k]; },
});
defG('navigator', { maxTouchPoints: 0 });
defG('location', {
  href: 'https://localhost:3000/', origin: 'https://localhost:3000',
  pathname: '/', search: '', assigned: [],
  assign(u) { this.assigned.push(u); },
});

const hist = {
  calls: [], state: null,
  pushState(state, _t, url) { this.calls.push(['push', url, state]); this.state = state; },
  replaceState(state, _t, url) { this.calls.push(['replace', url, state]); this.state = state; },
  back() { this.calls.push(['back']); },
};
defG('history', hist);
defG('requestAnimationFrame', (f) => setTimeout(f, 0));

await import('../feed.js');

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [], query: '', viewerOpen: false, viewerIndex: 0,
    selected: new Set(), downloading: new Set(), downloadQueue: new Set(),
    profile: { liked_posts: [], hidden_posts: [] },
    displayMode: 'search', displayIds: [],
  };
  a.els = { searchInput: makeEl('input') };
  a.viewerCalls = []; a.closed = 0; a.opened = []; a.toasts = [];
  a.openViewer = function (i) { a.viewerCalls.push(i); };
  a.closeViewer = function () { a.closed++; };
  a.showToast = function (m) { a.toasts.push(m); };
  a._isTouch = () => false;
  return a;
}

const setPath = (p) => { location.pathname = p; };
const reset = () => { hist.calls = []; hist.state = null; };

console.log('Глубокая ссылка /similar/<id> и кнопка «Похожие (по картинке)»\n');

// ── 1. Разбор пути ───────────────────────────────────────────────────────
{
  const a = makeApp();
  const s = a.parseLocation('/similar/42');
  check('parseLocation: /similar/<id>',
    s.matched && s.similarId === 42 && s.postId === null && s.query === '', JSON.stringify(s));

  const q = a.parseLocation('/search/cat');
  check('parseLocation: прочие маршруты получают similarId: null',
    q.matched && q.similarId === null && a.parseLocation('/post/5').similarId === null
    && a.parseLocation('/').similarId === null, JSON.stringify(q));

  const bad = a.parseLocation('/similar/abc');
  check('parseLocation: /similar/нечисло — маршрут не распознан', !bad.matched, JSON.stringify(bad));
}

// ── 2. openSimilarById: режим + запись истории ───────────────────────────
{
  let a = makeApp();
  reset();
  a._lastURL = '/search/cat/post/9';
  a.state.viewerOpen = true;
  a.state.query = 'cat';
  const grids = [];
  a.showGridMode = async (type, ids) => { grids.push([type, ids.slice()]); a.state.displayMode = 'similar'; };
  const origGet = API.get;
  API.get = async (url) => ({ posts: url === '/similar/77' ? [{ id: 10 }, { id: 11 }] : [] });
  const ok = await a.openSimilarById(77);
  API.get = origGet;
  check('openSimilarById: режим «похожие» открыт',
    ok === true && grids.length === 1 && grids[0][0] === 'similar' && String(grids[0][1]) === '10,11',
    JSON.stringify(grids));
  check('openSimilarById: открытый вьювер закрыт', a.closed === 1, 'closed=' + a.closed);
  check('openSimilarById: /similar/77 дописан в историю с similarId',
    a._lastURL === '/similar/77'
    && hist.calls.some(c => c[0] === 'push' && c[1] === '/similar/77' && c[2].similarId === 77),
    JSON.stringify(hist.calls));
  check('openSimilarById: _similarSourceId запомнен (идемпотентность popstate)', a._similarSourceId === 77);

  // Пустой результат — режим не открываем.
  a = makeApp();
  reset();
  API.get = async () => ({ posts: [] });
  const ok2 = await a.openSimilarById(77);
  API.get = origGet;
  check('openSimilarById: пустой список — false + тост, история не тронута',
    ok2 === false && a.toasts.length === 1 && hist.calls.length === 0,
    JSON.stringify({ toasts: a.toasts, calls: hist.calls }));

  // Ошибка сети.
  a = makeApp();
  API.get = async () => { throw new Error('boom'); };
  const ok3 = await a.openSimilarById(77);
  API.get = origGet;
  check('openSimilarById: ошибка сети — false + тост с текстом ошибки',
    ok3 === false && a.toasts.length === 1 && a.toasts[0].includes('boom'), JSON.stringify(a.toasts));

  // showSimilar берёт id текущего поста.
  a = makeApp();
  a.state.posts = [{ id: 55 }];
  a.state.viewerIndex = 0;
  let called = 0;
  a.openSimilarById = async (id) => { called = id; return true; };
  await a.showSimilar();
  check('showSimilar: id текущего поста во вьювере', called === 55, 'called=' + called);
}

// ── 3. _applyLocationState: вход/выход/идемпотентность ───────────────────
{
  let a = makeApp();
  const openedIds = [];
  a.openSimilarById = async (id) => { openedIds.push(id); return true; };
  setPath('/similar/7');
  await a._applyLocationState();
  check('popstate на /similar/<id> открывает режим',
    openedIds.length === 1 && openedIds[0] === 7, JSON.stringify(openedIds));

  // Уже открыта та же ссылка — повторного запроса нет.
  a.state.displayMode = 'similar';
  a._similarSourceId = 7;
  await a._applyLocationState();
  check('повторный переход на тот же /similar/<id> идемпотентен',
    openedIds.length === 1, JSON.stringify(openedIds));

  // Назад в режим «похожие» на другой id — открываем новый.
  a._similarSourceId = 99;
  setPath('/similar/8');
  await a._applyLocationState();
  check('другой /similar/<id> в истории — режим перерисовывается',
    openedIds.length === 2 && openedIds[1] === 8, JSON.stringify(openedIds));

  // Назад из режима «похожие» на URL запроса — выход из режима.
  a = makeApp();
  a.state.displayMode = 'similar';
  a._similarSourceId = 7;
  a.state.query = 'cat';
  a.state.posts = [{ id: 1 }];
  let clears = 0;
  a.clearMode = () => { clears++; a.state.displayMode = 'search'; };
  setPath('/search/dog');
  await a._applyLocationState();
  check('назад из «похожих»: clearMode + запрос из URL подхвачен',
    clears === 1 && a.state.query === 'dog' && a.els.searchInput.value === 'dog',
    JSON.stringify({ clears, query: a.state.query, input: a.els.searchInput.value }));
}

// ── 4. Клик по кнопке «Похожие (по картинке)» ────────────────────────────
{
  const a = makeApp();
  a.state.posts = [{ id: 9 }];
  a.state.viewerIndex = 0;
  a.openInNewTab = (url) => a.opened.push(url);
  let sims = 0;
  a.showSimilar = async () => { sims++; return true; };
  const ev = (extra) => Object.assign({ preventDefault() {} }, extra);

  a.onSimilarClick(ev({}));
  check('обычный клик — режим в этой вкладке',
    sims === 1 && a.opened.length === 0, JSON.stringify({ sims, opened: a.opened }));

  a.onSimilarClick(ev({ ctrlKey: true }));
  check('Ctrl+ЛКМ — /similar/9 в новой вкладке',
    a.opened.length === 1 && a.opened[0] === '/similar/9' && sims === 1,
    JSON.stringify({ sims, opened: a.opened }));

  a.onSimilarClick(ev({ metaKey: true }));
  check('Cmd+ЛКМ — тоже новая вкладка (macOS)', a.opened.length === 2 && a.opened[1] === '/similar/9');

  a.onSimilarAuxClick(ev({ button: 1 }));
  check('средняя кнопка — /similar/9 в новой вкладке',
    a.opened.length === 3 && a.opened[2] === '/similar/9', JSON.stringify(a.opened));

  a.onSimilarAuxClick(ev({ button: 0 }));
  check('ЛКМ через auxclick игнорируется', a.opened.length === 3);
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);

