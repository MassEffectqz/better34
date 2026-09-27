// profile_thumbs.test.js — вкладки «Лайки»/«Скрытые» профиля:
//  * фильтры (все/скачанные/невиденные/видео/недоступные) по данным /posts-by-ids;
//  * сортировка по liked_at (лайки) и по id (скрытые) с сохранением в localStorage;
//  * быстрые действия с плитки с оптимистичным откатом при ошибке;
//  * мульти-выбор и пакетные действия.
import { App } from '../state.js';
import { API } from '../api.js';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import '../profile.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {}, innerWidth: 1400, innerHeight: 800 });
defG('localStorage', {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
});

// ── Мини-DOM: плитки, контейнер вкладки, счётчик и тулбар ─────────────────
function makeClassList() {
  const s = new Set();
  return {
    contains: c => s.has(c),
    add: (...c) => c.forEach(x => s.add(x)),
    remove: (...c) => c.forEach(x => s.delete(x)),
    toggle(c, f) { const on = f != null ? !!f : !s.has(c); if (on) s.add(c); else s.delete(c); return on; },
  };
}
function makeTile(id, post) {
  return {
    dataset: { pfId: String(id) },
    hidden: false,
    removed: false,
    _pfPost: post || { id },
    classList: makeClassList(),
    remove() { this.removed = true; },
  };
}
function makeList() {
  // Настоящий appendChild переносит узел, а не дублирует: повторная
  // сортировка переставляет плитки, и в children должна остаться одна копия.
  return {
    children: [],
    // loadMoreThumbs снимает скелетоны через querySelectorAll; без этого
    // исключение уходит в его catch и молча обрывает обработку ответа.
    querySelectorAll: () => [],
    appendChild(c) {
      const i = this.children.indexOf(c);
      if (i >= 0) this.children.splice(i, 1);
      this.children.push(c);
      return c;
    },
  };
}
function makeTools() {
  const chip = (v) => ({ dataset: v, classList: makeClassList(), _attrs: {}, setAttribute(k, val) { this._attrs[k] = val; } });
  // this в стрелках не годится: функции создаются на верхнем уровне модуля,
  // где this === undefined. Держим ссылки на сам объект.
  const tools = {
    _sorts: ['new', 'old'].map(v => chip({ sort: v })),
    _filters: ['all', 'downloaded', 'unviewed', 'video', 'unavailable'].map(v => chip({ filter: v })),
    querySelector: () => null,
  };
  tools.querySelectorAll = (sel) => (sel === '[data-sort]' ? tools._sorts : sel === '[data-filter]' ? tools._filters : []);
  return tools;
}
function makeSelbar() {
  const bar = {
    hidden: true,
    _count: { textContent: '' },
    _btns: ['download', 'unlike', 'unhide', 'clearUnavailable'].map(v => ({ dataset: { selact: v }, disabled: false })),
  };
  bar.querySelector = (sel) => (sel === '.pf-selbar-count' ? bar._count : null);
  bar.querySelectorAll = () => bar._btns;
  return bar;
}

const dom = {
  'likes-shown-count': { textContent: '' },
  'hides-shown-count': { textContent: '' },
  'likes-tools': makeTools(),
  'hides-tools': makeTools(),
  'likes-selbar': makeSelbar(),
  'hides-selbar': makeSelbar(),
};
defG('document', {
  getElementById: (id) => dom[id] || null,
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({ classList: makeClassList(), style: { setProperty() {} }, dataset: {}, appendChild() {} }),
  createDocumentFragment: () => makeList(),
  addEventListener() {}, removeEventListener() {},
  documentElement: { lang: 'ru' },
});

let posted = [];
let failPosts = false;
const apiPost = async function (url) {
  posted.push(url);
  if (failPosts) throw new Error('offline');
  return {};
};
API.post = apiPost;
API.invalidate = function () {};

// Реальные оптимистичные хелперы (optimisticLike/optimisticHide) живут во
// viewer.js — импортируем его, чтобы проверять связку целиком.
await import('../viewer.js');

