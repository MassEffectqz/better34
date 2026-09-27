// tag_popover.test.js — всплывашка превью тега: локальный запрос за счётчиком
// и обложками, кэш без повторных походов, содержимое, деградация без данных.
import { App } from '../state.js';
import '../tag_popover.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {}, innerWidth: 1200, innerHeight: 800 });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
defG('document', {
  querySelector: () => null,
  querySelectorAll: () => [],
  body: { appendChild() {} },
  addEventListener() {},
  // esc() кладёт текст в textContent и читает innerHTML: браузер возвращает
  // экранированную строку. Заглушка повторяет именно это, иначе проверки
  // XSS проходили бы вхолостую.
  createElement: () => ({
    set textContent(v) { this._t = String(v); },
    get textContent() { return this._t || ''; },
    set innerHTML(v) { this._t = String(v); },
    get innerHTML() {
      return String(this._t || '')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    },
    setAttribute() {},
    getAttribute: () => null,
    addEventListener() {},
    classList: { add() {}, remove() {}, contains: () => false, toggle() {} },
    style: {},
  }),
  documentElement: { clientWidth: 1200, clientHeight: 800 },
});

console.log('Всплывашка тега: превью и смежные\n');

const a = Object.create(App);
a._tagCache = new Map();
a._tagPop = null;
const urls = [];
a.API = { get: (u) => {
  urls.push(u);
  const tag = decodeURIComponent(/preview\?/.test(u) ? /\/tags\/([^/]+)\//.exec(u)[1] : '');
  if (tag === 'broken') return Promise.reject(new Error('offline'));
  return Promise.resolve({
    tag,
    count: 128,
    posts: [
      { id: 11, thumb: '/api/thumb/11', width: 800, height: 600 },
      { id: 12, thumb: '/api/thumb/12', width: 600, height: 800 },
    ],
    related: [{ tag: 'long_hair', count: 90 }, { tag: 'smile', count: 40 }],
  });
} };

(async () => {
  let d = await a.fetchTagPreview('blue_hair');
  check('счётчик и обложки получены', d.count === 128 && d.posts.length === 2, JSON.stringify(d && d.count));
  check('URL содержит тег, limit и related',
    /tags\/blue_hair\/preview/.test(urls[0]) && /limit=6/.test(urls[0]) && /related=8/.test(urls[0]), urls[0]);

  // Повторный запрос того же тега — из кэша, без похода в сервер.
  await a.fetchTagPreview('blue_hair');
  await a.fetchTagPreview('BLUE_HAIR');
  check('кэш: регистр не важен, повторов нет', urls.length === 1, 'calls=' + urls.length);

  // Сбой сети не должен ломать наведение — просто null.
  d = await a.fetchTagPreview('broken');
  check('ошибка сети → null, без исключения', d === null);

  // Содержимое всплывашки.
  const body = a.renderTagPopoverBody({
    tag: 'blue_hair', count: 128,
    posts: [{ id: 11, thumb: '/api/thumb/11', width: 800, height: 600 }],
    related: [{ tag: 'long_hair', count: 90 }],
  });
  check('заголовок со счётчиком', /blue_hair/.test(body) && /128/.test(body), body.slice(0, 120));
  check('обложка кликабельна и несёт id', /data-post="11"/.test(body), body.slice(0, 240));
  check('пропорции обложки', /aspect-ratio:800\/600/.test(body), body.slice(0, 240));
  check('смежный тег кликабелен', /data-tag="long_hair"/.test(body) && /90/.test(body), body.slice(0, 320));

  // Пустые данные: подсказка вместо пустой сетки, без «undefined».
  const empty = a.renderTagPopoverBody({ tag: 'nothing', count: 0, posts: [], related: [] });
  check('пусто: понятный текст, нет мусора',
    /нет скачанных|No downloaded/i.test(empty) && !/undefined/.test(empty), empty.slice(0, 160));

  // XSS: значения с сервера экранируются.
  const evil = a.renderTagPopoverBody({
    tag: '<img src=x onerror=alert(1)>', count: 1,
    posts: [], related: [{ tag: '"><script>alert(2)</script>', count: 1 }],
  });
  check('XSS: тег экранирован', !evil.includes('<img') && evil.includes('&lt;img'), evil.slice(0, 160));
  check('XSS: смежный тег экранирован', !evil.includes('<script>'), evil.slice(0, 260));

  // showTagPopover без подключённого anchor ничего не делает.
  a.showTagPopover('x', null);
  check('без anchor не падаем', true);

  console.log(`\nИтог: ${passed} ok, ${failed} fail`);
  if (failed) process.exit(1);
})();
