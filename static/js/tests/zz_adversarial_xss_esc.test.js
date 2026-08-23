'use strict';

const fs = require('fs');
const path = require('path');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeClassList(initial) {
  const s = new Set(initial || []);
  return {
    contains(c) { return s.has(c); },
    add(c) { s.add(c); },
    remove(c) { s.delete(c); },
    toggle(c, force) {
      const on = force != null ? !!force : !s.has(c);
      if (on) s.add(c); else s.delete(c);
      return on;
    },
  };
}

// document.createElement моделирует БРАУЗЕРНОЕ экранирование текстового узла:
// &, <, > — экранируются, кавычки " — НЕТ (в тексте элемента кавычки не
// экранируются ни одним браузером).
function makeEscDiv() {
  const el = { innerHTML: '' };
  const escText = (s) => String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
  Object.defineProperty(el, 'textContent', {
    set(v) { this._t = v; this.innerHTML = escText(v); },
    get() { return this._t; },
  });
  return el;
}

global.window = {};
global.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
global.document = {
  createElement() { return makeEscDiv(); },
  querySelectorAll() { return []; },
};

const utilsPath = path.join(__dirname, '..', 'utils.js');
(0, eval)(fs.readFileSync(utilsPath, 'utf8'));

console.log('esc() attribute-context (F7) tests\n');

check('esc экранирует &', esc('a&b') === 'a&amp;b', esc('a&b'));
check('esc экранирует < и >', esc('<b>') === '&lt;b&gt;', esc('<b>'));

// F7: esc() используется в АТРИБУТНЫХ контекстах:
//   state.js:886  data-query="${esc(p.query || '')}"
//   profile.js:567 data-query="${esc(pr.query || '')}"
// Кавычки в тексте элемента не экранируются → data-query="..." можно
// «выломать», вставив атрибут-обработчик.
const payload = 'x" onmouseover="alert(1)';
check('F7: esc экранирует двойные кавычки (атрибутный контекст)',
  esc(payload).indexOf('&quot;') >= 0, 'esc(' + JSON.stringify(payload) + ') = ' + JSON.stringify(esc(payload)));

console.log('\n' + passed + ' passed, ' + failed + ' failed');
process.exit(failed ? 1 : 0);