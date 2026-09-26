// search_clear.test.js — крестик очистки строки поиска.
// Регрессия: класс .visible ставился только в onSearchInput (ввод с клавиатуры),
// а значение поля программно меняют ещё ~14 мест (пресет, подсказка, история,
// deep-link, «похожие», коллекция) — в них кнопка оставалась с opacity:0,
// то есть в поле есть текст, а крестика не видно.
import { App } from '../state.js';
import '../search.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {} });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
defG('document', { querySelector: () => null, querySelectorAll: () => [], createElement: () => ({ classList: mk() }), addEventListener() {}, removeEventListener() {} });

function mk() {
  const s = new Set();
  return {
    contains: (c) => s.has(c),
    add: (...c) => c.forEach((x) => s.add(x)),
    remove: (...c) => c.forEach((x) => s.delete(x)),
    toggle(c, f) { const on = f != null ? !!f : !s.has(c); if (on) s.add(c); else s.delete(c); return on; },
  };
}
const input = { value: '', focus() {} };
const clearBtn = { classList: mk() };
const a = Object.create(App);
a.els = { searchInput: input, searchClear: clearBtn };

console.log('Поиск: крестик очистки\n');

// ── 1. Пустое поле — крестик скрыт ────────────────────────────────────────
a.setSearchValue('');
check('пустое поле: крестик скрыт', !clearBtn.classList.contains('visible'));
check('пустое поле: значение пустое', input.value === '');

// ── 2. Программная установка (пресет/подсказка/история) ───────────────────
a.setSearchValue('catgirl neko');
check('после setSearchValue крестик виден', clearBtn.classList.contains('visible'));
check('значение поля записано', input.value === 'catgirl neko', input.value);

// ── 3. Обратный переход: строка очищена ───────────────────────────────────
a.setSearchValue('');
check('после очистки крестик снова скрыт', !clearBtn.classList.contains('visible'));

// ── 4. onSearchInput тоже синхронизирует (ввод с клавиатуры) ─────────────
a.state = { query: '', recommendActive: false };
a.updateQueryMeta = function () {};
a.renderQueryChips = function () {};
a.renderSuggestions = function () {};
// Остальное, что дёргает onSearchInput (в т.ч. отложенные таймеры) — заглушки:
// тест проверяет синхронизацию крестика, а не поиск.
a.hideHistory = function () {};
a.showHistory = function () {};
a.fetchSuggestions = function () {};
a.search = function () {};
a.suggestWord = () => ({ start: 0, end: 0, raw: '', prefix: '', word: '' });
input.value = 'abc';
a.onSearchInput();
check('ввод с клавиатуры: крестик виден', clearBtn.classList.contains('visible'));

// ── 5. Нет паники, если кнопки/поля нет (страница без шапки) ──────────────
const b = Object.create(App);
b.els = {};
b.setSearchValue('x');
b.syncSearchClear();
check('без searchInput/setSearchValue не падает', true);

// ── 6. Не-строки приводятся к строке ─────────────────────────────────────
a.setSearchValue(null);
check('null → пустая строка и скрытый крестик', input.value === '' && !clearBtn.classList.contains('visible'));
a.setSearchValue(42);
check('число → строка', input.value === '42' && clearBtn.classList.contains('visible'), input.value);

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);