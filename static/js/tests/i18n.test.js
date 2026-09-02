// i18n.test.js — словари t/tf, фолбэк, setLang и applyI18n.
import { t, tf, getLang, setLang, applyI18n } from '../i18n.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// setLang() пишет localStorage, красит <html lang>, диспатчит briefly-lang.
const events = [];
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = { querySelectorAll() { return []; }, documentElement: { lang: '' } };
globalThis.CustomEvent = class {
  constructor(type, opts) { this.type = type; this.detail = opts && opts.detail; }
};
globalThis.window = { dispatchEvent(e) { events.push(e); } };

console.log('i18n: словари, tf-подстановки, setLang/applyI18n\n');

setLang('en');
check('setLang(en) применяет язык', getLang() === 'en', 'getLang=' + getLang());
check('t: EN словарь', t('menu.search') === 'Search', t('menu.search'));
check('tf: подстановка {n} (EN)', tf('batch.selected', { n: 3 }) === 'Selected: 3', tf('batch.selected', { n: 3 }));
check('setLang диспатчит briefly-lang', events.some(e => e.type === 'briefly-lang' && e.detail === 'en'),
  JSON.stringify(events.map(e => e.type)));

setLang('ru');
check('setLang(ru) переключает словарь', t('menu.search') === 'Поиск', t('menu.search'));
check('tf: подстановка {n} (RU)', tf('grid.n', { n: 5 }) === '5 колонки', tf('grid.n', { n: 5 }));
check('setLang пишет выбор в localStorage', localStorage.getItem('briefly_lang') === 'ru',
  localStorage.getItem('briefly_lang'));

check('несуществующий ключ возвращается как есть', t('no.such.key') === 'no.such.key', t('no.such.key'));

setLang('fr');
check('setLang игнорирует неизвестный язык', getLang() === 'ru', 'getLang=' + getLang());

// applyI18n: data-i18n → textContent, data-i18n-ph → placeholder,
// data-i18n-title → title.
const fakes = [
  { dataset: { i18n: 'menu.search' }, textContent: '' },
  { dataset: { i18nPh: 'search.placeholder' }, placeholder: '' },
  { dataset: { i18nTitle: 'search.clear' }, title: '' },
];
applyI18n({ querySelectorAll: () => fakes });
check('applyI18n: data-i18n → textContent', fakes[0].textContent === 'Поиск', fakes[0].textContent);
check('applyI18n: data-i18n-ph → placeholder', fakes[1].placeholder === 'Поиск тегов...', fakes[1].placeholder);
check('applyI18n: data-i18n-title → title', fakes[2].title === 'Очистить', fakes[2].title);

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);