function makeApp() {
  const a = Object.create(App);
  a.state = { profile: { liked_posts: [], hidden_posts: [], liked_at: {} } };
  a.els = { likesList: makeList(), hidesList: makeList() };
  a._thumbs = {};
  a._thumbOpts = { likes: { sort: 'new', filter: 'all' }, hides: { sort: 'new', filter: 'all' } };
  a._thumbOptsLoaded = true;
  a._thumbSel = { likes: new Set(), hides: new Set() };
  a._thumbSelMode = { likes: false, hides: false };
  a.toasts = [];
  a.showToast = function (msg, kind) { a.toasts.push([msg, kind]); };
  // Хелперы оптимистичных обновлений дёргают сетку ленты — она тут не в фокусе.
  a.updateCardLike = function () {};
  a._scheduleRecommendRefresh = function () {};
  a._recSendDislike = function () {};
  a.downloadPost = function (p) { a.dl = (a.dl || []).concat(p.id); };
  a.loadProfile = function () { a.reloaded = (a.reloaded || 0) + 1; };
  a.confirmDialog = async function () { return a.confirmAnswer !== false; };
  return a;
}

console.log('Профиль: лайки/скрытые — фильтры, сортировка, действия, выбор\n');

// ── 0. Сетка не схлопывается ─────────────────────────────────────────────
// У плитки ВСЕ дети (картинка, заглушка, бейджи, панель действий) стоят
// position:absolute, поэтому высоту даёт только aspect-ratio. JS ставит его из
// размеров поста, но лишь когда размеры пришли; у постов без width/height база
// обязана остаться в CSS, иначе плитка сжимается в полоску, а картинка
// обрезается в ноль — сетка выглядит сломанной.
{
  const here = dirname(fileURLToPath(import.meta.url));
  const css = fs.readFileSync(join(here, '..', '..', 'css', 'style.css'), 'utf8');
  const rule = (sel) => {
    const i = css.indexOf('\n' + sel);
    return i < 0 ? '' : css.slice(i, css.indexOf('}', i));
  };
  const thumb = rule('.pf-thumb{');
  const skel = rule('.pf-thumb-skeleton{');
  check('плитка имеет aspect-ratio по умолчанию', /aspect-ratio:\s*3\/4/.test(thumb), thumb.slice(0, 140));
  check('скелетон и плитка в одних пропорциях',
    /aspect-ratio:\s*3\/4/.test(thumb) && /aspect-ratio:\s*3\/4/.test(skel), skel.slice(0, 140));
}
// ── 1. Фильтры ────────────────────────────────────────────────────────────
{
  const a = makeApp();
  const dl = { id: 1, downloaded: true, viewed: true };
  const fresh = { id: 2, viewed: false };
  const video = { id: 3, file_type: 'video', viewed: true };
  const gone = { id: 4, missing: true, viewed: true };
  check('фильтр «все» отдаёт всё', a._thumbMatchesFilter(fresh, 'all') && a._thumbMatchesFilter(gone, 'all'));
  check('фильтр «скачанные» ловит downloaded', a._thumbMatchesFilter(dl, 'downloaded') && !a._thumbMatchesFilter(fresh, 'downloaded'));
  check('фильтр «невиденные» ловит viewed=false', a._thumbMatchesFilter(fresh, 'unviewed') && !a._thumbMatchesFilter(dl, 'unviewed'));
  check('фильтр «видео» ловит video/gif',
    a._thumbMatchesFilter(video, 'video') && a._thumbMatchesFilter({ id: 5, file_type: 'gif' }, 'video')
    && !a._thumbMatchesFilter(dl, 'video'));
  check('фильтр «недоступные» ловит заглушки', a._thumbMatchesFilter(gone, 'unavailable') && !a._thumbMatchesFilter(dl, 'unavailable'));
  check('неизвестный фильтр ничего не прячет', a._thumbMatchesFilter(fresh, 'что-то'));
}

