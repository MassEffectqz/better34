// offline_sync.test.js — задача 2: Background Sync. Регистрация тега при
// постановке в очередь, Web Locks вокруг flush и сам sync-обработчик в sw.js.
import { enqueueMutation, flushOfflineQueue } from '../offline.js';
import fs from 'node:fs';
import vm from 'node:vm';

let passed = 0, failed =0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}
const flushMicrotasks = () => new Promise(r => setTimeout(r, 0));

// ── Memory-IDB ─────────────────────────────────────────────────────────────
let store = [];
let nextId = 1;
function installIdb() {
  store = []; nextId = 1;
  globalThis.indexedDB = {
    open() {
      const req = {
        result: {
          objectStoreNames: { contains: () => true },
          transaction() {
            const tx = {
              oncomplete: null, onerror: null,
              objectStore() {
                return {
                  add(rec) { store.push({ ...rec, id: nextId++ }); },
                  getAll() {
                    const r = { result: store.map(x => x), onsuccess: null, onerror: null };
                    queueMicrotask(() => r.onsuccess && r.onsuccess());
                    return r;
                  },
                  delete(id) { store = store.filter(x => x.id !== id); },
                };
              },
            };
            queueMicrotask(() => tx.oncomplete && tx.oncomplete());
            return tx;
          },
        },
        onsuccess: null, onerror: null, onupgradeneeded: null,
      };
      queueMicrotask(() => req.onsuccess && req.onsuccess());
      return req;
    },
  };
}
function setNavigator(v) {
  try { delete globalThis.navigator; } catch { /* Node >=21: только getter */ }
  Object.defineProperty(globalThis, 'navigator', { value: v, configurable: true });
}

console.log('Background Sync: регистрация тега, Web Locks, sync в sw.js\n');

(async () => {
  // ── 1. enqueue просит sync, когда SyncManager доступен ──
  installIdb();
  const registered = [];
  setNavigator({
    onLine: true,
    serviceWorker: { ready: Promise.resolve({ sync: { register: async (tag) => registered.push(tag) } }) },
  });
  globalThis.window = { SyncManager: class {} };
  check('enqueue: мутация сохранена', (await enqueueMutation('POST', '/like/5', { id: 5 })) === true);
  await flushMicrotasks();
  check('enqueue: зарегистрирован sync-тег briefly-flush', registered.includes('briefly-flush'),
    JSON.stringify(registered));

  // ── 2. Нет SyncManager (Firefox/Safari) — тихий фолбэк, очередь работает ──
  installIdb();
  registered.length = 0;
  setNavigator({ onLine: true }); // serviceWorker отсутствует
  globalThis.window = {};
  check('enqueue без SyncManager: всё равно true', (await enqueueMutation('POST', '/like/6', {})) === true);
  await flushMicrotasks();
  check('enqueue без SyncManager: register не вызывался', registered.length === 0);
  check('enqueue без SyncManager: запись в очереди', store.length === 1);

  // ── 3. flush берёт Web Locks, когда они есть ──
  const lockCalls = [];
  setNavigator({
    onLine: true,
    locks: { request: async (name, cb) => { lockCalls.push(name); return cb(); } },
  });
  globalThis.fetch = async () => ({ ok: true, status: 200 });
  store = [{ id: 1, method: 'POST', endpoint: '/like/1', body: {}, ts: 1 }];
  const flushedLocked = await flushOfflineQueue();
  check('flush: выполнен под блокировкой briefly-flush', lockCalls.includes('briefly-flush'), JSON.stringify(lockCalls));
  check('flush: под локом очередь доставлена', flushedLocked === 1 && store.length === 0);

  // ── 4. Нет Web Locks — фолбэк на прямой flush ──
  setNavigator({ onLine: true });
  store = [{ id: 2, method: 'POST', endpoint: '/like/2', body: {}, ts: 2 }];
  const flushedPlain = await flushOfflineQueue();
  check('flush без Web Locks: работает как раньше', flushedPlain === 1 && store.length === 0);

  // ── 5. sw.js: sync-обработчик реально выгружает очередь ──
  const swCode = fs.readFileSync(new URL('../../sw.js', import.meta.url), 'utf8');
  const listeners = {};
  const swFetchCalls = [];
  let swStore = [{ id: 1, method: 'POST', endpoint: '/like/9', body: { id: 9 }, ts: 1 }];
  const swFetch = async (url, opts) => {
    swFetchCalls.push({ url, opts });
    if (url === '/api/like/9') return { ok: true, status: 200 };
    throw new Error('offline');
  };
  const swIdb = {
    open() {
      const req = {
        result: {
          objectStoreNames: { contains: () => true },
          transaction() {
            const tx = {
              oncomplete: null, onerror: null,
              objectStore() {
                return {
                  getAll() {
                    const r = { result: swStore.map(x => x), onsuccess: null, onerror: null };
                    queueMicrotask(() => r.onsuccess && r.onsuccess());
                    return r;
                  },
                  delete(id) { swStore = swStore.filter(x => x.id !== id); },
                };
              },
            };
            queueMicrotask(() => tx.oncomplete && tx.oncomplete());
            return tx;
          },
        },
        onsuccess: null, onerror: null, onupgradeneeded: null,
      };
      queueMicrotask(() => req.onsuccess && req.onsuccess());
      return req;
    },
  };
  const sandbox = {
    self: { addEventListener: (type, fn) => { listeners[type] = fn; }, location: { origin: 'http://localhost' } },
    Response: { error: () => ({ error: true }) },
    caches: { open: async () => ({ put: async () => {}, match: async () => null }), keys: async () => [], delete: async () => {}, match: async () => null },
    fetch: swFetch,
    indexedDB: swIdb,
    URL,
    console,
  };
  vm.createContext(sandbox);
  vm.runInContext(swCode, sandbox);
  check('sw.js: обработчик sync зарегистрирован', typeof listeners.sync === 'function');
  if (listeners.sync) {
    swFetchCalls.length = 0;
    listeners.sync({ tag: 'other-tag', waitUntil: () => {} });
    check('sw.js: чужой sync-тег игнорируется', swFetchCalls.length === 0);

    swFetchCalls.length = 0;
    const waits = [];
    listeners.sync({ tag: 'briefly-flush', waitUntil: (p) => waits.push(p) });
    check('sw.js: waitUntil получил промис', waits.length === 1);
    const n = await waits[0];
    check('sw.js: очередь доставлена из воркера', n === 1, 'flushed=' + n);
    check('sw.js: URL /api + endpoint', swFetchCalls[0] && swFetchCalls[0].url === '/api/like/9',
      JSON.stringify(swFetchCalls.map(c => c.url)));
    check('sw.js: доставленное вычищено из IndexedDB', swStore.length === 0, JSON.stringify(swStore));

    // сеть опять пропала: всё остаётся в очереди до следующего sync
    swStore = [
      { id: 3, method: 'POST', endpoint: '/like/1', body: {}, ts: 3 },
      { id: 4, method: 'POST', endpoint: '/like/2', body: {}, ts: 4 },
    ];
    const waits2 = [];
    listeners.sync({ tag: 'briefly-flush', waitUntil: (p) => waits2.push(p) });
    const n2 = await waits2[0];
    check('sw.js: без сети ничего не доставлено и не удалено', n2 === 0 && swStore.length === 2,
      'flushed=' + n2 + ' store=' + JSON.stringify(swStore));
  }

  // прибираем глобальные стабы, чтобы не влиять на другие файлы node --test
  try { delete globalThis.window; } catch { globalThis.window = undefined; }
  try { delete globalThis.navigator; } catch { /* noop */ }

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();