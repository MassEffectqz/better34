// a11y.test.js — задача 4: focus trap, aria-live, aria-hidden.
import { App } from '../state.js';
await import('../a11y.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── стабы окружения ──
const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
function makeEl() {
  const handlers = {};
  return {
    style: {}, dataset: {}, classList: {
      _s: new Set(),
      add(...c) { c.forEach(x => this._s.add(x)); },
      remove(...c) { c.forEach(x => this._s.delete(x)); },
      contains(c) { return this._s.has(c); },
    },
    children: [],
    appendChild(c) { this.children.push(c); return c; },
    removeChild(c) { const i = this.children.indexOf(c); if (i >= 0) this.children.splice(i, 1); },
    setAttribute(k, v) { this._attrs = this._attrs || {}; this._attrs[k] = v; },
    removeAttribute(k) { if (this._attrs) delete this._attrs[k]; },
    getAttribute(k) { return this._attrs ? this._attrs[k] : null; },
    addEventListener(type, f) { (handlers[type] = handlers[type] || []).push(f); },
    removeEventListener(type, f) {
      if (!handlers[type]) return;
      const i = handlers[type].indexOf(f);
      if (i >= 0) handlers[type].splice(i, 1);
    },
    dispatch(type, ev) { (handlers[type] || []).forEach(f => f(ev || {})); },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    focus() { globalThis.__focused = this; },
    getAttributeNS: () => null,
    _handlers: handlers,
  };
}
defG('document', {
  _active: null,
  get activeElement() { return this._active; },
  set activeElement(v) { this._active = v; },
  getElementById: () => makeEl(),
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => makeEl(),
  addEventListener() {}, removeEventListener() {},
  hidden: true, documentElement: { lang: 'ru' },
});
defG('window', { addEventListener() {}, dispatchEvent() {} });
defG('localStorage', {
  _s: {}, getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); }, removeItem(k) { delete this._s[k]; },
});

console.log('a11y: focus trap, aria-live, aria-hidden\n');

// ── 1. trapFocus ──
const root = makeEl();
const btn1 = makeEl(); const btn2 = makeEl(); const btn3 = makeEl();
// filter() в trapFocus проверяет offsetParent (null → все отфильтрованные кроме activeElement).
btn1.offsetParent = {}; btn2.offsetParent = {}; btn3.offsetParent = {};
const inputs = [btn1, btn2, btn3];
root.querySelectorAll = (sel) => sel.includes('button') ? inputs : [];

const release = App.trapFocus(root, btn1);
check('trapFocus возвращает release-функцию', typeof release === 'function');

// симуляция Tab на последнем элементе
document.activeElement = btn3;
root.dispatch('keydown', { key: 'Tab', shiftKey: false, preventDefault() {} });
check('Tab на последнем → focus на первый', globalThis.__focused === btn1, 'focused=' + (globalThis.__focused && 'el'));

document.activeElement = btn1;
root.dispatch('keydown', { key: 'Tab', shiftKey: true, preventDefault() {} });
check('Shift+Tab на первом → focus на последний', globalThis.__focused === btn3);

release();

// ── 2. announce ──
const toast = makeEl();
App.els = { toast };
App.announce('Загрузки завершены');
const liveEl = toast.children.find(c => c._attrs && c._attrs['aria-live'] === 'polite');
check('announce создаёт .a11y-live с aria-live=polite', !!liveEl, JSON.stringify(toast.children.map(c => c._attrs)));
check('announce пишет текст в live-элемент', liveEl && liveEl.textContent === 'Загрузки завершены', liveEl && liveEl.textContent);

// ── 3. setAriaHidden ──
const main = makeEl();
App.setAriaHidden(main, true);
check('setAriaHidden(true) ставит aria-hidden=true', main.getAttribute('aria-hidden') === 'true');
check('setAriaHidden(true) добавляет .a11y-hidden', main.classList.contains('a11y-hidden'));
App.setAriaHidden(main, false);
check('setAriaHidden(false) ставит aria-hidden=false', main.getAttribute('aria-hidden') === 'false');
check('setAriaHidden(false) убирает .a11y-hidden', !main.classList.contains('a11y-hidden'));

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);