// ── 2. Сортировка и её сохранение ─────────────────────────────────────────
{
  const a = makeApp();
  a.state.profile.liked_at = { 1: 100, 2: 300, 3: 200 };
  check('ключ сортировки лайков — время лайка', a._thumbSortKey('likes', { id: 1 }) === 100);
  check('лайк без метки времени сортируется по id', a._thumbSortKey('likes', { id: 7 }) === 7);
  check('скрытые сортируются по id', a._thumbSortKey('hides', { id: 7 }) === 7 && a._thumbSortKey('hides', { id: 2 }) === 2);

  a._thumbs.likes = { key: 'k', ids: [1, 2, 3], posts: [], loaded: 3, token: 0, missing: [], tiles: [] };
  a._thumbs.likes.tiles = [
    makeTile(1, { id: 1, viewed: true }), makeTile(2, { id: 2, viewed: true }), makeTile(3, { id: 3, viewed: true }),
  ];
  a.setThumbSort('likes', 'old');
  const orderNewFirst = a.els.likesList.children.map(t => Number(t.dataset.pfId));
  check('сортировка «сначала новые»: 2 (300) → 3 (200) → 1 (100)',
    JSON.stringify(orderNewFirst) === JSON.stringify([2, 3, 1]), JSON.stringify(orderNewFirst));
  check('сортировка сохранена в localStorage',
    (localStorage.getItem('briefly_thumb_opts') || '').includes('"sort":"old"'), localStorage.getItem('briefly_thumb_opts'));

  a.setThumbSort('likes', 'new');
  const orderOldFirst = a.els.likesList.children.map(t => Number(t.dataset.pfId));
  check('сортировка «сначала старые»: 1 → 3 → 2',
    JSON.stringify(orderOldFirst) === JSON.stringify([1, 3, 2]), JSON.stringify(orderOldFirst));
  check('сортировка применяется только к загруженным плиткам', a._thumbs.likes.tiles.length === 3);

  const b = makeApp();
  b._thumbOptsLoaded = false;
  const opts = b.loadThumbOpts();
  check('опции восстанавливаются из localStorage', opts.likes.sort === 'new' && opts.likes.filter === 'all', JSON.stringify(opts));
}
// ── 3. Фильтр прячет плитки и обновляет счётчик ───────────────────────────
{
  const a = makeApp();
  a._thumbs.likes = { key: 'k', ids: [1, 2, 3, 4], posts: [], loaded: 4, token: 0, missing: [], tiles: [] };
  a._thumbs.likes.tiles = [
    makeTile(1, { id: 1, downloaded: true, viewed: true }),
    makeTile(2, { id: 2, viewed: false }),
    makeTile(3, { id: 3, viewed: true }),
    makeTile(4, { id: 4, missing: true, viewed: true }),
  ];
  a.setThumbFilter('likes', 'unviewed');
  const visible = a._thumbs.likes.tiles.filter(x => !x.hidden).map(x => Number(x.dataset.pfId));
  check('активный фильтр оставляет только невиденные', JSON.stringify(visible) === JSON.stringify([2]), JSON.stringify(visible));
  check('скрытая плитка помечена классом pf-hidden', a._thumbs.likes.tiles[0].classList.contains('pf-hidden'));
  check('счётчик показывает «показано 1 из 4»',
    dom['likes-shown-count'].textContent === 'Показано 1 из 4', dom['likes-shown-count'].textContent);

  a.setThumbFilter('likes', 'all');
  check('возврат к «всем» показывает все плитки', a._thumbs.likes.tiles.every(x => !x.hidden));
  const activeFilter = dom['likes-tools']._filters.find(c => c.classList.contains('active'));
  check('активный чип фильтра подсвечен', !!activeFilter && activeFilter.dataset.filter === 'all',
    activeFilter && activeFilter.dataset.filter);
  const inactive = dom['likes-tools']._filters.find(c => c.dataset.filter === 'unviewed');
  check('неактивный чип помечен aria-pressed=false', inactive._attrs['aria-pressed'] === 'false', JSON.stringify(inactive._attrs));
}

