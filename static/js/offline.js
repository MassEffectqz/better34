// Оффлайн-очередь (задача 1): когда сервер недоступен, мутации
// (лайк/скрытие/коллекции/комментарии/пресеты) кладутся в IndexedDB
// и доставляются при появлении сети. Сетевой кэш ленты — в sw.js,
// тут только очередь мутаций.

const DB_NAME = 'briefly-offline';
const DB_VER = 1;
// Тег Background Sync (задача 2): по нему service worker будится при
// появлении сети — даже если вкладка уже закрыта.
const SYNC_TAG = 'briefly-flush';
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

// Просим браузер разбудить service worker, когда появится сеть
// (Background Sync, задача 2). Работает в Chromium; в Firefox/Safari
// SyncManager нет — там очередь доставляет обработчик 'online' в state.js.
// Некритично: любые неудачи просто оставляют старое поведение.
async function registerSync() {
  try {
    if (typeof window === 'undefined' || typeof navigator === 'undefined') return;
    if (!('serviceWorker' in navigator) || !('SyncManager' in window)) return;
    const reg = await navigator.serviceWorker.ready;
    const syncMgr = reg && /** @type {any} */ (reg).sync;
    if (syncMgr) await syncMgr.register(SYNC_TAG);
  } catch { /* нет SW/SyncManager — доставка по 'online' */ }
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
    registerSync();
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
// Задача 2: открытая вкладка (событие online) и Background Sync в sw.js могут
// проснуться одновременно — Web Locks не даёт двум «флашам» выгружать одну
// очередь параллельно, иначе одна мутация уйдёт на сервер дважды.
export async function flushOfflineQueue() {
  const locks = (typeof navigator !== 'undefined' && navigator.locks) ? navigator.locks : null;
  if (locks && typeof locks.request === 'function') {
    return locks.request(SYNC_TAG, () => doFlush());
  }
  return doFlush();
}

async function doFlush() {
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