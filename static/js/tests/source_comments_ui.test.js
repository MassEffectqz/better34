// source_comments_ui.test.js — комментарии источника (dapi s=comment):
//  * определение сайта по хосту медиа (у постов из ленты post.source пуст);
//  * условие показа бейджа (has_comments из выдачи / счётчик из кэша);
//  * слой загрузки отдаёт данные и не трогает DOM;
//  * слияние с локальными комментариями в один список, сортировка по времени,
//    XSS-экранирование и обрезка текста с чужого сайта.
import { App } from '../state.js';
import '../source_comments.js';
import '../social.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {} });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
// esc() в utils.js: создаёт элемент, кладёт текст в textContent и читает
// innerHTML — браузер возвращает экранированную строку. Заглушка повторяет
// именно это поведение, иначе проверки XSS проходят бы вхолостую.
defG('document', {
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({
    set textContent(v) { this._t = String(v); },
    get textContent() { return this._t || ''; },
    get innerHTML() {
      return String(this._t || '')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    },
    set innerHTML(v) { this._t = String(v); },
    getAttribute: () => null,
  }),
});

// Узел для разметки вьюера: innerHTML хранится буквально (как в браузере),
// querySelector отдаёт дочерние узлы по id, чтобы работал рендер секции.
function node() {
  const kids = {};
  const self = {
    _html: '', textContent: '', className: '', hidden: false,
    disabled: false, title: '', value: '', parentElement: null,
    attributes: {},
    cls: new Set(),
    classList: {
      add(...c) { c.forEach((x) => self.cls.add(x)); },
      remove(...c) { c.forEach((x) => self.cls.delete(x)); },
      contains(c) { return self.cls.has(c); },
      toggle() {},
    },
    appendChild(c) { if (c) kids[c.id || Object.keys(kids).length] = c; },
    append(...c) { c.forEach((x) => { if (x) kids[Object.keys(kids).length] = x; }); },
    setAttribute(k, v) { this.attributes[k] = v; },
    removeAttribute(k) { delete this.attributes[k]; },
    addEventListener() {},
    querySelector(sel) {
      const id = String(sel).replace('#', '');
      if (id) {
        if (!kids[id]) { kids[id] = node(); kids[id].parentElement = this; }
        return kids[id];
      }
      return null;
    },
    querySelectorAll: () => [],
    children: kids,
  };
  Object.defineProperty(self, 'innerHTML', {
    get() { return self._html; },
    set(v) { self._html = String(v); },
  });
  return self;
}

console.log('Комментарии источника: UI\n');

// ── 1. Определение сайта ───────────────────────────────────────────────────
const a = Object.create(App);
check('сайт из post.source', a.sourceSiteOf({ source: 'Gelbooru' }) === 'gelbooru',
  a.sourceSiteOf({ source: 'Gelbooru' }));
check('сайт из хоста gelbooru', a.sourceSiteOf({ file_url: 'https://gelbooru.com/img/x/1.jpg' }) === 'gelbooru');
check('сайт из хоста hypnohub', a.sourceSiteOf({ file_url: 'https://hypnohub.net/img/x/1.jpg' }) === 'hypnohub');
check('сайт из preview_url', a.sourceSiteOf({ preview_url: 'https://i.rule34.xxx/1.jpg' }) === 'rule34',
  a.sourceSiteOf({ preview_url: 'https://i.rule34.xxx/1.jpg' }));
check('неизвестный хост → пусто', a.sourceSiteOf({ file_url: 'https://example.org/1.jpg' }) === '');
check('мусорный URL не роняет', a.sourceSiteOf({ file_url: 'не-урл' }) === '');
check('null-пост', a.sourceSiteOf(null) === '');

// ── 2. Условие бейджа ──────────────────────────────────────────────────────
check('бейдж: has_comments из выдачи', a.hasSourceComments({ has_comments: true }) === true);
check('бейдж: счётчик из кэша', a.hasSourceComments({ comment_count: 3 }) === true);
check('без бейджа: ни флага, ни счётчика', a.hasSourceComments({ has_comments: false, comment_count: 0 }) === false);
check('без бейджа: пост пустой', a.hasSourceComments({}) === false);
check('без бейджа: null', a.hasSourceComments(null) === false);

