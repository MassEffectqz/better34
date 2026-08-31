// Оффлайн-очередь (задача 1): когда сервер недоступен, мутации
// (лайк/скрытие/коллекции/комментарии/пресеты) кладутся в IndexedDB
// и доставляются при появлении сети. Сетевой кэш ленты — в sw.js,
// тут только очередь мутаций.

const DB_NAME = 'briefly-offline';
const DB_VER = 1;
let _dbPromise = null;

function idb() {
  if (_dbPromise) return _dbPromise;
  _dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VER);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains('queue')) {
        db.createObjectStore('queue', { keyPath: 'id', autoIncrement: true });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return _dbPromise;
}

// Что можно безопасно отложить. Любые не-GET методы — кандидаты, кроме
// сессий/загрузок/служебных — они бессмысленны офлайн.
function isQueueable(endpoint, method) {
  if (method === 'GET' || method === 'HEAD') return false;
  const e = String(endpoint || '');
  if (e.startsWith('/auth/')) return false;
  if (e.startsWith('/download')) return false;
  if (e.startsWith('/settings')) return false;
  if (e.startsWith('/db/')) return false;
  if (e.startsWith('/dups/')) return false;
  if (e.startsWith('/recommend/')) return false;
  if (e.startsWith('/profile') && !e.startsWith('/profile/meta')) return false;
  if (e.startsWith('/rename')) return false;
  if (e.startsWith('/remote/')) return false;
  return true;
}

export async function enqueueMutation(method, endpoint, body) {
  if (!isQueueable(endpoint, method)) return false;
  try {
    const db = await idb();
    await new Promise((resolve, reject) => {
      const tx = db.transaction('queue', 'readwrite');
      tx.objectStore('queue').add({ method, endpoint, body: body || null, ts: Date.now() });
      tx.oncomplete = resolve;
      tx.onerror = () => reject(tx.error);
    });
    return true;
  } catch {
    return false;
  }
}

async function listAll() {
  try {
    const db = await idb();
    return await new Promise((resolve, reject) => {
      const tx = db.transaction('queue', 'readonly');
      const req = tx.objectStore('queue').getAll();
      req.onsuccess = () => resolve(req.result || []);
      req.onerror = () => reject(req.error);
    });
  } catch {
    return [];
  }
}

async function removeItem(id) {
  try {
    const db = await idb();
    await new Promise((resolve) => {
      const tx = db.transaction('queue', 'readwrite');
      tx.objectStore('queue').delete(id);
      tx.oncomplete = resolve;
    });
  } catch { /* очередь не починится — пропускаем */ }
}

// Отправляет все отложенные мутации по порядку. Возвращает количество успешных.
export async function flushOfflineQueue() {
  const items = await listAll();
  let flushed = 0;
  for (const item of items) {
    try {
      const res = await fetch('/api' + item.endpoint, {
        method: item.method,
        headers: { 'Content-Type': 'application/json' },
        body: ['GET', 'HEAD'].includes(item.method) ? undefined : JSON.stringify(item.body || {}),
      });
      // 2xx и 404/410 (пост удалён на сервере) — считаем доставленным.
      if (res.ok || res.status === 404 || res.status === 410) {
        await removeItem(item.id);
        flushed++;
      } else {
        // 400/401/403 — данные устарели, повторять бессмысленно.
        await removeItem(item.id);
        flushed++;
      }
    } catch {
      // Сети всё ещё нет — останавливаемся, остальное уедет в следующий раз.
      break;
    }
  }
  return flushed;
}

export function hasPendingMutations() {
  return listAll().then((l) => l.length > 0);
}