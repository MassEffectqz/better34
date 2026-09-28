// friends_errors.test.js — ошибки при добавлении друга не должны теряться.
//
// Регрессия, из-за которой баг выглядел загадочно: 400 от сервера уходил в
// оффлайн-очередь и возвращал {ok:true, offline:true} — то есть «успешно
// отложено». Настоящая причина всплывала позже и обезличенно: «Отклонено
// сервером действий: 1, возможно, истекла сессия». Пользователь не понимал,
// что не так, и не мог понять, что вставлял не тот код.
import { enqueueMutation } from '../offline.js';
import { API } from '../api.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

console.log('Друзья: ошибки не маскируются под «отложено офлайн»\n');

const realFetch = globalThis.fetch;
const realIDB = globalThis.indexedDB;
const realDoc = globalThis.document;
// API._headers() ищет токен в <meta name="briefly-token"> — без стаба падает.
globalThis.document = { querySelector: () => null };

// Стаб IndexedDB: очередь просто не должна наполняться.
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

(async () => {
  // ── 1. Добавление друга не очередится вовсе ────────────────────────────────
  check('POST /friends не очередится', (await enqueueMutation('POST', '/friends', { code: 'x' })) === false);
  check('POST /friends/sync не очередится', (await enqueueMutation('POST', '/friends/sync', {})) === false);
  check('очередь пуста', store.length === 0, `в очереди: ${store.length}`);

  // ── 2. Ошибка 400 доходит до вызывающего, а не прячется ─────────────────────
  globalThis.fetch = async () => ({
    ok: false,
    status: 400,
    headers: { get: () => 'application/json' },
    text: async () => JSON.stringify({ error: 'friend_code_invalid' }),
    json: async () => ({}),
  });
  let got = null;
  try {
    await API.post('/friends', { code: 'briefly-friend-v1:мусор' });
  } catch (err) {
    got = err;
  }
  check('400 приводит к ошибке, а не к «успешно отложено»', got !== null);
  // В сообщении должен быть код с сервера, иначе UI не покажет причину.
  check('сообщение содержит код friend_code_invalid',
    got && String(got.message).includes('friend_code_invalid'), got && got.message);
  check('очередь осталась пустой после 400', store.length === 0, `в очереди: ${store.length}`);

  // ── 2б. То же для эндпоинта, который в очереди разрешён ────────────────────
  // Раньше 400 маскировался под «отложено офлайн» для ЛЮБОЙ мутации: запрос
  // уходил в очередь и возвращал ok. Проверяем на /collection — он очередится.
  store = [];
  globalThis.fetch = async () => ({
    ok: false,
    status: 400,
    headers: { get: () => 'application/json' },
    text: async () => JSON.stringify({ error: 'invalid_request' }),
    json: async () => ({}),
  });
  let got2 = null;
  try {
    await API.post('/collection', { name: 'x' });
  } catch (err) {
    got2 = err;
  }
  check('400 в обычной мутации тоже не маскируется', got2 !== null);
  check('очередь не получила 400-запрос', store.length === 0, `в очереди: ${store.length}`);

  // ── 3. Серверная ошибка 5xx — наоборот, в очередь (попробуем позже) ─────────
  globalThis.fetch = async () => {
    throw new TypeError('network down');
  };
  let masked = null;
  try {
    masked = await API.post('/like/77', { liked: true });
  } catch (err) {
    masked = err;
  }
  check('сетевой сбой честно кладётся в очередь', masked && masked.offline === true, JSON.stringify(masked));
  check('в очередь попал лайк', store.some(x => x.endpoint === '/like/77'));

  console.log(`\n${failed ? 'FAILED' : 'ok'}: ${passed} passed, ${failed} failed`);
  globalThis.fetch = realFetch;
  globalThis.indexedDB = realIDB;
  globalThis.document = realDoc;
  if (failed) process.exit(1);
})();