// ── 3. Слой загрузки: данные, а не DOM ─────────────────────────────────────
(async () => {
const api = { get: () => Promise.resolve({ site: 'hypnohub', count: 1, cached: true,
  comments: [{ id: 1, author: 'a', body: 'b', created_at: '2026-09-27 00:08' }] }) };
a.API = api;
let d = await a.loadSourceComments(5, 'hypnohub', {});
check('слой загрузки отдаёт комментарии', d.comments.length === 1 && d.count === 1, JSON.stringify(d));
check('слой загрузки не трогает DOM', typeof d.html === 'undefined');

// Пустой сайт — сразу unsupported, без похода в сеть.
let hit = 0;
a.API = { get: () => { hit++; return Promise.resolve({}); } };
d = await a.loadSourceComments(5, '', {});
check('пустой сайт: unsupported без запроса', d.unsupported === true && hit === 0, 'hit=' + hit);

// Сайт ответил «не поддерживает» — запоминаем и второй раз не идём на него.
a._srcUnsupported = new Set();
a.API = { get: () => { hit++; return Promise.resolve({ unsupported: true }); } };
hit = 0;
d = await a.loadSourceComments(5, 'gelbooru', {});
check('unsupported: помечен в ответе', d.unsupported === true, JSON.stringify(d));
check('unsupported: сайт запомнен', a._srcUnsupported.has('gelbooru'), [...a._srcUnsupported].join(','));
hit = 0;
d = await a.loadSourceComments(5, 'gelbooru', { cachedOnly: true });
check('unsupported: повторно не опрашиваем', hit === 0, 'hit=' + hit);

// Сбой сети не должен ронять панель: возвращаем error, а не исключение.
a._srcUnsupported = new Set();
a.API = { get: () => Promise.reject(new Error('offline')) };
d = await a.loadSourceComments(5, 'hypnohub', {});
check('ошибка сети: error вместо исключения', d.error === true && d.comments.length === 0, JSON.stringify(d));

// Явный refresh помечается в URL — иначе «обновить» ничего не делал бы.
const urls = [];
a.API = { get: (u) => { urls.push(u); return Promise.resolve({ site: 'hypnohub', comments: [] }); } };
await a.loadSourceComments(5, 'hypnohub', {});
await a.loadSourceComments(5, 'hypnohub', { refresh: true });
check('refresh=1 только по кнопке «обновить»',
  !/refresh=1/.test(urls[0]) && /refresh=1/.test(urls[1]), urls.join(' | '));
check('в запросе есть site и source_id',
  /site=hypnohub/.test(urls[0]) && /source_id=5/.test(urls[0]), urls[0]);
// Пассивная загрузка при открытии поста просит только кэш.
await a.loadSourceComments(5, 'hypnohub', { cachedOnly: true });
check('cached=1 при открытии поста', /cached=1/.test(urls[2]), urls[2]);

// ── 4. Слияние локальных и комментариев источника в один список ────────────
// renderMergedComments рисует в #vc-list; собираем узлы-приёмники заранее.
function mergeView(local, src, site) {
  const list = node();
  const count = node();
  const note = node();
  note.classList = { toggle() {}, add() {}, remove() {} };
  document.getElementById = (id) => ({ 'vc-list': list, 'vc-count': count, 'vc-src-note': note }[id] || null);
  a.state = { viewerOpen: true, user: { username: 'me' } };
  a._commentsSite = site;
  a._localComments = local;
  a._sourceComments = src;
  a._sourceNote = '';
  a.confirmDialog = () => Promise.resolve(true);
  a.renderMergedComments();
  return { list, count, note };
}

let v = mergeView(
  [{ id: 1, username: 'me', text: 'мой локальный', created_at: '2026-09-27T10:00:00Z' }],
  [{ id: 2, author: 'bob', body: 'с бура', created_at: '2026-09-27 09:00' }],
  'hypnohub'
);
check('слияние: оба комментария в одном списке',
  (v.list.innerHTML.match(/vc-item/g) || []).length === 2, v.list.innerHTML.slice(0, 200));
check('слияние: счётчик суммарный', v.count.textContent === '(2)', v.count.textContent);
check('слияние: сортировка по времени (сначала ранний)',
  v.list.innerHTML.indexOf('с бура') < v.list.innerHTML.indexOf('мой локальный'),
  v.list.innerHTML.slice(0, 240));
check('слияние: у комментария источника есть метка сайта',
  /vc-src-tag[^>]*>hypnohub</.test(v.list.innerHTML), v.list.innerHTML.slice(0, 240));
check('слияние: у своего комментария есть кнопка удаления',
  v.list.innerHTML.includes('data-cid="1"'), v.list.innerHTML.slice(0, 240));
check('слияние: у чужого комментария удаления нет',
  !v.list.innerHTML.includes('data-cid="2"'), v.list.innerHTML.slice(0, 240));

// Порядок при равных метках времени не должен прыгать между вызовами.
const local2 = [
  { id: 1, username: 'a', text: 'first', created_at: '2026-09-27T10:00:00Z' },
  { id: 2, username: 'b', text: 'second', created_at: '2026-09-27T10:00:00Z' },
];
const same1 = mergeView(local2, [], 'hypnohub').list.innerHTML;
const same2 = mergeView(local2, [], 'hypnohub').list.innerHTML;
check('стабильный порядок при равных датах', same1 === same2);

// Только комментарии источника — список не должен считаться пустым.
v = mergeView([], [{ id: 9, author: 'bob', body: 'только бур', created_at: '2026-09-27 09:00' }], 'hypnohub');
check('только источник: список не пустой', /только бур/.test(v.list.innerHTML), v.list.innerHTML.slice(0, 200));
check('только источник: счётчик 1', v.count.textContent === '(1)', v.count.textContent);

// Пусто везде — приглашение написать первый комментарий.
v = mergeView([], [], 'hypnohub');
check('пусто: приглашение написать', /будьте первым|be the first/i.test(v.list.innerHTML), v.list.innerHTML.slice(0, 140));

// XSS и обрезка — на данных с чужого сайта.
v = mergeView([], [
  { id: 1, author: '<img src=x onerror=alert(1)>', body: 'ok', created_at: '2026-09-27 00:08' },
  { id: 2, author: 'bob', body: 'y'.repeat(5000), created_at: '2026-09-27 00:09' },
], 'hypnohub');
check('XSS: тег автора не попал в DOM', !v.list.innerHTML.includes('<img'), v.list.innerHTML.slice(0, 200));
check('XSS: автор эскапирован', v.list.innerHTML.includes('&lt;img'), v.list.innerHTML.slice(0, 200));
check('длинный текст обрезан до лимита',
  v.list.innerHTML.includes('…') && !v.list.innerHTML.includes('y'.repeat(2001)));

// Заметка «источник не поддерживает» показывается только когда есть что сказать.
a._sourceNote = '';
v = mergeView([], [], 'hypnohub');
check('без заметки note пуст', v.note.textContent === '');

// ── 5. Фоновая предзагрузка комментариев источника ────────────────────────
// Ручной клик больше не нужен: посты с has_comments кэшируются сами, по одному
// и с паузой. setTimeout глушим, чтобы тест не ждал реальные 4 секунды между
// запросами, и двигаем очередь вручную.
const realTimeout = globalThis.setTimeout;
globalThis.setTimeout = () => 0;
const tick = () => new Promise((r) => realTimeout(r, 0));

a._srcUnsupported = new Set();
a._srcPrefetch = { queue: [], done: 0, running: false };
a.state = { viewerOpen: false, viewerIndex: -1, posts: [] };
a.updateCardCommentsBadge = function (p) { a._badged = p.id; };

const unsupportedSites = new Set(['gelbooru']);
a.API = { get: (u) => {
  const site = /site=([^&]+)/.exec(u)[1];
  if (unsupportedSites.has(site)) return Promise.resolve({ site, unsupported: true });
  return Promise.resolve({ site, count: 2,
    comments: [{ id: 1, author: 'a', body: 'b', created_at: '2026-09-27 00:08' }] });
} };

const posts = [
  { id: 1, has_comments: true, file_url: 'https://gelbooru.com/img/x/1.jpg' },
  { id: 2, has_comments: true, comment_count: 4, file_url: 'https://hypnohub.net/img/x/2.jpg' },
  { id: 3, has_comments: false, file_url: 'https://hypnohub.net/img/x/3.jpg' },
  { id: 4, file_url: 'https://hypnohub.net/img/x/4.jpg' },
  { id: 5, has_comments: true, file_url: 'https://example.org/5.jpg' },
  { id: 6, has_comments: true, file_url: 'https://hypnohub.net/img/x/6.jpg' },
];
a.state.posts = posts;
a.scheduleSourceCommentsPrefetch(posts);
// Первый пост (gelbooru) ушёл в работу сразу: счётчик вышел бы иначе.
check('в работу берётся has_comments с известным сайтом',
  a._srcPrefetch.running === true, 'running=' + a._srcPrefetch.running);
check('в очередь попал только следующий подходящий пост',
  JSON.stringify(a._srcPrefetch.queue.map((j) => j.id)) === '[6]',
  JSON.stringify(a._srcPrefetch.queue));

await tick(); // gelbooru ответил «не поддерживает»
check('сайт без комментариев запомнен и выкинут из очереди',
  a._srcUnsupported.has('gelbooru') && a._srcPrefetch.queue.length === 1,
  [...a._srcUnsupported].join(',') + ' queue=' + a._srcPrefetch.queue.length);

a._pumpSourceCommentsPrefetch(); // берём hypnohub
await tick();
check('после «unsupported» очередь продолжает работать',
  a._srcPrefetch.done === 2, 'done=' + a._srcPrefetch.done);
check('точный счётчик записан в пост ленты',
  posts.find((p) => p.id === 6).comment_count === 2,
  String(posts.find((p) => p.id === 6).comment_count));
check('бейдж карточки обновлён', a._badged === 6, 'badged=' + a._badged);

check('повторный вызов не дублирует', (() => {
  a.scheduleSourceCommentsPrefetch(posts);
  return a._srcPrefetch.queue.length === 0;
})(), String(a._srcPrefetch.queue.length));

check('открытый пост не ставим в очередь', (() => {
  a.state.viewerOpen = true; a.state.viewerIndex = 5; // открыт пост 6
  a._srcPrefetch.queue = []; a._srcPrefetch.done = 0; a._srcPrefetch.seen = new Set();
  a.scheduleSourceCommentsPrefetch(posts);
  const ids = JSON.stringify(a._srcPrefetch.queue.map((j) => j.id));
  a.state.viewerOpen = false;
  return ids === '[]';
})(), 'открытый пост должен пропускаться');

check('бюджет сессии ограничен', (() => {
  a._srcPrefetch = { queue: [], done: 0, running: false };
  const many = Array.from({ length: 60 }, (_, i) => ({
    id: 100 + i, has_comments: true, file_url: 'https://hypnohub.net/img/x/' + i + '.jpg' }));
  a.scheduleSourceCommentsPrefetch(many);
  return a._srcPrefetch.queue.length <= 40;
})(), 'очередь должна быть ограничена');

// В Node navigator — геттер, поэтому задаём его через defineProperty.
const setOnline = (v) => Object.defineProperty(globalThis, 'navigator',
  { value: { onLine: v }, configurable: true, writable: true });
check('оффлайн: очередь не трогается', (() => {
  a._srcPrefetch = { queue: [{ id: 77, site: 'hypnohub' }], done: 0, running: false };
  setOnline(false);
  a._pumpSourceCommentsPrefetch();
  setOnline(true);
  return a._srcPrefetch.queue.length === 1 && a._srcPrefetch.running === false;
})(), 'без сети очередь должна ждать');

globalThis.setTimeout = realTimeout;

console.log(`\nИтог: ${passed} ok, ${failed} fail`);
if (failed) process.exit(1);
})();