// history_deeplink.test.js — URL как состояние модалки-вьювера:
//  * разбор пути /search/<теги>[/post/<id>] и /post/<id>;
//  * «назад»/«вперёд» открывают и закрывают вьювер, без дублей в истории;
//  * открытие поста по id с догрузкой, если он не попал в выдачу (глубокая ссылка);
//  * Ctrl/Cmd+ЛКМ по карточке — открытие поста в новой вкладке.
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
    append(...nodes) { nodes.forEach(n => this.children.push(n)); },
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
defG('location', { href: 'https://localhost:3000/', origin: 'https://localhost:3000', pathname: '/', search: '' });

// history-стаб: пишем вызовы, state можно подменять как в браузере.
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
  };
  a.els = { searchInput: makeEl('input') };
  a.viewerCalls = [];
  a.closed = 0;
  a.opened = [];
  a.toasts = [];
  a.openViewer = function (i) { a.viewerCalls.push(i); };
  a.closeViewer = function () { a.closed++; };
  a.showToast = function (m) { a.toasts.push(m); };
  a._isTouch = () => false;
  return a;
}

const setPath = (p) => { location.pathname = p; };
const reset = () => { hist.calls = []; hist.state = null; };

console.log('URL ↔ модалка-вьювер (глубокие ссылки, назад/вперёд)\n');

// ── 1. Разбор путей ──────────────────────────────────────────────────────
{
  const a = makeApp();
  const dl = a.parseLocation('/search/eris_greyrat/post/14719307');
  check('parseLocation: глубокая ссылка со слагом поста',
    dl.matched && dl.query === 'eris_greyrat' && dl.postId === 14719307, JSON.stringify(dl));

  const enc = a.parseLocation('/search/big%20breasts%20%2Bcat/post/5');
  check('parseLocation: %-экранирование в тегах', enc.query === 'big breasts +cat', JSON.stringify(enc));

  const only = a.parseLocation('/search/solo');
  check('parseLocation: поиск без поста', only.matched && only.query === 'solo' && only.postId === null, JSON.stringify(only));

  const p = a.parseLocation('/post/42');
  check('parseLocation: /post/<id>', p.matched && p.query === '' && p.postId === 42, JSON.stringify(p));

  const root = a.parseLocation('/');
  check('parseLocation: корень', root.matched && root.query === '' && root.postId === null, JSON.stringify(root));

  const unknown = a.parseLocation('/qr');
  check('parseLocation: неизвестный маршрут не матчится', !unknown.matched);
}

// ── 2. Согласованность URL и pushState/replaceState ──────────────────────
{
  const a = makeApp();
  check('postUrl: запрос + пост', a.postUrl('solo', 7) === '/search/solo/post/7');
  check('postUrl: без запроса', a.postUrl('', 7) === '/post/7');
  check('postUrl: запрос без поста', a.postUrl('solo', null) === '/search/solo');
  check('postUrl: корень', a.postUrl('', null) === '/');

  reset();
  a._lastURL = '/search/solo/post/7';
  a.pushState('solo', 7);
  check('pushState: тот же URL не создаёт вторую запись истории', hist.calls.length === 0, JSON.stringify(hist.calls));

  a.pushState('solo', null);
  check('pushState: закрытие поста ведёт на URL запроса',
    hist.calls.length === 1 && hist.calls[0][0] === 'push' && hist.calls[0][1] === '/search/solo', JSON.stringify(hist.calls));

  a.replaceState('solo', 9);
  check('replaceState: листание постов не добавляет запись в историю',
    hist.calls.length === 2 && hist.calls[1][0] === 'replace' && hist.calls[1][1] === '/search/solo/post/9',
    JSON.stringify(hist.calls));
}

// ── 3. Закрытие вьювера и история ────────────────────────────────────────
{
  const a = makeApp();
  a.state.query = 'solo';

  // Пост открыт кликом по карточке: запись истории наша → уходим назад,
  // иначе в истории остаётся дубль и «назад» требует лишних нажатий.
  reset();
  hist.state = { query: 'solo', postId: 7 };
  a._closeModalHistory();
  check('закрытие вьювера: своя запись истории → history.back()',
    hist.calls.length === 1 && hist.calls[0][0] === 'back', JSON.stringify(hist.calls));

  // Глубокая ссылка (реальная загрузка страницы): записи истории наши нет.
  reset();
  a._lastURL = '/search/solo/post/7';
  a._closeModalHistory();
  check('закрытие по deep link: без history.back(), URL чистится от поста',
    hist.calls.length === 1 && hist.calls[0][0] === 'push' && hist.calls[0][1] === '/search/solo',
    JSON.stringify(hist.calls));
}

