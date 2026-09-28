// friend_page.test.js — страница профиля друга /friend/<id>[/<tab>]:
// это ОТДЕЛЬНАЯ СТРАНИЦА со своим адресом, а не панель во вкладке «Друзья».
//
// Что здесь ловится:
//  * маршрут не разбирается parseLocation -> прямая ссылка открывает ленту;
//  * «назад» в браузере возвращает не на предыдущую вкладку, а на ленту
//    (или вообще не возвращает страницу);
//  * повторное открытие того же адреса плодит записи истории и ломает «назад».
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

console.log('Страница профиля друга (/friend/<id>[/<tab>])\n');
