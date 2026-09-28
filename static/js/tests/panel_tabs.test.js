// @ts-check
import test from 'node:test';
import assert from 'node:assert/strict';
import { readAllCss } from './read-css.mjs';

// Ширина панели настроек: вкладка «Сеть» кладёт в ряд имя ключа, сам ключ,
// user_id и кнопку. Ключ API — длинная строка, поэтому именно он должен
// получать основную долю свободного места, а панель — быть достаточно
// широкой. Обе вещи проверяем здесь: без них правка тихо откатывается к
// схлопнувшемуся полю, и заметить это можно только глазами.
test('панель настроек шире профильной и считается от ширины окна', async () => {
  const { App } = await import('../state.js');
  const a = Object.create(App);
  // Расчёт ширины читает window.innerWidth и document.documentElement
  // .clientWidth. В этом файле globalThis.document объявлен через
  // defineProperty, поэтому задаём их явно, а не присваиваем document.
  const at = (w) => {
    Object.defineProperty(globalThis, 'window', {
      value: { innerWidth: w }, configurable: true, writable: true,
    });
    if (globalThis.document) {
      Object.defineProperty(globalThis.document, 'documentElement', {
        value: { clientWidth: w }, configurable: true, writable: true,
      });
    }
  };
  at(1440);

  const settings = a.defaultPanelWidth('settingsPanel');
  const profile = a.defaultPanelWidth('profilePanel');
  assert.ok(settings >= 560, `настройки должны быть не уже 560, получили ${settings}`);
  assert.ok(settings > profile,
    `настроек (${settings}) должно быть шире профиля (${profile}) — иначе ключи снова не влезут`);

  // Узкое окно: панель не схлопывается и при этом не вылезает за экран.
  // Здесь 460 недостижима — на 420px экране вьюпорт сильнее, иначе правый
  // край (кнопка закрытия) уехал бы за границу.
  at(420);
  const narrow = a.clampPanelWidth(10, 'settingsPanel');
  assert.ok(narrow <= 420 - 24, `панель не должна быть шире окна: ${narrow}`);
  assert.ok(narrow >= 320, `панель не должна схлопнуться: ${narrow}`);

  // Старая узкая настройка из localStorage разжимается до рабочей — иначе
  // пользователь, однажды сузивший панель мышью, не увидит правки вовсе.
  at(1440);
  assert.equal(a.clampPanelWidth(340, 'settingsPanel'), 460,
    'сохранённые 340px обязаны подниматься до минимума');

  // Ширина не зависит от позиции сетки постов: на широком мониторе сетка уходит
  // влево, и прежний расчёт «свободного бока» уводил панель за экран.
  assert.ok(a.clampPanelWidth(2000, 'settingsPanel') <= a.settingsPanelMaxWidth());
  assert.ok(a.settingsPanelMaxWidth() <= 1440, 'панель не должна быть шире окна');
});

// Сетка ключей: соседние колонки фиксированы, поэтому «1fr» на ключе
// раздавался не поровну. Проверяем, что ключ забирает заметную долю.
test('поле ключа API получает основную долю ряда', () => {
  const css = readAllCss();
  // Мобильное переопределение (max-width:480px) сворачивает ряд в одну колонку —
  // берём БАЗОВОЕ правило, иначе проверялось бы оно, а не то, что реально
  // раскладывает ключи на десктопе. Якорь на display:grid, а не ^: файл в
  // CRLF, и ^ с флагом m здесь ведёт себя по-разному.
  const m = /\.api-key-row\{display:grid;grid-template-columns:([^}]*)\}/.exec(css);
  assert.ok(m, 'не найдена базовая сетка .api-key-row');
  assert.ok(m[1].includes('2.4fr'),
    'ключ должен забирать заметно большую долю ряда: ' + m[1]);
  assert.ok(!/90px/.test(m[1]),
    'фиксированные 90px у соседей съедали место ключа: ' + m[1]);
  // Ключ не переносится на вторую строку: в API он всегда одной строкой, и
  // перенос делал бы высоту ряда прыгающей при наборе.
  const value = css.match(/\.api-key-row \.api-key-value\{([\s\S]+?)\}/);
  assert.ok(value && /nowrap/.test(value[0]),
    'ключ не должен переноситься на вторую строку');
});

class MockClassList {
  constructor() { this.values = new Set(); }
  /** @param {...string} names */
  add(...names) { for (const name of names) this.values.add(name); }
  /** @param {...string} names */
  remove(...names) { for (const name of names) this.values.delete(name); }
  /** @param {string} name */
  contains(name) { return this.values.has(name); }
  /** @param {string} name @param {boolean} [force] */
  toggle(name, force) {
    const on = force === undefined ? !this.contains(name) : Boolean(force);
    if (on) this.add(name); else this.remove(name);
    return on;
  }
}

