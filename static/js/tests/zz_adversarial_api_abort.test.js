'use strict';

const fs = require('fs');
const path = require('path');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

global.window = {};
global.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
global.document = {
  querySelector() { return null; },
};

// fetch-stub: запросы висят, пока их не резолвят; abort сигнала → reject(AbortError)
let fetchCalls = 0;
const pendingFetches = [];
global.fetch = (url, opts) => {
  fetchCalls++;
  return new Promise((resolve, reject) => {
    const rec = { url, resolve, reject, signal: opts.signal };
    const onAbort = () => {
      const e = new Error('Aborted');
      e.name = 'AbortError';
      reject(e);
    };
    opts.signal.addEventListener('abort', onAbort);
    pendingFetches.push(rec);
  });
};

const apiPath = path.join(__dirname, '..', 'api.js');
(0, eval)(fs.readFileSync(apiPath, 'utf8') + '\n;globalThis.API = API;');

console.log('API.get abort dedup (F5) tests\n');

(async () => {
  const okRes = {
    ok: true, status: 200,
    headers: { get: () => 'application/json' },
    json: async () => ({ posts: [42] }),
    text: async () => '',
  };

  const acA = new AbortController();
  const acB = new AbortController();
  const pA = API.get('/posts?page=2&x=1', { signal: acA.signal });
  const pB = API.get('/posts?page=2&x=1', { signal: acB.signal });

  check('дедуп: два одинаковых GET делят один fetch', fetchCalls === 1, 'fetchCalls=' + fetchCalls);
  check('дедуп: второй вызов получает тот же промис', pA === pB, 'pA !== pB');

  // первый вызывающий отменяет свой запрос — но сеть всё равно завершает
  // запрос, и данные должны дойти до второго вызывающего
  acA.abort();
  pendingFetches[0].resolve(okRes);
  const resB = await pB;
  check('F5: отмена запроса ОДНОГО вызывающего не должна ронять второго',
    resB && resB.posts, 'resB=' + JSON.stringify(resB) + ' (второй вызывающий потерял данные из-за чужого abort)');

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  process.exit(failed ? 1 : 0);
})();