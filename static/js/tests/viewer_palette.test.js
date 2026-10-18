// viewer_palette.test.js — цветовая палитра во вьювере:
//  * обычный клик по свотчу — применяет фильтр по цвету (setColorFilter), закрывая вьюер;
//  * Shift+клик и contextmenu (ПКМ) — копируют HEX в буфер обмена с тостом;
//  * повторный выбор цвета корректно заменяет существующий префикс color: в строке поиска.
import { App } from '../state.js';
import { rgbToHueName } from '../color.js';
await import('../color.js');

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
  setAttribute(k, v) { this[k] = v; }
  querySelector(sel) {
    if (sel === 'img') return this._img || null;
    return null;
  }
  fire(t, ev) { (this.listeners[t] || []).forEach(fn => fn(ev)); }
}

let copiedText = '';
const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', {});
defG('navigator', {
  clipboard: {
    writeText(txt) { copiedText = txt; return Promise.resolve(); },
  },
});
defG('document', {
  createElement: (tag) => new FakeEl(tag),
  getElementById: (id) => {
    if (id === 'color-popover') return { hidePopover() {} };
    return null;
  },
  addEventListener() {},
  removeEventListener() {},
});

// 1. Проверка классификации оттенка
check('rgbToHueName определяет красный', rgbToHueName(255, 0, 0) === 'red');
check('rgbToHueName определяет синий', rgbToHueName(0, 0, 255) === 'blue');
check('rgbToHueName определяет тёмный', rgbToHueName(10, 10, 10) === 'dark');

// 2. Тестирование взаимодействия с палитрой во вьювере
const container = new FakeEl('div');
const viewerContent = new FakeEl('div');
const fakeImg = {
  src: 'https://example.com/test.jpg',
  complete: true,
  naturalWidth: 100,
  naturalHeight: 100,
};
viewerContent._img = fakeImg;

App.els = App.els || {};
App.els.viewerPalette = container;
App.els.viewerContent = viewerContent;
App.els.searchInput = { value: '' };
App.state = App.state || {};
App.state.query = 'cat';
App.state.viewerOpen = true;

let closedViewer = false;
let closedOpts = null;
App.closeViewer = (opts) => {
  closedViewer = true;
  closedOpts = opts;
  App.state.viewerOpen = false;
};

let searchedQuery = '';
App.setSearchValue = (q) => { App.els.searchInput.value = q; };
App.search = (q) => { searchedQuery = q; return Promise.resolve(); };

const toasts = [];
App.showToast = (msg, type) => { toasts.push({ msg, type }); };

// Подменяем extractPaletteAsync на возвращающую тестовую палитру
App.extractPaletteAsync = () => Promise.resolve({
  colors: ['#ff0000', '#0000ff'],
  hues: ['red', 'blue'],
});

App.renderViewerPalette({ id: 123 });
// Ждём выполнение async show
await new Promise(r => setTimeout(r, 20));

check('палитра создала 2 свотча', container.children.length === 2, `count=${container.children.length}`);

const swatchRed = container.children[0];
const swatchBlue = container.children[1];

check('первый свотч красный', swatchRed.style.backgroundColor === '#ff0000');
check('второй свотч синий', swatchBlue.style.backgroundColor === '#0000ff');

// Обычный клик по красному свотчу — должен ПРИМЕНИТЬ цвет
const evClick = { stopPropagation() {}, shiftKey: false };
swatchRed.fire('click', evClick);

check('клик по свотчу закрыл вьюер с keepUrl: true', closedViewer && closedOpts?.keepUrl === true);
check('клик по свотчу применил цвет: cat color:red', searchedQuery === 'cat color:red', `query=${searchedQuery}`);
check('в буфер ничего не скопировано при обычном клике', copiedText === '');

// Клик по синему свотчу — должен заменить color:red на color:blue
App.state.query = 'cat color:red';
swatchBlue.fire('click', evClick);
check('повторный клик заменил цвет на color:blue', searchedQuery === 'cat color:blue', `query=${searchedQuery}`);

// Shift+клик — копирует HEX
const evShift = { stopPropagation() {}, shiftKey: true };
searchedQuery = '';
swatchBlue.fire('click', evShift);
check('Shift+клик скопировал #0000ff в буфер', copiedText === '#0000ff', `copied=${copiedText}`);
check('Shift+клик не вызывал поиск', searchedQuery === '');
check('Shift+клик показал тост скопировано', toasts.some(t => t.msg && t.msg.includes('#0000ff')));

// Контекстное меню (ПКМ) — копирует HEX
copiedText = '';
const evCtx = { stopPropagation() {}, preventDefault() {} };
swatchRed.fire('contextmenu', evCtx);
check('contextmenu скопировал #ff0000 в буфер', copiedText === '#ff0000', `copied=${copiedText}`);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) throw new Error(`${failed} checks failed`);
