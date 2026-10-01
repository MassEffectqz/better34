// providers_header.test.js — бейдж «Источник постов» в шапке у НЕ-админа.
//
// Регрессия: список источников жил только в /api/settings под requireAdmin, а
// loadSettings() у вошедшего не-админа выходил раньше запроса — в шапке
// оставался голый «rule34» из HTML, а меню не рисовалось вовсе. Теперь список
// едет отдельным GET /api/providers (он публичный для залогиненных), а
// смена источника — POST /api/providers, и на /api/settings не-админ не ходит.
import { App } from '../state.js';
import { API } from '../api.js';
import '../settings.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── DOM-стаб ────────────────────────────────────────────────────────────────
const els = {};
function el(id) {
  if (els[id]) return els[id];
  const node = {
    id, value: '', innerHTML: '', _text: '',
    style: {}, dataset: {},
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    addEventListener() {}, appendChild() {},
  };
  // utils.js esc() собирает строку через div.textContent → innerHTML, поэтому
  // стаб обязан экранировать так же — иначе имена источников в шапке пустые.
  Object.defineProperty(node, 'textContent', {
    get() { return this._text; },
    set(v) {
      this._text = String(v);
      this.innerHTML = String(v).replace(/[&<>"]/g,
        c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
    },
  });
  els[id] = node;
  return node;
}
// Node: document/localStorage — getter-only глобалы, нужен defineProperty.
const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('document', {
  getElementById: el,
  querySelector: () => null,
  querySelectorAll: () => [],
  documentElement: { setAttribute() {}, style: { setProperty() {} } },
  createElement: () => el('div' + Math.random()),
  addEventListener() {},
});
defG('window', { addEventListener() {}, dispatchEvent() {} });
const store = {};
defG('localStorage', {
  getItem: k => (k in store ? store[k] : null),
  setItem: (k, v) => { store[k] = String(v); },
  removeItem: k => { delete store[k]; },
});

console.log('providers: источник постов в шапке для не-админа\n');

(async () => {
  // ── API-стаб: фиксируем вызовы, сети не трогаем ──
  const calls = [];
  API.get = async (path) => {
    calls.push(['GET', path]);
    if (path === '/providers') {
      return {
        providers: [{ value: 'rule34', name: 'rule34.xxx' }, { value: 'gelbooru', name: 'Gelbooru' }],
        provider: 'gelbooru',
        max_query_len: 3800,
      };
    }
    if (path === '/settings') return { api_keys: [] };
    throw new Error('неожиданный GET ' + path);
  };
  API.post = async (path, body) => {
    calls.push(['POST', path, body]);
    if (path !== '/providers') throw new Error('неожиданный POST ' + path);
    return { ok: true, provider: body.provider };
  };
  API.invalidate = () => {};
  App.showToast = () => {};
  App.loadPosts = () => {};

  App.els.settingProvider = el('setting-provider');
  App.state.user = { username: 'viewer', is_admin: false };
  App.state.activeProvider = 'rule34';
  App.state.providers = [];

  // ── 1. loadSettings не-админа: список источников всё равно приходит ──
  await App.loadSettings();
  check('не-админ ходит в GET /api/providers', calls.some(c => c[0] === 'GET' && c[1] === '/providers'));
  check('не-админ НЕ ходит в GET /api/settings', !calls.some(c => c[1] === '/settings'));
  check('activeProvider взят из /providers', App.state.activeProvider === 'gelbooru', App.state.activeProvider);
  check('список источников в state', App.state.providers.length === 2, App.state.providers.length);
  check('бейдж показывает имя активного источника',
    document.getElementById('provider-badge-label').textContent === 'Gelbooru',
    document.getElementById('provider-badge-label').textContent);
  const menuHtml = document.getElementById('provider-menu-list').innerHTML;
  check('меню источников заполнено', menuHtml.includes('rule34.xxx') && menuHtml.includes('Gelbooru'), menuHtml);
  check('в меню подсвечен активный', /provider-menu-item active" data-value="gelbooru"/.test(menuHtml), menuHtml);
  check('селект настроек получил список', el('setting-provider').innerHTML.includes('Gelbooru'));

  // ── 2. Смена источника из шапки уходит в POST /api/providers ──
  await App.switchProvider('rule34');
  const post = calls.find(c => c[0] === 'POST');
  check('switchProvider → POST /api/providers', !!post && post[1] === '/providers', JSON.stringify(post));
  check('в body ушёл выбранный источник', !!post && post[2] && post[2].provider === 'rule34');
  check('активный источник обновился', App.state.activeProvider === 'rule34', App.state.activeProvider);
  check('бейдж перерисован', document.getElementById('provider-badge-label').textContent === 'rule34.xxx');
  // Повторный вызов с тем же значением — no-op, второй POST не уходит.
  await App.switchProvider('rule34');
  check('повторный клик не шлёт POST', calls.filter(c => c[0] === 'POST').length === 1);

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();