// ── 4. Быстрые действия ───────────────────────────────────────────────────
{
  const a = makeApp();
  a.state.profile.liked_posts = [1];
  const tile = makeTile(1, { id: 1, viewed: true });
  a._thumbs.likes = { key: 'k', ids: [1], posts: [{ id: 1 }], loaded: 1, token: 0, missing: [], tiles: [tile] };
  posted = [];
  a.onThumbAction('likes', { id: 1 }, 'like', tile);
  check('снятие лайка убирает id из профиля сразу (оптимистично)',
    !a.state.profile.liked_posts.includes(1), JSON.stringify(a.state.profile.liked_posts));
  check('запрос ушёл на /like/1', posted.includes('/like/1'), JSON.stringify(posted));
  await new Promise(r => setTimeout(r, 5));
  check('плитка убрана из вкладки и из кэша', a._thumbs.likes.tiles.length === 0 && a._thumbs.likes.ids.length === 0);
}
{
  // Ошибка запроса — откат: лайк возвращается, плитка остаётся на месте.
  const a = makeApp();
  a.state.profile.liked_posts = [2];
  const tile = makeTile(2, { id: 2, viewed: true });
  a._thumbs.likes = { key: 'k', ids: [2], posts: [{ id: 2 }], loaded: 1, token: 0, missing: [], tiles: [tile] };
  posted = [];
  failPosts = true;
  a.onThumbAction('likes', { id: 2 }, 'like', tile);
  await new Promise(r => setTimeout(r, 5));
  failPosts = false;
  check('при ошибке запроса лайк откатывается', a.state.profile.liked_posts.includes(2), JSON.stringify(a.state.profile.liked_posts));
  check('при ошибке запроса плитка остаётся', a._thumbs.likes.tiles.length === 1);
  check('при ошибке показан тост с ошибкой', a.toasts.some(([, kind]) => kind === 'error'), JSON.stringify(a.toasts));
}
{
  // Во вкладке «Скрытые» кнопка возвращает пост, во вкладке «Лайки» — скрывает.
  const a = makeApp();
  a.state.profile.hidden_posts = [3];
  const tile = makeTile(3, { id: 3, viewed: true });
  a._thumbs.hides = { key: 'k', ids: [3], posts: [{ id: 3 }], loaded: 1, token: 0, missing: [], tiles: [tile] };
  posted = [];
  a.onThumbAction('hides', { id: 3 }, 'hide', tile);
  await new Promise(r => setTimeout(r, 5));
  check('возврат из скрытых убирает id из hidden_posts',
    !a.state.profile.hidden_posts.includes(3) && posted.includes('/hide/3'), JSON.stringify(posted));
  check('после возврата плитка убрана из вкладки «Скрытые»', a._thumbs.hides.tiles.length === 0);
}
// ── 5. Мульти-выбор ───────────────────────────────────────────────────────
{
  const a = makeApp();
  a._thumbs.likes = { key: 'k', ids: [1, 2, 3], posts: [], loaded: 3, token: 0, missing: [], tiles: [] };
  a._thumbs.likes.tiles = [
    makeTile(1, { id: 1, viewed: true }),
    makeTile(2, { id: 2, viewed: false }),
    makeTile(3, { id: 3, viewed: true }),
  ];
  a.setThumbSelectMode('likes', true);
  check('режим выбора включён', a._thumbSelMode.likes === true);
  check('панель выбора показана', dom['likes-selbar'].hidden === false);
  check('кнопки панели заблокированы без выделения', dom['likes-selbar']._btns.every(b => b.disabled === true));

  a.toggleThumbSelect('likes', 2, a._thumbs.likes.tiles[1]);
  check('клик по чекбоксу выделяет плитку', a._thumbSel.likes.has(2) && a._thumbs.likes.tiles[1].classList.contains('pf-checked'));
  check('панель показывает число выбранных',
    dom['likes-selbar']._count.textContent === 'Выбрано: 1', dom['likes-selbar']._count.textContent);
  check('после выделения кнопки панели разблокированы', dom['likes-selbar']._btns.every(b => b.disabled === false));

  a.setThumbFilter('likes', 'unviewed');
  a.selectAllThumbs('likes', true);
  check('«выбрать все» берёт только видимые под фильтром плитки',
    a._thumbSel.likes.size === 1 && a._thumbSel.likes.has(2), JSON.stringify([...a._thumbSel.likes]));

  a.setThumbSelectMode('likes', false);
  check('выход из режима выбора очищает выделение', a._thumbSelMode.likes === false && a._thumbSel.likes.size === 0);
  check('панель выбора скрыта', dom['likes-selbar'].hidden === true);
}

