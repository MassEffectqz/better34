// api_mobile.test.js — поведение API-клиента на мобильной сети:
//  * дедлайн запроса вместо вечного «висящего» спиннера;
//  * один повтор GET, если сервер не ответил (смена Wi-Fi↔LTE);
//  * отмена вызывающим не превращается в ошибку и не ретраится;
//  * мутация по таймауту НЕ уходит в оффлайн-очередь (иначе переигровка
//    переключила бы лайк/скрытие обратно).
import { API } from '../api.js';
import { App } from '../state.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('document', { querySelector: () => null });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
defG('window', {});
defG('navigator', {});

// Мини-IndexedDB для оффлайн-очереди: складываем мутации в массив store.
const store = [];
function fakeTx() {
  const tx = {
    oncomplete: null,
    onerror: null,
    objectStore: () => ({ add: (v) => { store.push(v); setTimeout(() => tx.oncomplete && tx.oncomplete(), 0); } }),
  };
  return tx;
}
defG('indexedDB', {
  open() {
    // Настоящий IDB отдаёт объект запроса и потом вызывает onsuccess, а
    // данные лежат в req.result — код offline.js читает именно req.result.
    const req = { result: null, onsuccess: null, onerror: null, onupgradeneeded: null };
    req.result = {
      objectStoreNames: { contains: () => true },
      transaction: () => fakeTx(),
    };
    setTimeout(() => req.onsuccess && req.onsuccess(), 0);
    return req;
  },
});

// Тосты — наблюдаемый признак «мутация ушла в оффлайн-очередь».
const toasts = [];
App.showToast = (msg, kind) => toasts.push([msg, kind]);

const jsonResp = (data, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  headers: { get: (h) => (h === 'content-type' ? 'application/json' : null) },
  json: async () => data,
  text: async () => JSON.stringify(data),
});

console.log('API на мобильной сети: дедлайны и повторы\n');

// Ставим короткие дедлайны, чтобы тест не ждал реальные 20 с.
API.timeoutMs = 40;
API.mutationTimeoutMs = 40;
API.retryDelayMs = 5;
API.maxRetries = 1;
API._cache = {};
API._inflight = {};
API._inflightCtrl = {};
API._inflightAborts = {};

// ── 1. Таймаут GET → понятная ошибка, а не undefined ──────────────────────
{
  globalThis.fetch = (url, opts) => new Promise((_, rej) => {
    opts.signal.addEventListener('abort', () => {
      const e = new Error('aborted');
      e.name = 'AbortError';
      rej(e);
    });
  });
  let err = null;
  try { await API.get('/posts-by-ids?ids=1'); } catch (e) { err = e; }
  check('таймаут GET даёт ошибку, а не молчаливый undefined', !!err, String(err));
  check('ошибка помечена как таймаут', !!(err && err.timeout === true), JSON.stringify(err && err.message));
  check('сообщение объясняет проблему', !!(err && /время ожидания/i.test(err.message)), err && err.message);
}

// ── 2. Сетевой сбой → один повтор и успех ────────────────────────────────
{
  let calls = 0;
  globalThis.fetch = async () => {
    calls++;
    if (calls === 1) throw new TypeError('Failed to fetch');
    return jsonResp({ ok: true });
  };
  const data = await API.get('/profile');
  check('после сетевого сбоя GET повторяется ровно один раз', calls === 2, 'calls=' + calls);
  check('повтор вернул данные', data && data.ok === true, JSON.stringify(data));
}
{
  let calls = 0;
  globalThis.fetch = async () => { calls++; throw new TypeError('Failed to fetch'); };
  let err = null;
  try { await API.get('/local'); } catch (e) { err = e; }
  check('при повторном сбое ошибка пробрасывается', !!err, String(err));
  check('повторов не больше maxRetries+1', calls === 2, 'calls=' + calls);
}

// ── 3. Отмена вызывающим: без ошибки, без повтора ────────────────────────
{
  let calls = 0;
  globalThis.fetch = (url, opts) => new Promise((_, rej) => {
    calls++;
    opts.signal.addEventListener('abort', () => {
      const e = new Error('aborted');
      e.name = 'AbortError';
      rej(e);
    });
  });
  const ctl = new AbortController();
  const p = API.get('/posts', { signal: ctl.signal, fresh: true });
  setTimeout(() => ctl.abort(), 5);
  const res = await p;
  check('отмена вызывающим даёт undefined (как раньше), без ошибки', res === undefined, String(res));
  check('отменённый запрос не ретраится', calls === 1, 'calls=' + calls);
}

// ── 4. Ошибка сервера не ретраится ──────────────────────────────────────
{
  let calls = 0;
  globalThis.fetch = async () => { calls++; return jsonResp({ error: 'rate_limited' }, 429); };
  let err = null;
  try { await API.get('/posts', { fresh: true }); } catch (e) { err = e; }
  check('HTTP-ошибка (429) не повторяется', calls === 1, 'calls=' + calls);
  check('HTTP-ошибка пробрасывается как есть', !!err, String(err));
}

// ── 5. Мутация: таймаут → ошибка с пометкой, в очередь НЕ уходит ─────────
{
  store.length = 0;
  toasts.length = 0;
  globalThis.fetch = (url, opts) => new Promise((_, rej) => {
    opts.signal.addEventListener('abort', () => {
      const e = new Error('aborted');
      e.name = 'AbortError';
      rej(e);
    });
  });
  let err = null;
  try { await API.post('/like/42', { liked: true }); } catch (e) { err = e; }
  check('таймаут мутации даёт ошибку с пометкой timeout', !!(err && err.timeout === true), JSON.stringify(err && err.message));
  check('таймаутовая мутация НЕ повторяется на клиенте', store.length === 0, 'store=' + store.length);
  check('таймаутовая мутация не отправляется повторно из очереди',
    !toasts.some(([m]) => /отложено/i.test(m)), JSON.stringify(toasts));
}
{
  // Сетевой отказ (соединение рвалось) — действие честно откладывается.
  store.length = 0;
  toasts.length = 0;
  globalThis.fetch = async () => { throw new TypeError('Failed to fetch'); };
  const r = await API.post('/like/42', { liked: true });
  check('сетевой отказ кладёт мутацию в оффлайн-очередь', r && r.offline === true, JSON.stringify(r));
  check('в очередь попал объект (не строка) — doFlush сам сериализует',
    store.length === 1 && store[0].body && store[0].body.liked === true, JSON.stringify(store));
  check('пользователю показано, что действие отложено',
    toasts.some(([m]) => /отложено/i.test(m)), JSON.stringify(toasts));
}
{
  // Успешная мутация: тело уходит JSON-строкой ровно один раз.
  store.length = 0;
  let sent = null;
  globalThis.fetch = async (url, opts) => { sent = { url, method: opts.method, body: opts.body }; return jsonResp({ liked: true }); };
  const r = await API.post('/like/42', { liked: true });
  check('мутация возвращает разобранный JSON', r && r.liked === true, JSON.stringify(r));
  check('тело мутации сериализовано один раз', sent.body === '{"liked":true}', sent && sent.body);
  check('успешная мутация не попадает в очередь', store.length === 0);
}

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);