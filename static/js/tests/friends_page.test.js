// friend_page.test.js — страница профиля друга /friend/<id>[/<tab>]:
// это ОТДЕЛЬНАЯ СТРАНИЦА со своим адресом, а не панель во вкладке «Друзья».
// Раскладка: слева панель с аватаром, ником и вкладками, справа контент.
//
// Что здесь ловится:
//  * маршрут не разбирается parseLocation -> прямая ссылка открывает ленту;
//  * «назад» в браузере возвращает не на предыдущую вкладку, а на ленту
//    (или вообще не возвращает страницу);
//  * повторное открытие того же адреса плодит записи истории и ломает «назад»;
//  * в разметке осталась старая полоса счётчиков или потерялась боковая панель.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { App } from '../state.js';
import '../friends.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeClassList() {
  const s = new Set();
  return {
    contains: (c) => s.has(c), add: (c) => s.add(c), remove: (c) => s.delete(c),
    toggle(c, f) { const on = f != null ? !!f : !s.has(c); if (on) s.add(c); else s.delete(c); return on; },
  };
}

function makeEl(tag) {
  const el = {
    tagName: String(tag || 'div').toUpperCase(), children: [], dataset: {}, style: {},
    listeners: {}, className: '', textContent: '', value: '',
    classList: makeClassList(),
    _html: '',
    appendChild(c) { this.children.push(c); return c; },
    append(...n) { n.forEach(x => this.children.push(x)); },
    addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); },
    removeEventListener() {}, setAttribute() {}, removeAttribute() {}, focus() {},
    remove() {}, click() {}, querySelectorAll() { return []; },
    closest() { return null; },
    getBoundingClientRect() { return { top: 0, left: 0, width: 100, height: 100 }; },
    // Стаб разметки: friends.js собирает плитку через innerHTML, а потом берёт
    // tile.querySelector('img'). Здесь мы по наличию тега создаём такой узел,
    // чтобы тест мог имитировать загрузку картинки.
    set innerHTML(v) {
      this._html = String(v);
      this._img = String(v).includes('<img') ? makeEl('img') : null;
      this._err = String(v).includes('pf-err') ? makeEl('span') : null;
    },
    get innerHTML() { return this._html; },
    querySelector(sel) {
      if (sel === 'img') return this._img || null;
      if (sel === '.pf-err') return this._err || null;
      if (sel === '.pf-fallback') return null;
      return null;
    },
    // Имитация загрузки/ошибки картинки: события вешаются прямо в _friendTile.
    fire(type, ev) {
      (this.listeners[type] || []).forEach((fn) => fn(ev || { target: this }));
    },
  };
  return el;
}

const bodyCls = makeClassList();
const defG = (n, v) => Object.defineProperty(globalThis, n, { value: v, configurable: true, writable: true });
// Элементы кэшируем: friends.js ищет #friend-profile и вешает на него
// innerHTML, а затем берёт кнопки по id. С null-стабом openFriendProfile
// выходил бы на первой же строке и роутинг не проверялся бы вовсе.
const els = new Map();
const getEl = (id) => {
  if (!els.has(id)) els.set(id, makeEl('div'));
  return els.get(id);
};
defG('document', {
  getElementById: (id) => getEl(id), querySelector: () => null, querySelectorAll: () => [],
  createElement: (tag) => makeEl(tag), createTextNode: () => ({ nodeType: 3 }),
  createDocumentFragment: () => ({ children: [], appendChild(c) { this.children.push(c); } }),
  addEventListener() {}, removeEventListener() {}, body: { classList: bodyCls, appendChild() {} },
});
defG('window', { addEventListener() {}, innerWidth: 1400, innerHeight: 800 });
defG('localStorage', { _s: {}, getItem(k) { return this._s[k] != null ? this._s[k] : null; }, setItem() {}, removeItem() {} });
defG('navigator', { maxTouchPoints: 0 });
defG('location', { href: 'https://x/', origin: 'https://x', pathname: '/', search: '' });

const hist = {
  calls: [], state: null,
  pushState(s, _t, url) { this.calls.push(['push', url, s]); this.state = s; },
  replaceState(s, _t, url) { this.calls.push(['replace', url, s]); this.state = s; },
  back() { this.calls.push(['back']); },
};
defG('history', hist);
defG('requestAnimationFrame', (f) => setTimeout(f, 0));

