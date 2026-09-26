// collections_ui.test.js — вкладка «Коллекции»: перестановка, ZIP-экспорт,
// обложки в ответе списка.
import { App } from '../state.js';
import { API } from '../api.js';
import '../collections.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {} });
defG('localStorage', {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
});

// Ссылки для скачивания: собираем href'ы, по клику ничего не делаем.
const links = [];
defG('document', {
  body: { appendChild(c) { links.push(c); return c; } },
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({ click() { this._clicked = true; }, remove() { this._removed = true; } }),
  addEventListener() {}, removeEventListener() {},
  documentElement: { lang: 'ru' },
});

// Blob-URL для JSON-экспорта: подменяем, чтобы не тащить файловую систему.
const blobURLs = [];
defG('URL', {
  createObjectURL(blob) { const u = 'blob:fake/' + blobURLs.length; blobURLs.push(u); return u; },
  revokeObjectURL(u) { this._revoked = (this._revoked || []).concat(u); },
});

let posted = [];
let failPosts = false;
API.post = async function (url, body) {
  posted.push([url, body]);
  if (failPosts) throw new Error('offline');
  return {};
};
API.invalidate = function () {};

function makeApp(collections) {
  const a = Object.create(App);
  a.state = { profile: { collections } };
  a.renders = 0;
  // Реальный renderCollections требует богатого DOM — здесь важна логика
  // перестановки, поэтому считаем вызовы.
  a.renderCollections = function () { a.renders++; };
  a.toasts = [];
  a.showToast = function (msg, kind) { a.toasts.push([msg, kind]); };
  return a;
}

const cols = () => [
  { id: 'a', name: 'A', count: 1, ids: [1], covers: [1] },
  { id: 'b', name: 'B', count: 2, ids: [2, 3], covers: [2, 3] },
  { id: 'c', name: 'C', count: 0, ids: [], covers: [] },
];

console.log('Коллекции: перестановка и ZIP\n');

// ── 1. Перестановка ───────────────────────────────────────────────────────
{
  const a = makeApp(cols());
  await a.moveCollection({ id: 'b' }, -1);
  check('порядок в профиле изменён оптимистично',
    a.state.profile.collections.map(c => c.id).join(',') === 'b,a,c',
    a.state.profile.collections.map(c => c.id).join(','));
  check('на сервер ушёл полный новый порядок',
    posted.length === 1 && posted[0][0] === '/collections/reorder' && posted[0][1].ids.join(',') === 'b,a,c',
    JSON.stringify(posted));
  check('список перерисован', a.renders >= 1, String(a.renders));
}
{
  // Границы: сдвиг за пределы списка ничего не делает и не шлёт запрос.
  const a = makeApp(cols());
  posted = [];
  await a.moveCollection({ id: 'a' }, -1);
  await a.moveCollection({ id: 'c' }, 1);
  check('сдвиг за края списка игнорируется', posted.length === 0 && a.renders === 0, JSON.stringify(posted));
}
{
  // Ошибка запроса — откат к прежнему порядку.
  const a = makeApp(cols());
  posted = [];
  failPosts = true;
  await a.moveCollection({ id: 'a' }, 1);
  failPosts = false;
  check('при ошибке порядок возвращается',
    a.state.profile.collections.map(c => c.id).join(',') === 'a,b,c',
    a.state.profile.collections.map(c => c.id).join(','));
  check('при ошибке показан тост с ошибкой', a.toasts.some(([, kind]) => kind === 'error'), JSON.stringify(a.toasts));
}

// ── 2. ZIP одной коллекции ────────────────────────────────────────────────
{
  links.length = 0;
  const a = makeApp(cols());
  a.downloadCollectionZip({ id: 'b', name: 'B', ids: [2, 3] });
  check('href архива содержит id коллекции',
    links.length === 1 && links[0].href === '/api/download-zip?ids=2,3', links[0] && links[0].href);
  check('ссылка на архив кликнута и убрана', links[0]._clicked === true && links[0]._removed === true);
  check('показан тост о числе постов', a.toasts.some(([m]) => m === 'В очередь: 2'), JSON.stringify(a.toasts));
}
{
  links.length = 0;
  const a = makeApp(cols());
  a.downloadCollectionZip({ id: 'c', name: 'C', ids: [] });
  check('пустая коллекция: архив не качается, тост об ошибке',
    links.length === 0 && a.toasts.some(([, kind]) => kind === 'error'), JSON.stringify(a.toasts));
}

// ── 3. ZIP по всем коллекциям: дедупликация и кап ─────────────────────────
{
  links.length = 0;
  const a = makeApp([
    { id: 'a', name: 'A', ids: [1, 2] },
    { id: 'b', name: 'B', ids: [2, 3] },
  ]);
  a.downloadAllCollectionsZip();
  check('id из разных коллекций дедуплицируются',
    links.length === 1 && links[0].href === '/api/download-zip?ids=1,2,3', links[0] && links[0].href);
}
{
  links.length = 0;
  const many = [];
  for (let i = 1; i <= 2500; i++) many.push({ id: 'c' + i, name: 'c' + i, ids: [i] });
  const a = makeApp(many);
  a.downloadAllCollectionsZip();
  const n = links[links.length - 1].href.split('=')[1].split(',').length;
  check('id в архиве ограничены 2000 (кап /api/download-zip)', n === 2000, String(n));
}
{
  links.length = 0;
  const a = makeApp([{ id: 'a', name: 'A', ids: [] }]);
  a.downloadAllCollectionsZip();
  check('без постов архив не качается', links.length === 0 && a.toasts.some(([, k]) => k === 'error'));
}

// ── 4. JSON-экспорт состава коллекции ────────────────────────────────────
{
  links.length = 0;
  const a = makeApp(cols());
  a.exportCollectionJson({ id: 'b', name: 'Моя/коллекция', count: 2, ids: [2, 3] });
  check('экспорт создаёт blob-ссылку с расширением .json',
    links.length === 1 && String(links[0].href).startsWith('blob:') && links[0].download === 'Моя_коллекция.json',
    links[0] && (links[0].href + ' | ' + links[0].download));
  check('ссылка на JSON кликнута и убрана', links[0]._clicked === true && links[0]._removed === true);
  check('blob-URL освобождён', (URL._revoked || []).length === blobURLs.length, JSON.stringify(URL._revoked));
  check('показан тост об экспорте', a.toasts.some(([m]) => m === 'Коллекция «Моя/коллекция» экспортирована'), JSON.stringify(a.toasts));
}
{
  links.length = 0;
  const a = makeApp(cols());
  a.exportCollectionJson({ id: 'c', name: 'C', ids: [] });
  check('пустая коллекция не экспортируется', links.length === 0 && a.toasts.some(([, k]) => k === 'error'));
}

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);