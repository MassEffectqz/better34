// zz_adversarial_xss_esc.test.js — F7: esc() экранирует кавычки (атрибутный контекст).
import { esc } from '../utils.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

console.log('esc() attribute-context (F7) tests\n');

// document.createElement моделирует БРАУЗЕРНОЕ экранирование текстового узла:
// &, <, > — экранируются, кавычки " — НЕТ.
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
globalThis.document = {
  createElement() { return makeEscDiv(); },
  querySelectorAll() { return []; },
};

check('esc экранирует &', esc('a&b') === 'a&amp;b', esc('a&b'));
check('esc экранирует < и >', esc('<b>') === '&lt;b&gt;', esc('<b>'));

// F7: esc() используется в АТРИБУТНЫХ контекстах:
//   state.js  data-query="${esc(p.query || '')}"
//   profile.js data-query="${esc(pr.query || '')}"
// Кавычки в тексте элемента не экранируются → data-query="..." можно
// «выломать», вставив атрибут-обработчик.
const payload = 'x" onmouseover="alert(1)';
check('F7: esc экранирует двойные кавычки (атрибутный контекст)',
  esc(payload).indexOf('&quot;') >= 0, 'esc(' + JSON.stringify(payload) + ') = ' + JSON.stringify(esc(payload)));

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);