// ── 4. Открытие поста по id: лента, догрузка, недоступный пост ───────────
await (async () => {
  // Пост есть в ленте — доп. запросов нет.
  let a = makeApp();
  a.state.posts = [{ id: 1 }, { id: 2 }];
  let apiCalls = [];
  API.get = async (url) => { apiCalls.push(url); return { posts: [] }; };
  let ok = await a.openViewerByPostId(2);
  check('пост из ленты: открывается без доп. запроса',
    ok && a.viewerCalls[0] === 1 && apiCalls.length === 0, JSON.stringify({ viewerCalls: a.viewerCalls, apiCalls }));

  // Поста нет в выдаче (старая ссылка) — достраиваем через /posts-by-ids.
  a = makeApp();
  a.state.posts = [{ id: 1 }];
  apiCalls = [];
  API.get = async (url) => {
    apiCalls.push(url);
    return { posts: [{ id: 14719307, tags: 'eris_greyrat', file_url: 'https://x/1.jpg' }] };
  };
  ok = await a.openViewerByPostId(14719307);
  check('глубокая ссылка: пост достраивается /posts-by-ids',
    ok && apiCalls.length === 1 && apiCalls[0] === '/posts-by-ids?ids=14719307', JSON.stringify(apiCalls));
  check('глубокая ссылка: пост добавлен в ленту и открыт',
    a.state.posts.length === 2 && a.state.posts[1].id === 14719307 && a.viewerCalls[0] === 1,
    JSON.stringify({ posts: a.state.posts.map(p => p.id), viewerCalls: a.viewerCalls }));

  // Пост недоступен на источнике — вьювер не открываем, сообщаем пользователю.
  a = makeApp();
  API.get = async () => ({ posts: [] });
  ok = await a.openViewerByPostId(404);
  check('недоступный пост: тост и вьювер не открывается',
    !ok && a.viewerCalls.length === 0 && a.toasts.length === 1, JSON.stringify(a.toasts));

  // ── 5. Синхронизация UI с URL (popstate / pageshow) ────────────────────
  a = makeApp();
  a.state.query = 'eris_greyrat';
  a.state.posts = [{ id: 1 }];
  a.loadPosts = async () => {};
  API.get = async () => ({ posts: [{ id: 14719307 }] });
  setPath('/search/eris_greyrat/post/14719307');
  await a._applyLocationState();
  check('URL со слагом поста: вьювер открывается даже без поста в ленте',
    a.viewerCalls.length === 1 && a.state.posts.length === 2, JSON.stringify({ viewerCalls: a.viewerCalls }));

  // Назад на URL без поста → модалка закрывается.
  a = makeApp();
  a.state.query = 'eris_greyrat';
  a.state.posts = [{ id: 1 }];
  a.state.viewerOpen = true;
  a.loadPosts = async () => {};
  setPath('/search/eris_greyrat');
  await a._applyLocationState();
  check('URL без поста: открытая модалка закрывается',
    a.closed === 1 && a.viewerCalls.length === 0, JSON.stringify({ closed: a.closed }));

  // Смена запроса в URL (назад на предыдущий поиск) — перезагрузка ленты.
  a = makeApp();
  a.state.query = 'solo';
  a.state.posts = [{ id: 1 }];
  let loads = 0;
  a.loadPosts = async () => { loads++; a.state.posts = [{ id: 5 }]; };
  setPath('/search/cat');
  await a._applyLocationState();
  check('смена запроса в URL: лента перезагружается, поле поиска синхронно',
    loads === 1 && a.state.query === 'cat' && a.els.searchInput.value === 'cat',
    JSON.stringify({ loads, query: a.state.query, input: a.els.searchInput.value }));

  // Неизвестный маршрут (например /qr) не трогает ленту и модалку.
  a = makeApp();
  loads = 0;
  a.loadPosts = async () => { loads++; };
  setPath('/qr');
  await a._applyLocationState();
  check('неизвестный маршрут: лента и модалка не трогаются', loads === 0 && a.closed === 0);

  // ── 6. Ctrl/Cmd+ЛКМ и средняя кнопка по карточке ленты ────────────────
  a = makeApp();
  a.state.query = 'solo';
  a.pageSize = () => 10;
  a.openInNewTab = (url) => a.opened.push(url);
  const post = {
    id: 7, file_type: 'jpg', preview_url: 'https://x/7t.jpg', file_url: 'https://x/7.jpg',
    width: 100, height: 100, score: 1,
  };
  const card = a.createPostCard(post);
  card.dataset.index = '3';
  const click = (extra) => card.listeners.click[0](Object.assign({
    ctrlKey: false, metaKey: false, button: 0, clientX: 0, clientY: 0,
    target: { closest: () => null }, preventDefault() {},
  }, extra));

  click({ ctrlKey: true });
  check('Ctrl+ЛКМ по карточке: пост уходит в новую вкладку',
    a.opened.length === 1 && a.opened[0] === '/search/solo/post/7' && a.viewerCalls.length === 0,
    JSON.stringify({ opened: a.opened, viewerCalls: a.viewerCalls }));

  click({ metaKey: true });
  check('Cmd+ЛКМ по карточке: то же поведение (macOS)', a.opened.length === 2, JSON.stringify(a.opened));

  card.listeners.auxclick[0]({ button: 1, target: { closest: () => null }, preventDefault() {} });
  check('средняя кнопка мыши: тоже новая вкладка', a.opened.length === 3, JSON.stringify(a.opened));

  click({});
  check('обычный клик по карточке: открывается вьювер',
    a.viewerCalls.length === 1 && a.viewerCalls[0] === 3 && a.opened.length === 3,
    JSON.stringify({ viewerCalls: a.viewerCalls, opened: a.opened }));

  check('isOpenInNewTabClick: без модификаторов — false',
    !a.isOpenInNewTabClick({}) && !a.isOpenInNewTabClick(null));

  // ── 7. Ряд «Похожие по тегам»: Ctrl/Cmd+ЛКМ → новая вкладка ──────────
  a = makeApp();
  a.state.query = 'solo';
  a.openInNewTab = (url) => a.opened.push(url);
  a.chain = [];
  a.openRelatedChain = (p, list) => a.chain.push([p.id, (list || []).length]);
  a._relPosts = [{ id: 11 }, { id: 22 }];
  const relEv = (extra) => Object.assign({
    target: { closest: (sel) => (sel === '.rel-item' ? { dataset: { idx: '1' } } : null) },
    preventDefault() {},
  }, extra);

  a.onRelatedClick(relEv({ ctrlKey: true }));
  check('Ctrl+ЛКМ по «Похожие по тегам»: пост открывается в новой вкладке',
    a.opened.length === 1 && a.opened[0] === '/search/solo/post/22' && a.chain.length === 0,
    JSON.stringify({ opened: a.opened, chain: a.chain }));

  a.onRelatedAuxClick(relEv({ button: 1 }));
  check('средняя кнопка по «Похожие по тегам»: тоже новая вкладка',
    a.opened.length === 2 && a.opened[1] === '/search/solo/post/22', JSON.stringify(a.opened));

  a.onRelatedKeydown(relEv({ key: 'Enter', ctrlKey: true }));
  check('Ctrl+Enter по «Похожие по тегам»: новая вкладка', a.opened.length === 3, JSON.stringify(a.opened));

  a.onRelatedClick(relEv({}));
  check('обычный клик по «Похожие по тегам»: открывается цепочка похожих',
    a.chain.length === 1 && a.chain[0][0] === 22 && a.chain[0][1] === 2, JSON.stringify(a.chain));

  a.onRelatedKeydown(relEv({ key: 'Enter' }));
  check('Enter по «Похожие по тегам»: цепочка без новой вкладки',
    a.chain.length === 2 && a.opened.length === 3, JSON.stringify({ chain: a.chain, opened: a.opened }));

  a.onRelatedKeydown(relEv({ key: ' ' }));
  check('Space не активирует миниатюру (во вьювере это слайдшоу)',
    a.chain.length === 2 && a.opened.length === 3, JSON.stringify(a.chain));

  a.onRelatedAuxClick(relEv({ button: 0 }));
  check('ЛКМ через auxclick игнорируется', a.opened.length === 3);

  a.onRelatedClick(relEv({ ctrlKey: true, target: { closest: () => null } }));
  check('Ctrl+ЛКМ мимо миниатюры: ничего не открывается', a.opened.length === 3);

  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed) throw new Error(`${failed} checks failed`);
})();