// ── 6. Пакетные действия ──────────────────────────────────────────────────
{
  const a = makeApp();
  a.state.profile.liked_posts = [1, 2];
  a._thumbs.likes = {
    key: 'k', ids: [1, 2, 3], posts: [{ id: 1 }, { id: 2 }, { id: 3 }], loaded: 3, token: 0, missing: [],
    tiles: [makeTile(1, { id: 1 }), makeTile(2, { id: 2 }), makeTile(3, { id: 3 })],
  };
  a._thumbSel.likes = new Set([1, 2]);
  posted = [];
  await a.thumbSelAction('likes', 'unlike');
  check('пакетное снятие лайков: запрос на каждый выбранный id',
    posted.includes('/like/1') && posted.includes('/like/2') && posted.length === 2, JSON.stringify(posted));
  check('пакетное снятие лайков: лайки убраны из профиля',
    a.state.profile.liked_posts.length === 0, JSON.stringify(a.state.profile.liked_posts));
  check('пакетное снятие лайков: плитки убраны из вкладки',
    a._thumbs.likes.tiles.length === 1 && Number(a._thumbs.likes.tiles[0].dataset.pfId) === 3,
    JSON.stringify(a._thumbs.likes.tiles.map(x => x.dataset.pfId)));
  check('выделение очищено', a._thumbSel.likes.size === 0);
}
{
  // Частичный сбой: успешные снимаются, неуспешные остаются в выделении.
  const a = makeApp();
  a.state.profile.liked_posts = [1, 2];
  a._thumbs.likes = {
    key: 'k', ids: [1, 2], posts: [{ id: 1 }, { id: 2 }], loaded: 2, token: 0, missing: [],
    tiles: [makeTile(1, { id: 1 }), makeTile(2, { id: 2 })],
  };
  a._thumbSel.likes = new Set([1, 2]);
  posted = [];
  API.post = async function (url) { posted.push(url); if (url === '/like/2') throw new Error('offline'); return {}; };
  await a.thumbSelAction('likes', 'unlike');
  check('при частичном сбое успешные id сняты, неуспешные остались выделены',
    a._thumbSel.likes.size === 1 && a._thumbSel.likes.has(2), JSON.stringify([...a._thumbSel.likes]));
  check('при частичном сбое лайк 1 снят из профиля', !a.state.profile.liked_posts.includes(1));
  check('при частичном сбое лайк 2 остался в профиле', a.state.profile.liked_posts.includes(2));
  API.post = apiPost;
}
{
  // Скачивание выбранных: в очередь уходят только загруженные посты.
  const a = makeApp();
  a._thumbs.hides = { key: 'k', ids: [1, 9], posts: [{ id: 1 }], loaded: 2, token: 0, missing: [9], tiles: [makeTile(1, { id: 1 })] };
  a._thumbSel.hides = new Set([1, 9]);
  await a.thumbSelAction('hides', 'download');
  check('в очередь ушли только загруженные посты', JSON.stringify(a.dl) === JSON.stringify([1]), JSON.stringify(a.dl));
  check('показан тост о числе поставленных в очередь', a.toasts.some(([m]) => m === 'В очередь: 1'), JSON.stringify(a.toasts));
}
{
  // Удаление недоступных из списка — только после подтверждения.
  const a = makeApp();
  a.confirmAnswer = false;
  a._thumbs.likes = {
    key: 'k', ids: [1, 2], posts: [], loaded: 2, token: 0, missing: [2],
    tiles: [makeTile(1, { id: 1 }), makeTile(2, { id: 2, missing: true })],
  };
  await a.thumbSelAction('likes', 'clearUnavailable');
  check('отказ в диалоге оставляет недоступные на месте', a._thumbs.likes.tiles.length === 2);
  a.confirmAnswer = true;
  await a.thumbSelAction('likes', 'clearUnavailable');
  check('подтверждение убирает только заглушки',
    a._thumbs.likes.tiles.length === 1 && a._thumbs.likes.tiles[0].removed === false, JSON.stringify(a._thumbs.likes.tiles.length));
  check('недоступные выброшены из ids кэша', !a._thumbs.likes.ids.includes(2), JSON.stringify(a._thumbs.likes.ids));
  check('после очистки показан тост с числом убранных',
    a.toasts.some(([m]) => m === 'Убрано недоступных: 1'), JSON.stringify(a.toasts));
}

