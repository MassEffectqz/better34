// offline_queue.test.js — оффлайн-очередь мутаций: что кладётся в IndexedDB
// (белый список isQueueable), порядок и условия доставки flushOfflineQueue.
import { enqueueMutation, flushOfflineQueue } from '../offline.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── IndexedDB-стаб: одна memory-очередь ────────────────────────────────────
let store = [];
let nextId = 1;
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

console.log('offline-очередь: isQueueable, enqueueMutation, flushOfflineQueue\n');

(async () => {
  // ── 1. Что очередится, а что нет ──
  const notQueueable = [
    ['GET', '/posts'], ['HEAD', '/posts'], ['POST', '/auth/login'], ['POST', '/auth/logout'],
    ['POST', '/download/77'], ['POST', '/settings'], ['POST', '/db/clean'],
    ['POST', '/dups/clean'], ['POST', '/recommend/abc'], ['POST', '/profile'],
    ['POST', '/rename/1'], ['POST', '/remote/push'],
  ];
  for (const [m, e] of notQueueable) {
    check(`не очередится: ${m} ${e}`, (await enqueueMutation(m, e, {})) === false);
  }
  check('исключение: /profile/meta очередится', (await enqueueMutation('POST', '/profile/meta', {})) === true);

  const okLike = await enqueueMutation('POST', '/like/77', { id: 77 });
  check('очередится: POST /like/77', okLike === true);
  const rec = store.find(x => x.endpoint === '/like/77');
  check('в очереди лежат method+endpoint+body+ts',
    rec && rec.method === 'POST' && rec.body && rec.body.id === 77 && typeof rec.ts === 'number',
    JSON.stringify(rec));
  check('DELETE тоже очередится', (await enqueueMutation('DELETE', '/collection/3')) === true);

  // ── 2. flush: всё доставляется и вычищается ──
  store = [
    { id: 1, method: 'POST', endpoint: '/like/77', body: { id: 77 }, ts: 1 },
    { id: 2, method: 'POST', endpoint: '/hide/9', body: { id: 9 }, ts: 2 },
  ];
  const calls = [];
  globalThis.fetch = async (url, opts) => {
    calls.push({ url, opts });
    return { ok: true, status: 200 };
  };
  const flushed = await flushOfflineQueue();
  check('flush: обе мутации доставлены', flushed === 2, 'flushed=' + flushed);
  check('flush: очередь пуста', store.length === 0, 'store=' + JSON.stringify(store));
  check('flush: URL это /api + endpoint', calls[0].url === '/api/like/77' && calls[1].url === '/api/hide/9',
    JSON.stringify(calls.map(c => c.url)));
  check('flush: метод и JSON-body сохранены',
    calls[0].opts.method === 'POST' && calls[0].opts.body === JSON.stringify({ id: 77 }),
    calls[0].opts.body);

  // ── 3. 400/404 считаются доставленными (повтор бессмысленен) ──
  store = [{ id: 5, method: 'POST', endpoint: '/like/1', body: {}, ts: 5 }];
  globalThis.fetch = async () => ({ ok: false, status: 400 });
  check('flush: 400 не залипает в очереди', (await flushOfflineQueue()) === 1 && store.length === 0);
  store = [{ id: 6, method: 'POST', endpoint: '/like/2', body: {}, ts: 6 }];
  globalThis.fetch = async () => ({ ok: false, status: 404 });
  check('flush: 404 (пост удалён) тоже вычищается', (await flushOfflineQueue()) === 1 && store.length === 0);

  // ── 4. Сети всё ещё нет: стоп на первом же сбое, очередь сохраняется ──
  store = [
    { id: 7, method: 'POST', endpoint: '/like/1', body: {}, ts: 7 },
    { id: 8, method: 'POST', endpoint: '/like/2', body: {}, ts: 8 },
  ];
  globalThis.fetch = async () => { throw new Error('offline'); };
  check('flush: без сети ничего не доставлено', (await flushOfflineQueue()) === 0);
  check('flush: без сети очередь не тронута', store.length === 2, 'store=' + JSON.stringify(store));

  store = [
    { id: 9, method: 'POST', endpoint: '/like/1', body: {}, ts: 9 },
    { id: 10, method: 'POST', endpoint: '/like/2', body: {}, ts: 10 },
  ];
  globalThis.fetch = async (url) => {
    if (url === '/api/like/1') return { ok: true, status: 200 };
    throw new Error('offline');
  };
  const partial = await flushOfflineQueue();
  check('flush: частичная доставка — стоп на сбое', partial === 1, 'flushed=' + partial);
  check('flush: доставленное вычищено, остальное ждёт сети',
    store.length === 1 && store[0].endpoint === '/like/2', JSON.stringify(store));

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();