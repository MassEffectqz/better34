/* briefly service worker.
 * Стратегии:
 *  - /api/* и /events — только сеть (данные всегда свежие, свои ошибки UI);
 *  - навигация — сеть, при неудаче кэш страницы, иначе офлайн-заглушка;
 *  - /static/* — cache-first (URL версионирован ?v=..., immutable).
 */
const CACHE = 'briefly-static-v6';
const API_CACHE = 'briefly-api-v1';
const PRECACHE = ['/static/offline.html'];

// JS/CSS не кэшируем жёстко: URL модулей фиксированы (?v= только у входа),
// поэтому код всегда тянем из сети и лишь fallback'ом держим в кэше.
// Исключение — собранный esbuild-бандл (/static/js/dist/): его URL
// версионируется ?v= вместе с точкой входа, ему положен cache-first.
const NO_HARD_CACHE = /\/static\/(?!js\/dist\/).+\.(js|css)$/;

// Ответы ленты (задача 1, offline-first): network-first, при обрыве —
// отдаём последний успешный ответ (лента листается и без сервера).
const API_NETWORK_FIRST = /^\/api\/(posts|posts-by-ids|local|profile)(\?|$)/;

// Миниатюры неизменяемы по построению (JPEG по id) — их кэшируем намертво.
const API_THUMB_FIRST = /^\/api\/thumb\//;

self.addEventListener('install', (e) => {
  e.waitUntil(
    caches.open(CACHE)
      .then((c) => c.addAll(PRECACHE))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    const names = await caches.keys();
    await Promise.all(names.filter((n) => n !== CACHE && n !== API_CACHE).map((n) => caches.delete(n)));
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  let url;
  try { url = new URL(req.url); } catch { return; }
  if (url.origin !== self.location.origin) return;

  // API и SSE — только сеть (данные всегда свежие, свои ошибки UI).
  if (url.pathname.startsWith('/api/events')) return;

  // Лента офлайн (задача 1): network-first, при обрыве — последний ответ.
  if (req.method === 'GET' && API_NETWORK_FIRST.test(url.pathname)) {
    e.respondWith((async () => {
      const cache = await caches.open(API_CACHE);
      try {
        const res = await fetch(req);
        if (res.ok) cache.put(req, res.clone());
        return res;
      } catch {
        const cached = await cache.match(req);
        return cached || Response.error();
      }
    })());
    return;
  }

  // Миниатюры: cache-first (они неизменяемы по построению).
  if (req.method === 'GET' && API_THUMB_FIRST.test(url.pathname)) {
    e.respondWith((async () => {
      const cache = await caches.open(API_CACHE);
      const cached = await cache.match(req);
      if (cached) return cached;
      try {
        const res = await fetch(req);
        if (res.ok) cache.put(req, res.clone());
        return res;
      } catch {
        return Response.error();
      }
    })());
    return;
  }

  // Прочие API — только сеть (данные всегда свежие, свои ошибки UI).
  if (url.pathname.startsWith('/api/')) return;

  // Навигация: сеть -> кэш -> офлайн-заглушка.
  if (req.mode === 'navigate') {
    e.respondWith((async () => {
      try {
        return await fetch(req);
      } catch {
        const cached = await caches.match(req, { ignoreSearch: true });
        if (cached) return cached;
        const offline = await caches.match('/static/offline.html');
        return offline || Response.error();
      }
    })());
    return;
  }

  // Версионированная статика без кода (картинки, шрифты) — cache-first.
  if (NO_HARD_CACHE.test(url.pathname)) {
    e.respondWith((async () => {
      try {
        const res = await fetch(req);
        if (res.ok) {
          const cache = await caches.open(CACHE);
          cache.put(req, res.clone());
        }
        return res;
      } catch {
        const cached = await caches.match(req);
        return cached || Response.error();
      }
    })());
    return;
  }

  e.respondWith((async () => {
    const cached = await caches.match(req);
    if (cached) return cached;
    try {
      const res = await fetch(req);
      if (res.ok && url.pathname.startsWith('/static/')) {
        const cache = await caches.open(CACHE);
        cache.put(req, res.clone());
      }
      return res;
    } catch {
      return cached || Response.error();
    }
  })());
});

// ── Background Sync (задача 2) ─────────────────────────────────────────────
// Страница кладёт мутации (лайк/скрытие/коллекции/комментарии) в IndexedDB
// 'briefly-offline' (static/js/offline.js) и просит sync 'briefly-flush'.
// Браузер будит этот обработчик при появлении сети, даже если все вкладки
// закрыты. Firefox/Safari SyncManager не поддерживают — там очередь
// доставляет обработчик 'online' в state.js при открытой вкладке.
const OFFLINE_DB = 'briefly-offline';
const SYNC_TAG = 'briefly-flush';

// Копия flush-логики offline.js: классический воркер не может импортировать
// ES-модуль страницы. Дубликат не опасен: очередь одна, параллельную доставку
// страницы и воркера разруливает Web Locks, а серверный ToggleLike/ToggleHide
// идемпотентны (повторный toggle вернёт текущее состояние, дублей в БД нет).
self.addEventListener('sync', (e) => {
  if (e.tag !== SYNC_TAG) return;
  e.waitUntil(flushQueueInSW());
});

async function flushQueueInSW() {
  let db;
  try {
    db = await new Promise((resolve, reject) => {
      const req = indexedDB.open(OFFLINE_DB, 1);
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  } catch { return 0; }

  // getAll + delete по одному — тем же составом ответов, что и на странице:
  // 2xx и 404/410 (объект удалён) — доставлено; 400/401/403 — устарело,
  // повтор бессмысленен; сетевой сбой — стоп до следующего sync.
  const list = await new Promise((resolve) => {
    try {
      const tx = db.transaction('queue', 'readonly');
      const req = tx.objectStore('queue').getAll();
      req.onsuccess = () => resolve(req.result || []);
      req.onerror = () => resolve([]);
    } catch { resolve([]); }
  });

  let flushed = 0;
  for (const item of list) {
    try {
      const res = await fetch('/api' + item.endpoint, {
        method: item.method,
        headers: { 'Content-Type': 'application/json' },
        body: ['GET', 'HEAD'].includes(item.method) ? undefined : JSON.stringify(item.body || {}),
      });
      if (res.ok || res.status === 404 || res.status === 410 ||
          res.status === 400 || res.status === 401 || res.status === 403) {
        await new Promise((resolve) => {
          try {
            const tx = db.transaction('queue', 'readwrite');
            tx.objectStore('queue').delete(item.id);
            tx.oncomplete = resolve;
            tx.onerror = resolve;
          } catch { resolve(); }
        });
        flushed++;
      }
    } catch {
      break; // сети всё ещё нет — остальное уедет при следующем sync
    }
  }
  try { db.close(); } catch { /* noop */ }
  return flushed;
}