// ── Непроверенные и недоступные — разные вещи ────────────────────────────
// Сервер отдаёт unresolved для id, о которых источник не ответил. Клиент
// обязан показать по ним «не удалось проверить» с кнопкой повтора и НЕ
// писать их в s.missing: иначе одна сетевая ошибка закрепляет живой пост
// как удалённый до перезагрузки страницы.
{
  const a = makeApp();
  a._thumbs.likes = { key: 'likes', ids: [1, 2, 3], posts: [], loaded: 0, token: 0, missing: [], tiles: [] };
  a._thumbsBatch = 3;
  a._updateThumbMore = () => {};
  a._appendThumbs = (el, posts) => { (a.got || []).concat(posts.map(p => p.id)); a.got = (a.got || []).concat(posts.map(p => p.id)); };
  a._appendMissingThumbs = (el, ids) => { a.missed = (a.missed || []).concat(ids); };
  a._appendUnresolvedThumbs = (el, ids) => { a.tried = (a.tried || []).concat(ids); };
  API.get = async () => ({ posts: [{ id: 1 }], unresolved: [2, 3] });

  await a.loadMoreThumbs('likes');
  check('найденные посты показаны', JSON.stringify(a.got) === '[1]', JSON.stringify(a.got));
  check('непроверенные уходят в повтор, а не в недоступные',
    JSON.stringify(a.tried) === '[2,3]' && JSON.stringify(a.missed || []) === '[]',
    JSON.stringify({ tried: a.tried, missed: a.missed }));
  check('s.missing остаётся чистым', JSON.stringify(a._thumbs.likes.missing || []) === '[]',
    JSON.stringify(a._thumbs.likes.missing));
  check('фильтр «недоступные» непроверенные не ловит',
    !a._thumbMatchesFilter({ id: 2, unresolved: true }, 'unavailable')
    && a._thumbMatchesFilter({ id: 2, missing: true }, 'unavailable'));

  // Источник ответил — заглушка сменяется настоящей плиткой.
  API.get = async () => ({ posts: [{ id: 2 }], unresolved: [] });
  a._appendUnresolvedThumbs = () => {};
  a.tiles = [{ _pfPost: { id: 2, unresolved: true }, dataset: { pfId: '2' }, removed: false,
    remove() { this.removed = true; } }];
  a._thumbs.likes.tiles = a.tiles;
  a.got = [];
  await a.retryUnresolvedThumbs('likes', 2);
  check('повтор подставляет настоящую плитку',
    JSON.stringify(a.got) === '[2]' && a.tiles[0].removed === true,
    JSON.stringify({ got: a.got, rm: a.tiles[0].removed, live: a._thumbs.likes.tiles.length }));
}

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);