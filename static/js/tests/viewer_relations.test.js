// viewer_relations.test.js — chip-ряд «родитель/дети» вьюера:
//  * обычный клик — прежний _openPostById (лента / поиск id:N);
//  * Ctrl/Cmd+ЛКМ и auxclick (средняя кнопка мыши) — пост в новой вкладке;
//  * auxclick с ЛКМ в новую вкладку не открывает.
import { App } from '../state.js';
import { API } from '../api.js';
await import('../viewer.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── стабы окружения ──────────────────────────────────────────────────────
class FakeEl {
  constructor(tag) {
    this.tagName = String(tag || 'div').toUpperCase();
    this.listeners = {};
    this.children = [];
    this.dataset = {};
    this.style = {};
    this.className = '';
    this.textContent = '';
    this.innerHTML = '';
    const s = new Set();
    this.classList = {
      add(c) { s.add(c); },
      remove(c) { s.delete(c); },
      contains(c) { return s.has(c); },
      toggle(c) { if (s.has(c)) s.delete(c); else s.add(c); },
    };
  }
  addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); }
  removeEventListener() {}
  appendChild(c) { this.children.push(c); return c; }
  fire(t, ev) { (this.listeners[t] || []).forEach(fn => fn(ev)); }
}

globalThis.window = {};
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = {
  createElement: (tag) => new FakeEl(tag),
  addEventListener() {},
  removeEventListener() {},
};

// Патчим реальный API.get: ответ /posts/:id/relations отдаём контролируемо.
let relRequests = 0;
const RELATIONS = {
  parent: { id: 7, file_type: 'jpg' },
  children: [{ id: 8 }, { id: 9, file_type: 'png' }],
};
API.get = (url) => {
  if (url.includes('/relations')) { relRequests++; return Promise.resolve(RELATIONS); }
  return Promise.reject(new Error('unexpected ' + url));
};

// ── стабы приложения ─────────────────────────────────────────────────────
const host = new FakeEl('div');
App.els = { relations: host };
App.state = { viewerOpen: true, viewerIndex: 0, posts: [{ id: 1 }], query: 'solo' };
App._relChipsFor = null;
App._relChipsToken = 0;
App._relChipsAbort = null;

const opened = [];
App.openInNewTab = (url) => opened.push(url);
const navigated = [];
App._openPostById = (id) => navigated.push(id);

const ev = (extra) => Object.assign({ preventDefault() {} }, extra);

// ── сценарий ─────────────────────────────────────────────────────────────
App._renderViewerRelations({ id: 1 });
await new Promise((resolve) => setTimeout(resolve, 0));

check('relations-запрос выполнен один раз', relRequests === 1, String(relRequests));
check('chips отрисованы: родитель + два ребёнка',
  host.children.length === 3, String(host.children.length));

const parentChip = host.children[0];
const childChip = host.children[1];
const child2Chip = host.children[2];

parentChip.fire('click', ev({}));
check('обычный клик по chip родителя: прежний переход (_openPostById)',
  navigated.length === 1 && navigated[0] === 7 && opened.length === 0,
  JSON.stringify({ navigated, opened }));

parentChip.fire('click', ev({ ctrlKey: true }));
check('Ctrl+ЛКМ по chip родителя: новая вкладка без перехода',
  opened.length === 1 && opened[0] === '/search/solo/post/7' && navigated.length === 1,
  JSON.stringify({ navigated, opened }));

childChip.fire('click', ev({ metaKey: true }));
check('Cmd+ЛКМ (macOS) по chip ребёнка: новая вкладка',
  opened.length === 2 && opened[1] === '/search/solo/post/8' && navigated.length === 1,
  JSON.stringify({ navigated, opened }));

child2Chip.fire('auxclick', ev({ button: 1 }));
check('средняя кнопка мыши (auxclick) по chip: новая вкладка',
  opened.length === 3 && opened[2] === '/search/solo/post/9',
  JSON.stringify(opened));

child2Chip.fire('auxclick', ev({ button: 0 }));
check('auxclick с ЛКМ игнорируется',
  opened.length === 3 && navigated.length === 1,
  JSON.stringify({ navigated, opened }));

childChip.fire('click', ev({}));
check('обычный клик по chip ребёнка: _openPostById с его id',
  navigated.length === 2 && navigated[1] === 8 && opened.length === 3,
  JSON.stringify({ navigated, opened }));

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);
