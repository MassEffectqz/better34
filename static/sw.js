/* briefly service worker.
 * Стратегии:
 *  - /api/* и /events — только сеть (данные всегда свежие, свои ошибки UI);
 *  - навигация — сеть, при неудаче кэш страницы, иначе офлайн-заглушка;
 *  - /static/* — cache-first (URL версионирован ?v=..., immutable).
 */
const CACHE = 'briefly-static-v1';
const PRECACHE = ['/static/offline.html'];

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
    await Promise.all(names.filter((n) => n !== CACHE).map((n) => caches.delete(n)));
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  let url;
  try { url = new URL(req.url); } catch { return; }
  if (url.origin !== self.location.origin) return;

  // API и SSE — только сеть.
  if (url.pathname.startsWith('/api/') || url.pathname === '/api/events') return;

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

  // Прочая статика — cache-first с догрузкой мимо кэша.
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