class MockNode {
  /** @param {string} [id] */
  constructor(id = '') {
    this.id = id;
    /** @type {Record<string, string>} */
    this.dataset = {};
    /** @type {Map<string, string>} */
    this.attrs = new Map();
    /** @type {Map<string, (event?: Record<string, any>) => void>} */
    this.listeners = new Map();
    this.classList = new MockClassList();
    this.tabIndex = 0;
    this.hidden = false;
    this.type = '';
    /** @type {MockNode | null} */
    this.nav = null;
    /** @type {MockNode[]} */
    this.tabs = [];
  }
  /** @param {string} name @param {string} value */
  setAttribute(name, value) { this.attrs.set(name, String(value)); }
  /** @param {string} name @returns {string | null} */
  getAttribute(name) { return this.attrs.get(name) ?? null; }
  /** @param {string} type @param {(event?: Record<string, any>) => void} fn */
  addEventListener(type, fn) { this.listeners.set(type, fn); }
  /** @param {string} type @param {Record<string, any>} [event] */
  fire(type, event = {}) { this.listeners.get(type)?.({ preventDefault() {}, ...event }); }
  focus() { /** @type {any} */ (globalThis.document).activeElement = this; }
  scrollIntoView() {}
  /** @param {string} selector @returns {MockNode | null} */
  querySelector(selector) { return selector.includes('panel-nav') ? this.nav : null; }
  /** @param {string} selector @returns {MockNode[]} */
  querySelectorAll(selector) { return selector.includes('panel-nav-item') ? this.tabs : []; }
}

/** @type {Map<string, string>} */
const store = new Map();
/** @type {Map<string, MockNode>} */
const contents = new Map();
const panel = new MockNode('profile-panel');
const nav = new MockNode();
panel.nav = nav;
nav.tabs = [];
for (const key of ['presets', 'likes', 'tags']) {
  const tab = new MockNode(`profile-tab-${key}`);
  const content = new MockNode(`tab-${key}`);
  tab.dataset.panelTab = key;
  tab.setAttribute('aria-controls', `tab-${key}`);
  tab.classList.add('panel-nav-item');
  content.setAttribute('role', 'tabpanel');
  nav.tabs.push(tab);
  contents.set(`tab-${key}`, content);
}
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  value: {
    getItem(/** @type {string} */ key) { return store.get(key) ?? null; },
    setItem(/** @type {string} */ key, /** @type {string} */ value) { store.set(key, String(value)); },
  },
});
Object.defineProperty(globalThis, 'document', {
  configurable: true,
  value: {
    activeElement: /** @type {MockNode | null} */ (null),
    /** @param {string} id @returns {MockNode | null} */
    getElementById(id) { return contents.get(id) ?? null; },
  },
});

const { App } = await import('../state.js');
/** @type {any} */ (App).els.profilePanel = panel;

/**
 * @param {string} key
 * @returns {MockNode}
 */
function content(key) {
  const node = contents.get(key);
  if (!node) throw new Error(`missing tab content: ${key}`);
  return node;
}

test('bindPanelTabs follows the HTML contract and restores a missing tab safely', () => {
  store.set('briefly_profile_tab', 'removed-section');
  const binding = App.bindPanelTabs('profilePanel', 'tab-', key => store.set('last-callback', key));
  if (!binding) throw new Error('binding failed');
  assert.equal(binding.storageKey, 'briefly_profile_tab');
  assert.equal(nav.getAttribute('role'), 'tablist');
  assert.equal(nav.tabs[0].getAttribute('aria-selected'), 'true');
  assert.equal(content('tab-presets').hidden, false);
  assert.equal(content('tab-likes').hidden, true);
  assert.equal(content('tab-presets').getAttribute('aria-labelledby'), 'profile-tab-presets');
});

test('mouse and keyboard activation persist state and wrap through sections', () => {
  nav.tabs[1].fire('click');
  assert.equal(store.get('briefly_profile_tab'), 'likes');
  assert.equal(content('tab-presets').hidden, true);
  assert.equal(content('tab-likes').hidden, false);

  nav.tabs[1].fire('keydown', { key: 'ArrowRight' });
  assert.equal(store.get('briefly_profile_tab'), 'tags');
  assert.equal(nav.tabs[2].getAttribute('aria-selected'), 'true');

  nav.tabs[2].fire('keydown', { key: 'ArrowRight' });
  assert.equal(store.get('briefly_profile_tab'), 'presets');
  nav.tabs[0].fire('keydown', { key: 'End' });
  assert.equal(store.get('briefly_profile_tab'), 'tags');
  nav.tabs[2].fire('keydown', { key: 'Home' });
  assert.equal(store.get('briefly_profile_tab'), 'presets');
});
