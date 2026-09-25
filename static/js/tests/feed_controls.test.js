// feed_controls.test.js — UX-улучшения ленты:
//  * Shift+клик по чекбоксу — выделение диапазона (якорь, замена, чекбоксы);
//  * кнопка «Наверх»: показ после ~1.5 экранов прокрутки, плавный скролл;
//  * полоса режима для «похожих» (i18n mode.similar);
//  * showGridMode не открывает закрытую панель профиля (глубокая ссылка).
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
defG('location', { href: 'https://localhost:3000/', origin: 'https://localhost:3000', pathname: '/', search: '' });
defG('history', { pushState() {}, replaceState() {}, back() {} });
defG('requestAnimationFrame', (f) => setTimeout(f, 0));

await import('../feed.js');

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [], query: '', viewerOpen: false, viewerIndex: 0,
    selected: new Set(), downloading: new Set(), downloadQueue: new Set(),
    profile: { liked_posts: [], hidden_posts: [] },
    profileOpen: false, displayMode: 'search', displayIds: [],
    recommendActive: false,
  };
  a.els = {};
  a.batchUpdates = 0;
  a.updateBatchBar = function () { a.batchUpdates++; };
  a.showToast = function () {};
  return a;
}

console.log('Shift-диапазон, кнопка «Наверх», полоса режима «похожие»\n');

// ── 1. Shift+клик по чекбоксу — диапазон ─────────────────────────────────
{
  const a = makeApp();
  a.state.posts = [1, 2, 3, 4, 5, 6].map(id => ({ id }));
  // Чекбоксы карточек 2..5 (grid-стаб отдаёт их на querySelectorAll).
  const cbs = [2, 3, 4, 5].map(id => ({ dataset: { id: String(id) }, classList: makeClassList() }));
  a.els.grid = { querySelector: () => null, querySelectorAll: () => cbs };

  a.toggleSelect(2, false);
  check('обычный клик: один пост выделен, якорь установлен',
    a.state.selected.size === 1 && a.state.selected.has(2) && a._lastSelId === 2,
    JSON.stringify({ sel: [...a.state.selected], anchor: a._lastSelId }));

  a.toggleSelect(5, true);
  check('Shift+клик: диапазон 2..5',
    a.state.selected.size === 4 && [2, 3, 4, 5].every(id => a.state.selected.has(id)),
    JSON.stringify([...a.state.selected]));
  check('Shift: якорь остался на прежнем обычном клике', a._lastSelId === 2, String(a._lastSelId));
  check('Shift: чекбоксы карточек синхронизированы',
    cbs.every(c => c.classList.contains('checked')),
    JSON.stringify(cbs.map(c => c.classList.contains('checked'))));

  a.toggleSelect(1, true);
  check('повторный Shift в обратную сторону: диапазон заменяется (1..2)',
    a.state.selected.size === 2 && a.state.selected.has(1) && a.state.selected.has(2) && !a.state.selected.has(5),
    JSON.stringify([...a.state.selected]));
  check('чекбокс удалённого из диапазона поста снят', !cbs[3].classList.contains('checked'));

  a.toggleSelect(4, false);
  check('обычный клик после диапазона: переключение + новый якорь',
    a.state.selected.has(4) && a._lastSelId === 4,
    JSON.stringify({ sel: [...a.state.selected], anchor: a._lastSelId }));

  a.toggleSelect(5, true);
  check('Shift от нового якоря: диапазон 4..5',
    a.state.selected.size === 2 && a.state.selected.has(4) && a.state.selected.has(5),
    JSON.stringify([...a.state.selected]));

  a.clearSelection();
  check('clearSelection: выбор пуст и якорь сброшен',
    a.state.selected.size === 0 && a._lastSelId == null && a.batchUpdates > 0,
    JSON.stringify({ size: a.state.selected.size, anchor: a._lastSelId }));

  a.toggleSelect(5, true);
  check('Shift после clearSelection — как обычный клик (нет якоря)',
    a.state.selected.size === 1 && a._lastSelId === 5, JSON.stringify([...a.state.selected]));

  // Shift по тому же посту, что и якорь — обычное переключение.
  a.toggleSelect(5, true);
  check('Shift по якорю — обычное переключение', a.state.selected.size === 0 && a._lastSelId === 5,
    JSON.stringify([...a.state.selected]));
}

// ── 2. Кнопка «Наверх» ───────────────────────────────────────────────────
{
  const a = makeApp();
  const bt = makeEl('button');
  bt.classList.add('hidden');
  a.els.backToTop = bt;
  const mainEl = { scrollTop: 0, clientHeight: 800, scrollHeight: 8000 };
  const origGet = document.getElementById;
  document.getElementById = (id) => (id === 'main' ? mainEl : null);

  mainEl.scrollTop = 1000; // < 1.5 экранов (1200)
  a.onMainScroll();
  check('«Наверх» скрыта на первом экране', bt.classList.contains('hidden'));

  mainEl.scrollTop = 3000;
  a.onMainScroll();
  check('«Наверх» видна после ~1.5 экранов прокрутки', !bt.classList.contains('hidden'));

  let scrolled = null;
  mainEl.scrollTo = (opts) => { scrolled = opts; };
  a.scrollToTop();
  check('клик по «Наверх» — плавный скролл наверх',
    scrolled && scrolled.top === 0 && scrolled.behavior === 'smooth', JSON.stringify(scrolled));

  delete mainEl.scrollTo;
  mainEl.scrollTop = 500;
  a.scrollToTop(); // фолбэк для браузеров без Element.scrollTo
  check('без scrollTo — scrollTop = 0', mainEl.scrollTop === 0, String(mainEl.scrollTop));

  document.getElementById = origGet;
}

// ── 3. Полоса режима для «похожих» ───────────────────────────────────────
{
  const a = makeApp();
  a.els.modeBar = makeEl('div');
  a.els.modeBar.classList.add('hidden');
  a.els.modeBarText = { textContent: '' };
  a.els.modeBarRefresh = makeEl('div');
  a.els.sentinel = makeEl('div');
  a.state.displayMode = 'similar';
  a.state.displayIds = [1, 2, 3];
  a.renderModeBar();
  check('полоса режима: «похожих (3)» вместо «коллекцию»',
    a.els.modeBarText.textContent.includes('похожих') && !a.els.modeBar.classList.contains('hidden'),
    a.els.modeBarText.textContent);
}

// ── 4. showGridMode и панель профиля ─────────────────────────────────────
{
  const origGet = API.get;
  API.get = async () => ({ posts: [{ id: 1 }] });
  const run = async (profileOpen) => {
    const a = makeApp();
    let toggles = 0;
    a.state.profileOpen = profileOpen;
    a.toggleProfile = () => { toggles++; };
    a.setStatus = () => {};
    a.showSkeletons = () => {};
    a.hideSkeletons = () => {};
    a.renderModeBar = () => {};
    a.renderPosts = () => {};
    a.pushState = () => {};
    await a.showGridMode('similar', [1]);
    return toggles;
  };
  const closed = await run(false);
  const opened = await run(true);
  API.get = origGet;
  check('showGridMode: закрытая панель профиля не открывается (глубокая ссылка)',
    closed === 0, 'toggles=' + closed);
  check('showGridMode: открытая панель профиля закрывается, как раньше',
    opened === 1, 'toggles=' + opened);
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);

