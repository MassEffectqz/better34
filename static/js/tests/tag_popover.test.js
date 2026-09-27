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
a._tagSourceCache = new Map();
a._tagPop = null;
const urls = [];
a.API = { get: (u) => {
  urls.push(u);
  const isSrc = /source-preview\?/.test(u);
  const tag = decodeURIComponent(/preview\?/.test(u) ? /\/tags\/([^/]+)\//.exec(u)[1] : '');
  if (tag === 'broken') return Promise.reject(new Error('offline'));
  if (isSrc) {
    return Promise.resolve({
      site: 'rule34', query: tag, count: 1,
      posts: [{ id: 91, thumb: '/api/proxy?url=x&kind=preview', width: 100, height: 100, site: 'rule34' }],
    });
  }
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
  // Быстрый запрос не должен ходить на бор: иначе наведение на тег без
  // скачанных постов висело бы на сетевом поиске.
  check('быстрый запрос не трогает источник', !/source-preview/.test(urls[0]), urls[0]);

  // Превью с бору — отдельный запрос и отдельный кэш.
  const src = await a.fetchTagSourcePreview('blue_hair');
  check('источник: свой эндпоинт и обложки с сайта',
    /source-preview/.test(urls[1]) && src.site === 'rule34' && src.posts.length === 1, urls[1]);
  await a.fetchTagSourcePreview('BLUE_HAIR');
  check('кэш источника: повторов нет', urls.filter((u) => /source-preview/.test(u)).length === 1,
    'calls=' + urls.length);
  check('сбой источника → null', (await a.fetchTagSourcePreview('broken')) === null);

  // Повторный запрос того же тега — из кэша, без похода в сервер. Считаем
  // именно быстрые запросы: источник проверяется отдельно.
  const localCalls = () => urls.filter((u) => !/source-preview/.test(u)).length;
  await a.fetchTagPreview('blue_hair');
  await a.fetchTagPreview('BLUE_HAIR');
  check('кэш: регистр не важен, повторов нет', localCalls() === 1, 'calls=' + localCalls());

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

  // Пусто в библиотеке, но бор что-то нашёл: показываем превью с источника.
  const fromSrc = a.renderTagPopoverBody({
    tag: 'cloudy_sky', count: 0, posts: [], related: [],
    source: {
      site: 'rule34', query: 'cloudy_sky', count: 2,
      posts: [
        { id: 101, thumb: '/api/proxy?url=https%3A%2F%2Fi%2F101.jpg&kind=preview', width: 800, height: 600, site: 'rule34' },
        { id: 102, thumb: '/api/proxy?url=https%3A%2F%2Fi%2F102.jpg&kind=preview', width: 600, height: 800, site: 'rule34' },
      ],
    },
  });
  check('источник: видно, с какого сайта и что не скачано',
    /rule34/.test(fromSrc) && /не скачано|not downloaded/i.test(fromSrc), fromSrc.slice(0, 200));
  check('источник: обложки кликабельны и несут id',
    /data-post="101"/.test(fromSrc) && /data-post="102"/.test(fromSrc), fromSrc.slice(0, 300));
  check('источник: сетка помечена как нескачанная', /tag-pop-covers-src/.test(fromSrc), fromSrc.slice(0, 200));
  check('источник: локальная пустота не показывается',
    !/нет скачанных|No downloaded/i.test(fromSrc), fromSrc.slice(0, 200));

  // Бор ответил, но по тегу у него тоже ничего нет — говорим про это прямо.
  const srcNone = a.renderTagPopoverBody({
    tag: 'rare_tag', count: 0, posts: [], related: [], source: { site: 'rule34', count: 0, posts: [] },
  });
  check('источник пуст: сообщаем про бор, а не про библиотеку',
    /rule34/.test(srcNone) && !/нет скачанных/i.test(srcNone), srcNone.slice(0, 200));

  // Имя сайта приходит с сервера — экранируем.
  const srcEvil = a.renderTagPopoverBody({
    tag: 'x', count: 0, posts: [], related: [],
    source: { site: '"><script>alert(3)</script>', count: 1, posts: [{ id: 1, thumb: '/x.jpg' }] },
  });
  check('XSS: сайт экранирован', !srcEvil.includes('<script>'), srcEvil.slice(0, 200));

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

  // Позиция пересчитывается ПОСЛЕ прихода данных: заглушка «тег…» занимает
  // ~30px, реальное содержимое — в разы выше, и без пересчёта всплывашка
  // уезжает за нижний край экрана.
  const pop = {
    _html: '',
    style: {},
    classList: { add() {}, remove() {}, contains: () => false, toggle() {} },
    isConnected: true,
    set innerHTML(v) { this._html = String(v); },
    get innerHTML() { return this._html; },
    getBoundingClientRect: () => ({ left: 0, top: 0, right: 0, bottom: 0, width: 300, height: 300 }),
  };
  a._tagPop = pop;
  let places = 0;
  a._placeTagPopover = function () { places++; };
  const anchor = { isConnected: true, dataset: { tag: 'blue_hair' } };
  a.fetchTagPreview = function () {
    return Promise.resolve({ tag: 'blue_hair', count: 9, posts: [{ id: 1, thumb: '/api/thumb/1' }], related: [] });
  };
  a.showTagPopover('blue_hair', anchor);
  check('позиция посчитана сразу (по заглушке)', places === 1, 'places=' + places);
  await new Promise((r) => setTimeout(r, 0));
  check('позиция пересчитана после прихода данных', places === 2, 'places=' + places);
  check('всплывашка наполнена содержимым', /data-post="1"/.test(pop.innerHTML), pop.innerHTML.slice(0, 120));

  // Два этапа: в библиотеке пусто → показываем «ищу», потом доклеиваем блок с
  // бору. Раньше всё ехало одним запросом, и подсказка висела пустой.
  let srcResolve = null;
  a.fetchTagPreview = function () {
    return Promise.resolve({ tag: 'lonely', count: 0, posts: [], related: [] });
  };
  a.fetchTagSourcePreview = function () {
    return new Promise((r) => { srcResolve = r; });
  };
  a.showTagPopover('lonely', anchor);
  await new Promise((r) => setTimeout(r, 0));
  check('пусто в библиотеке: видно, что идёт поиск',
    /Ищу|Searching/i.test(pop.innerHTML), pop.innerHTML.slice(0, 160));
  check('пусто в библиотеке: сетки обложек нет', !/data-post=/.test(pop.innerHTML), pop.innerHTML.slice(0, 160));
  srcResolve({ site: 'rule34', posts: [{ id: 91, thumb: '/api/proxy?url=x&kind=preview', width: 1, height: 1, site: 'rule34' }] });
  await new Promise((r) => setTimeout(r, 0));
  check('ответ с бору доклеен в ту же всплывашку',
    /data-post="91"/.test(pop.innerHTML) && /rule34/.test(pop.innerHTML), pop.innerHTML.slice(0, 200));
  check('надпись «ищу» исчезла', !/Ищу|Searching/i.test(pop.innerHTML), pop.innerHTML.slice(0, 200));

  // Упавший быстрый запрос: показываем пустоту, а не вечное «tag…».
  a.fetchTagPreview = function () { return Promise.resolve(null); };
  a.fetchTagSourcePreview = function () { return Promise.resolve(null); };
  a.showTagPopover('dead', anchor);
  await new Promise((r) => setTimeout(r, 0));
  check('упавший запрос: понятная пустота вместо вечного «tag…»',
    !/tag…/.test(pop.innerHTML) && /нет скачанных|No downloaded/i.test(pop.innerHTML),
    pop.innerHTML.slice(0, 200));

  // Служебная пометка не должна попасть в общий кэш: он общий для всех
  // последующих показов того же тега.
  const cached = { tag: 'cached', count: 0, posts: [], related: [] };
  a.fetchTagPreview = function () { return Promise.resolve(cached); };
  a.fetchTagSourcePreview = function () { return Promise.resolve(null); };
  a.showTagPopover('cached', anchor);
  await new Promise((r) => setTimeout(r, 0));
  check('кэш не заражается служебной пометкой', cached.__searching === undefined, String(cached.__searching));

  console.log(`\nИтог: ${passed} ok, ${failed} fail`);
  if (failed) process.exit(1);
})();
