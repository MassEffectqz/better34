// state_storage.js — управление постоянным хранилищем, темами оформления,
// акцентными цветами и настройками сетки с поддержкой обратной совместимости (better34_* с briefly_* fallback).
import { App } from './state.js';

/**
 * Чтение из LocalStorage с обратной совместимостью (better34_* -> briefly_*).
 * @param {string} key
 * @param {string} [fallbackKey]
 * @returns {string | null}
 */
export function getStorage(key, fallbackKey = undefined) {
  try {
    const val = localStorage.getItem(key);
    if (val !== null) return val;
    if (fallbackKey && fallbackKey !== key) {
      return localStorage.getItem(fallbackKey);
    }
  } catch { /* private mode */ }
  return null;
}

/**
 * Запись в LocalStorage с зеркалированием в fallbackKey для 100% обратной совместимости.
 * @param {string} key
 * @param {any} val
 * @param {string} [fallbackKey]
 */
export function setStorage(key, val, fallbackKey = undefined) {
  try {
    const str = String(val);
    localStorage.setItem(key, str);
    if (fallbackKey && fallbackKey !== key) {
      localStorage.setItem(fallbackKey, str);
    }
  } catch { /* private mode */ }
}

/**
 * Удаление ключа из LocalStorage (включая fallback).
 * @param {string} key
 * @param {string} [fallbackKey]
 */
export function removeStorage(key, fallbackKey = undefined) {
  try {
    localStorage.removeItem(key);
    if (fallbackKey && fallbackKey !== key) {
      localStorage.removeItem(fallbackKey);
    }
  } catch { /* private mode */ }
}

