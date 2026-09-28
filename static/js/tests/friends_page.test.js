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
  return {
    tagName: String(tag || 'div').toUpperCase(), children: [], dataset: {}, style: {},
    listeners: {}, className: '', textContent: '', innerHTML: '', value: '',
    classList: makeClassList(),
    appendChild(c) { this.children.push(c); return c; },
    append(...n) { n.forEach(x => this.children.push(x)); },
    addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); },
    removeEventListener() {}, setAttribute() {}, removeAttribute() {}, focus() {},
    remove() {}, click() {}, querySelector() { return null; }, querySelectorAll() { return []; },
    closest() { return null; },
    getBoundingClientRect() { return { top: 0, left: 0, width: 100, height: 100 }; },
  };
}

const bodyCls = makeClassList();
const defG = (n, v) => Object.defineProperty(globalThis, n, { value: v, configurable: true, writable: true });
defG('document', {
  getElementById: () => null, querySelector: () => null, querySelectorAll: () => [],
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

console.log('Страница профиля друга (/friend/<id>[/<tab>])\n');
