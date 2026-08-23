// zz_adversarial_suggest_stale.test.js — F4: устаревший ответ /suggest не затирает свежий.
import { App } from '../state.js';
import { API } from '../api.js';
await import('../search.js');

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

function makeEl() {
  return {
    style: {}, classList: makeClassList(), children: [],
    innerHTML: '', textContent: '',
    appendChild(c) { this.children.push(c); },
    addEventListener() {},
    querySelector() { return null; },
  };
}

globalThis.window = {};
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = {
  querySelector() { return null; },
  createElement: () => makeEl(),
};

const flush = () => new Promise(res => setTimeout(res, 10));

console.log('suggestProfileTag stale race (F4) tests\n');

(async () => {
  const a = Object.create(App);
  const favSuggestions = {
    classList: makeClassList(),
    children: [],
    appendChild(c) { this.children.push(c); },
    set innerHTML(v) { if (v === '') this.children.length = 0; },
    get innerHTML() { return this.children.map(c => c.textContent).join(''); },
  };
  a.els = {
    favTagInput: { value: '' },
    hiddenTagInput: { value: '' },
    favSuggestions,
    profileSuggestions: { classList: makeClassList(), innerHTML: '' },
  };

  // контролируемые ответы /suggest по порядку вызовов (патчим реальный API)
  const calls = [];
  API.get = async function (url) {
    const rec = { url, resolvers: [] };
    rec.promise = new Promise(res => rec.resolvers.push(res));
    calls.push(rec);
    return rec.promise;
  };
  API.post = async function () { return {}; };
  API.invalidate = function () {};

  // пользователь печатает «cd» ПОСЛЕ «ab»; ответ по «ab» приходит ПОЗЖЕ
  a.els.favTagInput.value = 'ab';
  a.suggestProfileTag('fav');
  a.els.favTagInput.value = 'cd';
  a.suggestProfileTag('fav');
  await flush();

  // сначала приходит свежий ответ (cd)
  calls[1].resolvers[0]({ tags: [{ value: 'cd-x', label: 'cd-x' }] });
  await flush();
  check('F4: свежий ответ (cd) отрисован', favSuggestions.children.some(c => c.textContent === 'cd-x'),
    'children=' + favSuggestions.children.map(c => c.textContent).join(','));

  // потом — УСТАРЕВШИЙ ответ (ab) без какой-либо защиты seq/token
  calls[0].resolvers[0]({ tags: [{ value: 'ab-x', label: 'ab-x' }] });
  await flush();
  const latest = favSuggestions.children.some(c => c.textContent === 'cd-x');
  check('F4: устаревший ответ не должен затирать актуальные подсказки',
    latest, 'старый ответ перезаписал подсказки: ' + favSuggestions.children.map(c => c.textContent).join(','));

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();