export const StorageMethods = {
  getStorage,
  setStorage,
  removeStorage,

  // Доступные темы: базовые — авто/тёмная/светлая, плюс «цветовые схемы»:
  // deep — AMOLED-чёрный для OLED-экранов, sepia — тёплая «бумажная» светлая.
  THEMES: ['auto', 'dark', 'light', 'deep', 'sepia'],

  loadTheme() {
    let theme = 'auto';
    const saved = getStorage('better34_theme', 'briefly_theme');
    if (saved) theme = saved;
    if (!this.THEMES.includes(theme)) theme = 'auto';
    this.state.theme = theme;
    this.applyResolvedTheme();
    // Режим «Авто»: следим за системной темой вживую.
    if (typeof window !== 'undefined' && window.matchMedia) {
      if (!this._themeMQ) {
        this._themeMQ = window.matchMedia('(prefers-color-scheme: light)');
        const onChange = () => {
          if (this.state.theme === 'auto') this.applyResolvedTheme();
        };
        if (this._themeMQ.addEventListener) this._themeMQ.addEventListener('change', onChange);
        else if (this._themeMQ.addListener) this._themeMQ.addListener(onChange);
      }
    }
  },

  resolvedTheme() {
    if (this.state.theme !== 'auto') return this.state.theme;
    try {
      if (typeof window !== 'undefined' && window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) return 'light';
    } catch { /* noop */ }
    return 'dark';
  },

  applyResolvedTheme() {
    if (typeof document !== 'undefined' && document.documentElement && typeof document.documentElement.setAttribute === 'function') {
      document.documentElement.setAttribute('data-theme', this.resolvedTheme());
    }
  },

  setTheme(theme) {
    if (!this.THEMES.includes(theme)) return;
    this.state.theme = theme;
    this.applyResolvedTheme();
    setStorage('better34_theme', theme, 'briefly_theme');
  },

  toggleTheme() {
    const next = this.resolvedTheme() === 'dark' ? 'light' : 'dark';
    this.setTheme(next);
  },

  loadGridSetting() {
    const v = getStorage('better34_grid_cols', 'briefly_grid_cols');
    const n = parseInt(v || '', 10);
    this.state.gridCols = (Number.isInteger(n) && n >= 2 && n <= 6) ? n : null;
    this.renderGridMenu();
  },

  setGridSetting(val) {
    const n = parseInt(val || '', 10);
    this.state.gridCols = (val === 'auto' || !Number.isInteger(n)) ? null : (n >= 2 ? Math.min(n, 6) : null);
    setStorage('better34_grid_cols', this.state.gridCols === null ? 'auto' : String(this.state.gridCols), 'briefly_grid_cols');
    this.renderGridMenu();
    if (typeof this.rebuildMasonry === 'function') {
      this.rebuildMasonry();
    }
  },

  loadAutoRefreshSetting() {
    const v = getStorage('better34_auto_refresh_feed', 'briefly_auto_refresh_feed');
    this.state.autoRefreshFeed = v !== '0';
  },

  setAutoRefreshSetting(val) {
    this.state.autoRefreshFeed = !!val;
    setStorage('better34_auto_refresh_feed', this.state.autoRefreshFeed ? '1' : '0', 'briefly_auto_refresh_feed');
  },

  loadSettings() {
    this.loadTheme();
    this.loadGridSetting();
    this.loadAccent();
    this.loadAutoRefreshSetting();
  },

  renderGridMenu() {
    const active = this.state.gridCols === null ? 'auto' : String(this.state.gridCols);
    if (typeof document !== 'undefined' && typeof document.querySelectorAll === 'function') {
      document.querySelectorAll('.grid-opt').forEach((/** @type {HTMLElement} */ b) => {
        b.classList.toggle('active', b.dataset.cols === active);
      });
    }
  },

  cycleGridDensity() {
    const order = ['auto', '2', '3', '4', '5', '6'];
    const cur = this.state.gridCols === null ? 'auto' : String(this.state.gridCols);
    const next = order[(order.indexOf(cur) + 1) % order.length];
    this.setGridSetting(next);
    if (typeof this.showToast === 'function') {
      this.showToast(`Сетка: ${next === 'auto' ? 'авто' : next + ' в ряд'}`);
    }
  },

  ACCENTS: {
    purple: ['#a78bfa', '#7c5cbf'],
    indigo: ['#818cf8', '#4f46e5'],
    blue: ['#60a5fa', '#3b82f6'],
    sky: ['#38bdf8', '#0284c7'],
    cyan: ['#22d3ee', '#0891b2'],
    teal: ['#2dd4bf', '#0d9488'],
    green: ['#34d399', '#059669'],
    lime: ['#a3e635', '#65a30d'],
    amber: ['#fbbf24', '#d97706'],
    orange: ['#fb923c', '#ea580c'],
    rose: ['#fb7185', '#e11d48'],
    pink: ['#f472b6', '#db2777'],
    red: ['#f87171', '#dc2626'],
  },

  // mixColor затемняет шестизначный hex к чёрному (ratio 0..1) — из него
  // строится --accent-dim для кастомного цвета.
  mixColor(hex, ratio) {
    const n = parseInt(hex.slice(1), 16);
    if (!Number.isFinite(n)) return hex;
    const r = (n >> 16) & 0xFF, g = (n >> 8) & 0xFF, b = n & 0xFF;
    const dim = (v) => Math.round(v * (1 - ratio)).toString(16).padStart(2, '0');
    return '#' + dim(r) + dim(g) + dim(b);
  },

  customAccentColor() {
    const v = getStorage('better34_accent_custom', 'briefly_accent_custom');
    return (v && /^#[0-9a-f]{6}$/i.test(v)) ? v.toLowerCase() : null;
  },

  loadAccent() {
    let name = 'purple';
    const saved = getStorage('better34_accent', 'briefly_accent');
    if (saved) name = saved;
    if (name !== 'custom' && !this.ACCENTS[name]) name = 'purple';
    this.state.accent = name;
    this.applyAccent(name);
  },

  applyAccent(name) {
    let a, d;
    if (name === 'custom') {
      const c = this.customAccentColor();
      if (!c) { a = this.ACCENTS.purple[0]; d = this.ACCENTS.purple[1]; }
      else { a = c; d = this.mixColor(c, 0.34); }
    } else {
      const pair = this.ACCENTS[name];
      if (!pair) return;
      a = pair[0]; d = pair[1];
    }
    if (typeof document !== 'undefined' && document.documentElement && document.documentElement.style && typeof document.documentElement.style.setProperty === 'function') {
      document.documentElement.style.setProperty('--accent', a);
      document.documentElement.style.setProperty('--accent-dim', d);
    }
  },

  setAccent(name) {
    if (name === 'custom') {
      const input = typeof document !== 'undefined'
        ? /** @type {HTMLInputElement | null} */ (document.getElementById('setting-accent-custom'))
        : null;
      const raw = input && /^#[0-9a-f]{6}$/i.test(input.value) ? input.value.toLowerCase() : this.customAccentColor();
      if (!raw) return;
      setStorage('better34_accent_custom', raw, 'briefly_accent_custom');
      this.state.accent = 'custom';
      this.applyAccent('custom');
      setStorage('better34_accent', 'custom', 'briefly_accent');
      this.renderAccent();
      return;
    }
    if (!this.ACCENTS[name]) return;
    this.state.accent = name;
    this.applyAccent(name);
    setStorage('better34_accent', name, 'briefly_accent');
    this.renderAccent();
  },

  renderAccent() {
    if (typeof document !== 'undefined' && typeof document.querySelectorAll === 'function') {
      document.querySelectorAll('.accent-swatch').forEach((/** @type {HTMLElement} */ b) => {
        b.classList.toggle('active', b.dataset.accent === this.state.accent);
      });
      const inp = /** @type {HTMLInputElement | null} */ (document.getElementById('setting-accent-custom'));
      if (inp) {
        inp.classList.toggle('active', this.state.accent === 'custom');
        if (this.state.accent === 'custom') {
          const c = this.customAccentColor();
          if (c) inp.value = c;
        }
      }
    }
  },
};

export function extendStorage(targetApp) {
  Object.assign(targetApp, StorageMethods);
  return targetApp;
}

export { App };

export const THEMES = StorageMethods.THEMES;
export const ACCENTS = StorageMethods.ACCENTS;
export const loadTheme = StorageMethods.loadTheme;
export const resolvedTheme = StorageMethods.resolvedTheme;
export const applyResolvedTheme = StorageMethods.applyResolvedTheme;
export const setTheme = StorageMethods.setTheme;
export const toggleTheme = StorageMethods.toggleTheme;
export const loadGridSetting = StorageMethods.loadGridSetting;
export const setGridSetting = StorageMethods.setGridSetting;
export const loadAutoRefreshSetting = StorageMethods.loadAutoRefreshSetting;
export const setAutoRefreshSetting = StorageMethods.setAutoRefreshSetting;
export const loadSettings = StorageMethods.loadSettings;
export const renderGridMenu = StorageMethods.renderGridMenu;
export const cycleGridDensity = StorageMethods.cycleGridDensity;
export const mixColor = StorageMethods.mixColor;
export const customAccentColor = StorageMethods.customAccentColor;
export const loadAccent = StorageMethods.loadAccent;
export const applyAccent = StorageMethods.applyAccent;
export const setAccent = StorageMethods.setAccent;
export const renderAccent = StorageMethods.renderAccent;
