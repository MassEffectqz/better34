// offline_library.test.js — библиотека офлайн: сбор id постов, пачками по
// 4 миниатюры, остановка, отказ при отсутствии сети и очистка кэша.
import { App } from '../state.js';
import '../offline_library.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {} });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
defG('navigator', { onLine: true, storage: undefined });
defG('document', {
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  body: { appendChild() {} },
  addEventListener() {},
  createElement: () => ({
    set textContent(v) { this._t = v; }, get textContent() { return this._t || ''; },
    set className(v) { this._c = v; }, get className() { return this._c || ''; },
    setAttribute() {}, addEventListener() {},
    classList: { add() {}, remove() {}, contains: () => false, toggle() {} },
  }),
});

console.log('Библиотека офлайн\n');

const setOnline = (v) => defG('navigator', { onLine: v, storage: undefined });

(async () => {
  const a = Object.create(App);
  a._offlineLib = { running: false, cancel: false, done: 0, total: 0 };

  // 1) Страницы /api/local собираются до конца, id — в порядке поступления.
  const pages = [
    { posts: [{ id: 1 }, { id: 2 }] },
    { posts: [{ id: 3 }] },
    { posts: [] },
  ];
  let pageNo = 0;
  const requested = [];
  a.API = { get: (u) => { requested.push(u); const d = pages[Math.min(pageNo, 2)]; pageNo++; return Promise.resolve(d); } };

  const fetched = [];
  defG('fetch', (u) => { fetched.push(u); return Promise.resolve({ ok: true }); });
  defG('caches', { open: () => Promise.reject(new Error('no cache in node')), delete: () => Promise.resolve(true) });

  const prog = [];
  const res = await a.prefetchOfflineLibrary((p) => prog.push(p));
  check('собраны id всех постов', res.total === 3 && res.done === 3, JSON.stringify(res));
  check('страницы запрашиваются по limit=100',
    /limit=100/.test(requested[0]) && /page=1/.test(requested[0]) && /page=2/.test(requested[1]), requested.join(' | '));
  check('миниатюры по /api/thumb/<id>',
    fetched.join(',') === '/api/thumb/1,/api/thumb/2,/api/thumb/3', fetched.join(','));
  check('прогресс приходит', prog.length > 0 && prog[prog.length - 1].done === 3, JSON.stringify(prog));

  // 2) Повторный запуск, пока идёт первый, не стартует второй.
  a._offlineLib = { running: true, cancel: false, done: 0, total: 0 };
  const skipped = await a.prefetchOfflineLibrary();
  check('параллельный запуск пропущен', skipped.skipped === true, JSON.stringify(skipped));

  // 3) Офлайн: не начинаем и не лезем в сеть.
  a._offlineLib = { running: false, cancel: false, done: 0, total: 0 };
  setOnline(false);
  const off = await a.prefetchOfflineLibrary();
  check('офлайн: понятная ошибка без запросов', off.error === 'offline', JSON.stringify(off));
  setOnline(true);

  // 4) Отмена останавливает цикл.
  a._offlineLib = { running: false, cancel: false, done: 0, total: 0 };
  let page = 0;
  a.API = { get: () => { page++; return Promise.resolve({ posts: Array.from({ length: 100 }, (_, i) => ({ id: page * 1000 + i })) }); } };
  const cancelP = a.prefetchOfflineLibrary();
  a.cancelPrefetchOfflineLibrary();
  const cancelled = await cancelP;
  check('отмена помечена в результате', cancelled.cancelled === true, JSON.stringify(cancelled));

  // 5) Очистка кэша обращается к нужным именам.
  const deleted = [];
  defG('caches', { delete: (n) => { deleted.push(n); return Promise.resolve(true); } });
  const cleared = await a.clearOfflineCache();
  check('очистка кэша', cleared === true && deleted.includes('briefly-api-v1'), deleted.join(','));
  // В приватном режиме кэша нет — это не ошибка, а false.
  defG('caches', { delete: () => Promise.reject(new Error('nope')) });
  check('очистка без кэша → false', (await a.clearOfflineCache()) === false);

  console.log(`\nИтог: ${passed} ok, ${failed} fail`);
  if (failed) process.exit(1);
})();
