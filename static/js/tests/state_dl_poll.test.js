// state_dl_poll.test.js — задача 3: проводка уведомления в startDlPoll.
// state.js import-safe (DOM — только внутри методов) — импортируем как ESM
// (как zz_adversarial-тесты), стабим окружение и вызываем startDlPoll напрямую.
import { App } from '../state.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

console.log('startDlPoll: было active → стало 0 → notifyDownloadsDone\n');

// ── стабы DOM/библиотек ──
const els = {};
function el(id) {
  if (!els[id]) els[id] = {
    id, style: {}, dataset: {}, classList: {
      _s: new Set(),
      add(...c) { c.forEach(x => this._s.add(x)); },
      remove(...c) { c.forEach(x => this._s.delete(x)); },
      toggle(c, f) { if (f === undefined) { this._s.has(c) ? this._s.delete(c) : this._s.add(c); } else if (f) this._s.add(c); else this._s.delete(c); },
      contains(c) { return this._s.has(c); },
    },
    addEventListener() {}, removeEventListener() {},
    appendChild() {}, remove() {}, focus() {}, click() {}, value: '', textContent: '',
  };
  return els[id];
}
// Node 24: navigator/localStorage — getter-only глобалы, нужен defineProperty.
const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('document', {
  getElementById: el, querySelector: () => null, querySelectorAll: () => [],
  createElement: () => el('div' + Math.random()),
  addEventListener() {}, removeEventListener() {}, hidden: true,
  documentElement: { lang: 'ru' },
});
defG('window', { addEventListener() {}, dispatchEvent() {} });
defG('localStorage', {
  _s: {}, getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); }, removeItem(k) { delete this._s[k]; },
});
defG('navigator', { onLine: true });
defG('indexedDB', { open: () => ({ onsuccess: null, onerror: null, onupgradeneeded: null }) });
defG('AbortController', class { constructor() { this.signal = { aborted: false }; } abort() {} });
defG('EventSource', class {
  constructor(url) { this.url = url; }
  addEventListener(type, f) { if (type === 'event' && !globalThis.__onEvent) globalThis.__onEvent = f; }
});
defG('CustomEvent', class { constructor(type, o) { this.type = type; this.detail = o && o.detail; } });
defG('location', { href: 'http://x/', origin: 'http://x', pathname: '/', search: '' });
defG('history', { pushState() {}, replaceState() {}, state: null });
defG('fetch', async () => ({ ok: true, status: 200, headers: { get: () => 'application/json' }, json: async () => ({}), text: async () => '' }));
defG('requestAnimationFrame', (f) => setTimeout(f, 0));

// методы-зависимости startDlPoll
let notifyCalls = [];
App.notifyDownloadsDone = (done, failedN) => { notifyCalls.push([done, failedN]); return true; };
App.getCardByIndex = () => null;
App.showToast = () => {};

// init() не вызывался — заполняем els заглушками, которые трогает startDlPoll.
const mkEl = () => ({ style: {}, textContent: '', classList: { add() {}, remove() {}, toggle() {}, contains: () => false } });
App.els = { dlProgress: mkEl(), dlProgressBar: mkEl(), dlProgressText: mkEl(), viewerProgress: mkEl() };

// Поднимаем SSE-поллер: EventSource-стаб запоминает обработчик 'event'.
App.startDlPoll();

const onEvent = globalThis.__onEvent;
check('SSE-обработчик подписан', typeof onEvent === 'function');
const ev = (obj) => onEvent({ data: JSON.stringify(obj) });

// Партии нет — уведомления не будет.
ev({ type: 'status', queued: 0, active: 0, done: 0 });
check('нет партии → нет уведомления', notifyCalls.length === 0);

// Партия пошла.
ev({ type: 'status', queued: 3, active: 0, done: 0 });
ev({ type: 'status', queued: 0, active: 2, done: 1 });
check('партия идёт → уведомления нет', notifyCalls.length === 0);

// Партия завершилась: done=3, одна ошибка по result-событию.
ev({ type: 'result', post_id: 5, success: false, error: 'x' });
ev({ type: 'status', queued: 0, active: 0, done: 3 });
check('active→0 → ровно одно уведомление', notifyCalls.length === 1, JSON.stringify(notifyCalls));
check('в уведомление переданы done и счётчик ошибок',
  notifyCalls[0][0] === 3 && notifyCalls[0][1] === 1, JSON.stringify(notifyCalls[0]));

// Повторный статус-пакет без партии — повторного уведомления нет.
ev({ type: 'status', queued: 0, active: 0, done: 3 });
check('повтор «пустого» статуса не дублирует уведомление', notifyCalls.length === 1);

// Новая партия → снова завершение → снова уведомление.
ev({ type: 'status', queued: 1, active: 0, done: 3 });
ev({ type: 'status', queued: 0, active: 0, done: 4 });
check('новая партия → новое уведомление', notifyCalls.length === 2 && notifyCalls[1][0] === 4,
  JSON.stringify(notifyCalls));

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);