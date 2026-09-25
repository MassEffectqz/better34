// @ts-check
import test from 'node:test';
import assert from 'node:assert/strict';

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
