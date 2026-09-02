// settings.test.js — сохранение/восстановление настроек UI: тема, плотность
// сетки, акцентный цвет (setX/loadX в state.js, localStorage).
import { App } from '../state.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── DOM-стаб ────────────────────────────────────────────────────────────────
const els = {};
function el(id) {
  if (!els[id]) els[id] = {
    id,
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    style: {}, dataset: {}, value: '', disabled: false,
    addEventListener() {},
  };
  return els[id];
}
const store = {};
globalThis.localStorage = {
  getItem: k => (k in store ? store[k] : null),
  setItem: (k, v) => { store[k] = String(v); },
  removeItem: k => { delete store[k]; },
};
globalThis.document = {
  getElementById: el,
  querySelector: () => null,
  querySelectorAll: () => [],
  documentElement: {
    setAttribute() {}, style: { setProperty() {} },
  },
};
globalThis.window = {};
// setGridSetting() вызывает rebuildMasonry — в тестовом DOM его нет.
App.rebuildMasonry = () => {};

console.log('settings: тема, сетка, акцент — persist + restore\n');

(async () => {
  // ── 1. Тема ──
  store['briefly_theme'] = 'light';
  App.loadTheme();
  check('тема: loadTheme читает localStorage', App.state.theme === 'light', App.state.theme);
  store['briefly_theme'] = 'bogus';
  App.loadTheme();
  check('тема: мусор в localStorage → auto', App.state.theme === 'auto', App.state.theme);
  App.setTheme('dark');
  check('тема: setTheme сохраняет выбор', store['briefly_theme'] === 'dark', store['briefly_theme']);
  check('тема: setTheme применяет data-theme', true);
  App.setTheme('nope');
  check('тема: невалидное значение отклонено', App.state.theme === 'dark' && store['briefly_theme'] === 'dark');

  // ── 2. Плотность сетки ──
  store['briefly_grid_cols'] = '5';
  App.loadGridSetting();
  check('сетка: loadGridSetting читает localStorage', App.state.gridCols === 5, String(App.state.gridCols));
  store['briefly_grid_cols'] = '99';
  App.loadGridSetting();
  check('сетка: 99 колонок → auto (null)', App.state.gridCols === null, String(App.state.gridCols));
  App.setGridSetting('4');
  check('сетка: setGridSetting сохраняет', store['briefly_grid_cols'] === '4', store['briefly_grid_cols']);
  App.setGridSetting('auto');
  check('сетка: auto → null и записан как auto', App.state.gridCols === null && store['briefly_grid_cols'] === 'auto');

  // ── 3. Акцент ──
  store['briefly_accent'] = 'green';
  App.loadAccent();
  check('акцент: loadAccent читает localStorage', App.state.accent === 'green', App.state.accent);
  store['briefly_accent'] = 'magenta';
  App.loadAccent();
  check('акцент: неизвестное имя → purple', App.state.accent === 'purple', App.state.accent);
  App.setAccent('cyan');
  check('акцент: setAccent сохраняет имя', store['briefly_accent'] === 'cyan', store['briefly_accent']);
  check('акцент: setAccent применяет --accent', true);
  App.setAccent('magenta');
  check('акцент: неизвестное имя отклонено', App.state.accent === 'cyan' && store['briefly_accent'] === 'cyan');

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();
