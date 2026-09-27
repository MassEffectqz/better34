// offline_library.js — библиотека офлайн. Сервис-воркер уже кэширует ленту
// (network-first) и миниатюры (cache-first), но кэш наполняется только тем,
// что вы пролистали. Здесь — явная загрузка всей библиотеки заранее, счётчик
// прогресса, учёт места и очистка. Плюс баннер «нет сети».
import { App } from './state.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

const PREFS = {
  concurrency: 4,        // одновременных запросов к нашим миниатюрам
  pageSize: 100,         // постов за страницу /api/local (сервер больше не берёт)
  budgetMB: 300,         // столько максимум готовы занять под офлайн
  maxPosts: 3000,        // и не больше стольких постов (вдруг галерея огромная)
};

App._offlineLib = { running: false, cancel: false, done: 0, total: 0 };

// offlineLibraryStats — что сейчас лежит в кэше и сколько это в мегабайтах.
// navigator.storage.estimate() даёт расход по origin (кэш + IndexedDB).
App.offlineLibraryStats = async function () {
  let entries = 0;
  try {
    if (typeof caches !== 'undefined') {
      const cache = await caches.open('briefly-api-v1');
      entries = (await cache.keys()).length;
    }
  } catch { /* кэш недоступен (приватный режим) */ }
  let usageMB = 0, quotaMB = 0;
  try {
    if (navigator.storage && typeof navigator.storage.estimate === 'function') {
      const est = await navigator.storage.estimate();
      usageMB = Math.round((est.usage || 0) / 1048576);
      quotaMB = Math.round((est.quota || 0) / 1048576);
    }
  } catch { /* браузер не умеет */ }
  return { entries, usageMB, quotaMB };
};

// prefetchOfflineLibrary тянет метаданты локальной библиотеки и миниатюры.
// Само кэширование делает сервис-воркер: мы просто делаем fetch на те же URL,
// что и карточки, поэтому дублирующая логика в SW не нужна.
App.prefetchOfflineLibrary = async function (onProgress) {
  const st = this._offlineLib;
  if (st.running) return { skipped: true };
  if (typeof navigator !== 'undefined' && navigator.onLine === false) {
    return { error: 'offline' };
  }
  const api = this.API || API;
  st.running = true; st.cancel = false; st.done = 0; st.total = 0;

  // 1) Собираем id локальных постов постранично.
  const ids = [];
  try {
    for (let page = 1; ids.length < PREFS.maxPosts && !st.cancel; page++) {
      const d = await api.get('/api/local?limit=' + PREFS.pageSize + '&page=' + page, { fresh: true });
      const posts = (d && (d.posts || d.items)) || [];
      if (!posts.length) break;
      for (const p of posts) ids.push(p.id);
    }
  } catch (err) {
    st.running = false;
    return { error: String(err && err.message || err) };
  }
  st.total = ids.length;
  if (onProgress) onProgress({ done: 0, total: st.total });

  // 2) Качаем миниатюры пачками по concurrency. Порциями, а не по одному:
  // так меньше накладных и можно в любой момент остановиться.
  let budgetHit = false;
  for (let i = 0; i < ids.length && !st.cancel && !budgetHit; i += PREFS.concurrency) {
    const batch = ids.slice(i, i + PREFS.concurrency);
    await Promise.all(batch.map((id) =>
      fetch('/api/thumb/' + id, { cache: 'no-cache' }).catch(() => null)));
    st.done += batch.length;
    if (onProgress) onProgress({ done: st.done, total: st.total });
    // Бюджет места: на мобильном 300 МБ — это много, дальше не лезем.
    if (i > 0 && (i % (PREFS.concurrency * 20)) === 0) {
      const s = await this.offlineLibraryStats();
      if (s.quotaMB && s.usageMB > Math.min(PREFS.budgetMB, s.quotaMB * 0.25)) budgetHit = true;
    }
  }
  st.running = false;
  return { done: st.done, total: st.total, cancelled: st.cancel, budgetHit };
};

App.cancelPrefetchOfflineLibrary = function () {
  this._offlineLib.cancel = true;
};

App.clearOfflineCache = async function () {
  try {
    if (typeof caches === 'undefined') return false;
    await caches.delete('briefly-api-v1');
    await caches.delete('briefly-static-v6');
    return true;
  } catch { return false; }
};

// bindOfflineLibraryUI вешает кнопки в «Настройки → Обслуживание» и
// показывает размер кэша. Вызывается один раз при инициализации.
App.bindOfflineLibraryUI = function () {
  if (this._offlineLibBound) return;
  this._offlineLibBound = true;
  this.ensureOfflineBanner();
  const $ = (id) => document.getElementById(id);
  const info = $('offline-lib-info');
  const btn = $('btn-offline-lib');
  const cancel = $('btn-offline-lib-cancel');
  const clear = $('btn-offline-lib-clear');

  const refreshSize = async () => {
    if (!info) return;
    const s = await this.offlineLibraryStats();
    info.textContent = s.quotaMB
      ? tf('offlineLib.size', { n: s.entries, mb: s.usageMB })
      : (s.entries ? tf('offlineLib.sizeNoQuota', { n: s.entries }) : '');
  };
  this._refreshOfflineLibSize = refreshSize;

  if (btn) btn.addEventListener('click', async () => {
    btn.disabled = true;
    if (cancel) cancel.classList.remove('hidden');
    const res = await this.prefetchOfflineLibrary((p) => {
      if (info) info.textContent = tf('offlineLib.progress', { done: p.done, total: p.total });
    });
    btn.disabled = false;
    if (cancel) cancel.classList.add('hidden');
    if (!info) return;
    if (res.error) {
      info.textContent = res.error === 'offline' ? t('offlineLib.needNet') : t('offlineLib.failed');
    } else if (res.cancelled) {
      info.textContent = tf('offlineLib.cancelled', { n: res.done });
    } else if (res.budgetHit) {
      const s = await this.offlineLibraryStats();
      info.textContent = tf('offlineLib.budget', { n: s.usageMB });
    } else {
      info.textContent = tf('offlineLib.done', { n: res.done || 0 });
    }
    await refreshSize();
  });
  if (cancel) cancel.addEventListener('click', () => this.cancelPrefetchOfflineLibrary());
  if (clear) clear.addEventListener('click', async () => {
    const ok = await this.confirmDialog({
      title: t('offlineLib.title'),
      message: t('offlineLib.confirmClear'),
      okText: t('confirm.ok'),
      danger: true,
    });
    if (!ok) return;
    const done = await this.clearOfflineCache();
    if (info) info.textContent = done ? t('offlineLib.cleared') : t('offlineLib.failed');
    await refreshSize();
  });
  refreshSize();
};

// ── Баннер «нет сети» ──────────────────────────────────────────────────────
App.ensureOfflineBanner = function () {
  if (this._offlineBanner) return this._offlineBanner;
  const el = document.createElement('div');
  el.className = 'offline-banner hidden';
  el.setAttribute('role', 'status');
  el.setAttribute('aria-live', 'polite');
  el.textContent = t('offlineLib.banner');
  document.body.appendChild(el);
  this._offlineBanner = el;
  const sync = () => {
    const off = typeof navigator !== 'undefined' && navigator.onLine === false;
    el.classList.toggle('hidden', !off);
    if (!off && typeof this.flushOfflineQueue === 'function') this.flushOfflineQueue();
  };
  window.addEventListener('online', sync);
  window.addEventListener('offline', sync);
  sync();
  return el;
};
