// viewer_source.test.js — кнопка «Открыть на источнике» во вьювере:
//  * sourcePostUrl маппит Post.Source (имя провайдера/хост) → URL поста;
//  * неизвестный или пустой источник — кнопка скрыта (без битых ссылок);
//  * обычный клик — переход, Ctrl/Cmd+ЛКМ и средняя кнопка — новая вкладка.
import { App } from '../state.js';

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
    querySelector() { return null; }, querySelectorAll() { return []; }, closest() { return null; },
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
defG('location', {
  href: 'https://localhost:3000/', origin: 'https://localhost:3000',
  pathname: '/', search: '', assigned: [],
  assign(u) { this.assigned.push(u); },
});
defG('history', { pushState() {}, replaceState() {}, back() {} });

await import('../viewer.js');

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [], viewerIndex: 0,
    profile: { liked_posts: [], hidden_posts: [] },
  };
  a.els = { viewerSource: makeEl('button') };
  a.opened = [];
  a.openInNewTab = function (url) { a.opened.push(url); };
  return a;
}

console.log('Кнопка «Открыть на источнике» во вьювере\n');

// ── 1. sourcePostUrl ─────────────────────────────────────────────────────
{
  const a = makeApp();
  const P = (id, source) => ({ id, source });
  check('sourcePostUrl: rule34',
    a.sourcePostUrl(P(5, 'rule34')) === 'https://rule34.xxx/index.php?page=post&s=view&id=5',
    String(a.sourcePostUrl(P(5, 'rule34'))));
  check('sourcePostUrl: gelbooru',
    a.sourcePostUrl(P(7, 'gelbooru')) === 'https://gelbooru.com/index.php?page=post&s=view&id=7',
    String(a.sourcePostUrl(P(7, 'gelbooru'))));
  check('sourcePostUrl: safebooru',
    a.sourcePostUrl(P(3, 'safebooru')) === 'https://safebooru.org/index.php?page=post&s=view&id=3',
    String(a.sourcePostUrl(P(3, 'safebooru'))));
  check('sourcePostUrl: хост img4.gelbooru.com → базовый домен',
    a.sourcePostUrl(P(9, 'img4.gelbooru.com')) === 'https://gelbooru.com/index.php?page=post&s=view&id=9',
    String(a.sourcePostUrl(P(9, 'img4.gelbooru.com'))));
  check('sourcePostUrl: wimg.rule34.xxx → rule34.xxx',
    a.sourcePostUrl(P(1, 'wimg.rule34.xxx')) === 'https://rule34.xxx/index.php?page=post&s=view&id=1',
    String(a.sourcePostUrl(P(1, 'wimg.rule34.xxx'))));
  check('sourcePostUrl: CDN-префикс cdn.example.com срезается',
    a.sourcePostUrl(P(2, 'cdn.example.com')) === 'https://example.com/index.php?page=post&s=view&id=2',
    String(a.sourcePostUrl(P(2, 'cdn.example.com'))));
  check('sourcePostUrl: пустой источник — null',
    a.sourcePostUrl(P(1, '')) === null && a.sourcePostUrl({ id: 1 }) === null && a.sourcePostUrl(null) === null);
  check('sourcePostUrl: неизвестный формат (e621) — null',
    a.sourcePostUrl(P(6, 'e621')) === null, String(a.sourcePostUrl(P(6, 'e621'))));
}

// ── 2. _syncSourceButton: видимость и data-url ───────────────────────────
{
  const a = makeApp();
  a.els.viewerSource.classList.add('hidden');
  const url = a._syncSourceButton({ id: 5, source: 'rule34' });
  check('_syncSourceButton: источник есть — кнопка видима, data-url заполнен',
    url != null && !a.els.viewerSource.classList.contains('hidden')
    && a.els.viewerSource.dataset.url === url,
    JSON.stringify({ url, ds: a.els.viewerSource.dataset }));

  a._syncSourceButton({ id: 6, source: '' });
  check('_syncSourceButton: источника нет — кнопка скрыта, data-url удалён',
    a.els.viewerSource.classList.contains('hidden') && a.els.viewerSource.dataset.url === undefined,
    JSON.stringify(a.els.viewerSource.dataset));
}

// ── 3. Клики по кнопке ───────────────────────────────────────────────────
{
  const a = makeApp();
  a.els.viewerSource.dataset.url = 'https://rule34.xxx/index.php?page=post&s=view&id=5';

  a.onSourceClick({ ctrlKey: false, preventDefault() {} });
  check('обычный клик — переход на источник в этой вкладке',
    location.assigned.length === 1 && location.assigned[0].includes('rule34.xxx') && a.opened.length === 0,
    JSON.stringify({ assigned: location.assigned, opened: a.opened }));

  a.onSourceClick({ ctrlKey: true, preventDefault() {} });
  check('Ctrl+ЛКМ — новая вкладка',
    a.opened.length === 1 && a.opened[0].includes('rule34.xxx') && location.assigned.length === 1,
    JSON.stringify({ assigned: location.assigned, opened: a.opened }));

  a.onSourceClick({ metaKey: true, preventDefault() {} });
  check('Cmd+ЛКМ — тоже новая вкладка (macOS)', a.opened.length === 2, JSON.stringify(a.opened));

  a.onSourceAuxClick({ button: 1, preventDefault() {} });
  check('средняя кнопка — новая вкладка', a.opened.length === 3, JSON.stringify(a.opened));

  a.onSourceAuxClick({ button: 0, preventDefault() {} });
  check('ЛКМ через auxclick игнорируется', a.opened.length === 3);

  delete a.els.viewerSource.dataset.url;
  a.onSourceClick({ ctrlKey: false, preventDefault() {} });
  a.onSourceAuxClick({ button: 1, preventDefault() {} });
  check('без data-url — клики ничего не делают',
    location.assigned.length === 1 && a.opened.length === 3,
    JSON.stringify({ assigned: location.assigned, opened: a.opened }));
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);