function makeApp() {
  const a = Object.create(App);
  a.state = { posts: [], query: '', profile: { liked_posts: [], hidden_posts: [] } };
  a._lastURL = '/';
  a.rendered = [];
  // Рендер профиля заменяем: он ходит в сеть, здесь проверяем только роутинг.
  a.renderFriendProfile = function () { a.rendered.push(this._friendProfile.tab); return Promise.resolve(); };
  a.renderFriendProfileTabs = function () { a.rendered.push('tabs:' + this._friendProfile.tab); return Promise.resolve(); };
  return a;
}

const reset = () => { hist.calls = []; hist.state = null; bodyCls._s = new Set(); };

// ── 0. Раскладка: панель слева, контент справа ──────────────────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const root = join(here, '..', '..');
  const html = readFileSync(join(root, 'index.html'), 'utf8');
  const friends = readFileSync(join(root, 'js', 'friends.js'), 'utf8');
  const css = readFileSync(join(root, 'css', '04-panels.css'), 'utf8');

  check('контейнер страницы друга вне вкладки «Друзья»',
    /<div id="friend-profile"[^>]*><\/div>/.test(html) && !/id="tab-friends"[\s\S]{0,4000}id="friend-profile"/.test(html));
  check('двухколоночная раскладка объявлена', /\.friend-layout\{display:grid;grid-template-columns:/.test(css));
  check('боковая панель friend-side есть и в разметке, и в стилях',
    friends.includes('class="friend-side"') && /\.friend-side\{/.test(css));
  check('у вкладок боковая навигация friend-nav-item',
    friends.includes('friend-nav-item') && /\.friend-nav-item\{/.test(css));
  // Старая полоса счётчиков дублировала числа вкладок — её быть не должно.
  check('старая полоса счётчиков удалена',
    !friends.includes('friend-profile-stats') && !/\.friend-stat\{/.test(css));
  // Аватар в шапке: адрес друга может прийти javascript:, фильтр обязателен.
  check('аватар друга фильтруется по безопасным URL',
    /f\.avatar && \/\^\(https\?:\\\/\\\/\|\\\/\|data:image\\\/\)\//.test(friends));
}

// ── 1. Разбор маршрута ─────────────────────────────────────────────────────
{
  const a = makeApp();
  const d = a.parseLocation('/friend/abc123');
  check('parseLocation: /friend/<id> — страница друга, вкладка по умолчанию',
    d.matched && d.friendId === 'abc123' && d.friendTab === 'likes', JSON.stringify(d));

  const t = a.parseLocation('/friend/abc123/dislikedtags');
  check('parseLocation: вкладка друга в адресе',
    t.matched && t.friendId === 'abc123' && t.friendTab === 'dislikedtags', JSON.stringify(t));

  const prof = a.parseLocation('/profile/friends');
  check('parseLocation: вкладка профиля не сломана',
    prof.matched && prof.profileTab === 'friends' && !prof.friendId, JSON.stringify(prof));

  const grid = a.parseLocation('/grid/likes');
  check('parseLocation: режим сетки не сломана',
    grid.matched && grid.gridMode === 'likes' && !grid.friendId, JSON.stringify(grid));

  // /friends — наш API-словарь: подмена открыла бы профиль вместо витрины.
  const bad = a.parseLocation('/friends');
  check('parseLocation: /friends (API) не матчится как страница друга', !bad.matched);
}

// ── 2. Открытие страницы пишет адрес в историю и прячет ленту ───────────────
{
  const a = makeApp();
  reset();
  await a.openFriendProfile('abc123');
  check('открытие друга: адрес в истории и лента скрыта',
    hist.calls.length === 1 && hist.calls[0][1] === '/friend/abc123' && bodyCls.contains('friend-profile-open'),
    JSON.stringify(hist.calls));
}

// ── 3. Повторное открытие того же адреса не плодит историю ─────────────────
{
  const a = makeApp();
  await a.openFriendProfile('abc123');
  reset();
  await a.openFriendProfile('abc123');
  check('повторное открытие: лишней записи в истории нет', hist.calls.length === 0, JSON.stringify(hist.calls));
}

// ── 4. Смена вкладки меняет адрес (поэтому «назад» листает вкладки) ─────────
{
  const a = makeApp();
  await a.openFriendProfile('abc123');
  reset();
  await a._friendProfileTab('collections');
  check('смена вкладки: адрес в истории',
    hist.calls.length === 1 && hist.calls[0][1] === '/friend/abc123/collections', JSON.stringify(hist.calls));
  check('смена вкладки: длина страницы сброшена', a._friendProfile.shown <= 60);

  reset();
  await a._friendProfileTab('likes');
  check('вкладка «лайки» — без суффикса в адресе',
    hist.calls.length === 1 && hist.calls[0][1] === '/friend/abc123', JSON.stringify(hist.calls));
}

// ── 5. «Назад» из вкладки: popstate открывает страницу БЕЗ новой записи ────
{
  const a = makeApp();
  reset();
  await a.openFriendProfile('abc123', 'dislikedtags', { push: false });
  check('popstate: страница открыта без записи в истории',
    hist.calls.length === 0 && a._friendProfile.tab === 'dislikedtags', JSON.stringify(hist.calls));
}

// ── 6. Неизвестная вкладка в адресе падает на «лайки», а не на пустоту ──────
{
  const a = makeApp();
  reset();
  await a.openFriendProfile('abc123', 'нет-такой');
  check('неизвестная вкладка: открывается «likes»',
    a._friendProfile.tab === 'likes' && hist.calls[0][1] === '/friend/abc123', JSON.stringify(hist.calls));
}

// ── 7. Закрытие возвращает на ленту и убирает класс с body ─────────────────
{
  const a = makeApp();
  a.state.query = 'solo';
  await a.openFriendProfile('abc123');
  reset();
  a.closeFriendProfile();
  const urls = hist.calls.map((c) => c[1]);
  check('закрытие: адрес ленты, а не /friend/...',
    urls.length === 1 && !urls[0].startsWith('/friend/') && a._friendProfile === null, JSON.stringify(hist.calls));
  check('закрытие: класс на body снят, лента вернулась', !bodyCls.contains('friend-profile-open'));
}

// ── 8. Картинки в плитках видны ─────────────────────────────────────────────
// Регрессия: в CSS .profile-thumbs img{opacity:0}, и картинка проявляется
// ТОЛЬКО с классом loaded. Без него файлы грузились, но оставались прозрачными —
// сетка друга выглядела пустой. Тест имитирует загрузку и требует класса.
{
  const a = makeApp();
  const tile = a._friendTile({ id: 42, tags: 'a b', preview_url: 'https://cdn/p.jpg' });
  const img = tile.querySelector('img');
  check('плитка содержит img', !!img);
  if (img) {
    check('до загрузки loaded не стоит (иначе мигнёт мусор)', !img.classList.contains('loaded'));
    img.fire('load', { target: img });
    check('после загрузки картинка видима (класс loaded)', img.classList.contains('loaded'));
    check('плитка не помечена битой', !tile.classList.contains('pf-broken'));
    check('src ведёт через прокси превью',
      typeof img.src === 'string' && img.src.includes('kind=preview'), String(img.src));
  }
}

// ── 9. Битые превью: перебор кандидатов и внятная заглушка ─────────────────
{
  const a = makeApp();
  // Первое превью умерло, но есть локальная миниатюра — плитка обязана дойти до неё.
  const tile = a._friendTile({ id: 7, preview_url: 'https://cdn/dead.jpg', downloaded: true, thumb_path: 'x.jpg' });
  const img = tile.querySelector('img');
  img.fire('error', { target: img });
  check('после ошибки пробуем следующий кандидат',
    typeof img.src === 'string' && img.src.includes('/api/thumb/7'), String(img.src));
  img.fire('load', { target: img });
  check('успешный второй кандидат делает плитку видимой', img.classList.contains('loaded'));

  // Совсем без кандидатов: не пустая дыра, а подпись «недоступно».
  const none = a._friendTile({ id: 9 });
  check('пост без превью помечается pf-broken', none.classList.contains('pf-broken'));
}

// ── 9. Регрессия: страница друга показывается, а не чёрный экран ───────────
// hidden с оверлея снимался только в showFriendCode — и то для #friends-code-box.
// Для самого #friend-profile его не снимал никто, хотя body.friend-profile-open
// прячет #header и main, а .friend-profile.hidden{display:none} — сам оверлей.
// Итог: открытие /friend/<id> давало гарантированный чёрный экран без ошибок.
{
  const a = Object.create(App);
  a._friendProfile = {
    id: 'abc123', tab: 'likes', shown: 60,
    data: { friend: { id: 'abc123', nickname: 'Друг', url: 'https://x/', likes: [], disliked: [], fav_tags: [], disliked_tags: [], collections: [] } },
  };
  a._friendSideWhen = function () {};
  a.renderFriendProfileTabs = async function () {};
  const box = getEl('friend-profile');
  box.classList.add('hidden');
  await App.renderFriendProfile.call(a);
  check('страница друга: hidden снят с оверлея',
    !box.classList.contains('hidden'), box.innerHTML.slice(0, 60));
  check('страница друга: боковая панель отрисована',
    box.innerHTML.includes('friend-side-tabs') && box.innerHTML.includes('btn-friend-back'));
}
// ── 10. Размер сетки на странице друга ──────────────────────────────────────
// Выбранное число колонок живёт в localStorage (не трогает ленту), применяется
// через --pf-cols на оверлее и переживает переключение вкладок и переоткрытие.
{
  const here = dirname(fileURLToPath(import.meta.url));
  const root = join(here, '..', '..');
  const friends = readFileSync(join(root, 'js', 'friends.js'), 'utf8');
  check('контрол размера сетки есть в разметке страницы друга',
    friends.includes('id="friend-grid-size"') && friends.includes('id="friend-grid-bar"'));
  check('варианты 2..6 генерируются в select', friends.includes('[2, 3, 4, 5, 6]') && friends.includes('<option value="${n}">'));

  // Харнесс выше даёт setItem-пустышку: для проверки сохранения подставляем рабочий.
  const store = {};
  defG('localStorage', {
    getItem: (k) => (store[k] != null ? store[k] : null),
    setItem: (k, v) => { store[k] = String(v); },
    removeItem: (k) => { delete store[k]; },
  });

  const a = Object.create(App);
  a.state = { gridCols: null };
  check('по умолчанию прежние 3 колонки', a._friendGridCols() === 3, String(a._friendGridCols()));
  a.state.gridCols = 5;
  check('без своей настройки берётся глобальная плотность', a._friendGridCols() === 5, String(a._friendGridCols()));
  store['briefly_friend_grid_cols'] = '2';
  check('своя настройка сильнее глобальной', a._friendGridCols() === 2, String(a._friendGridCols()));
  store['briefly_friend_grid_cols'] = '99';
  check('мусор в хранилище не ломает выбор', a._friendGridCols() === 5, String(a._friendGridCols()));
  delete store['briefly_friend_grid_cols'];

  const box = getEl('friend-profile');
  const applied = [];
  box.style = { setProperty: (k, v) => applied.push([k, v]) };
  a._wireFriendGrid(box);
  check('колонки применяются к оверлею через --pf-cols',
    applied.some(([k, v]) => k === '--pf-cols' && v === '5'), JSON.stringify(applied));

  const sel = getEl('friend-grid-size');
  check('контрол показывает текущее значение', sel.value === '5', sel.value);
  sel.value = '4';
  sel.fire('change');
  check('смена колонок применяется сразу',
    applied.some(([k, v]) => k === '--pf-cols' && v === '4'), JSON.stringify(applied));
  check('смена колонок сохраняется', store['briefly_friend_grid_cols'] === '4',
    String(store['briefly_friend_grid_cols']));

  // Полоса сетки прячется на вкладках без плиток (теги, альбомы).
  const a2 = Object.create(App);
  a2.state = { gridCols: null };
  a2._friendProfile = { id: 'x', tab: 'collections', shown: 60, data: { friend: {} } };
  a2.renderFriendProfileTabs = App.renderFriendProfileTabs;
  const bar = getEl('friend-grid-bar');
  bar.classList.remove('hidden');
  await a2.renderFriendProfileTabs();
  check('на вкладке альбомов полоса сетки скрыта', bar.classList.contains('hidden'));
  a2._friendProfile.tab = 'likes';
  await a2.renderFriendProfileTabs();
  check('на вкладке лайков полоса сетки видна', !bar.classList.contains('hidden'));
}
console.log(`\n${passed} passed, ${failed} failed`);
if (failed) process.exit(1);