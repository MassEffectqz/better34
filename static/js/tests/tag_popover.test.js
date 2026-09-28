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
  const src = await a.fetchTagSourcePreview('blue_hair', [], 6);
  check('источник: свой эндпоинт и обложки с сайта',
    /source-preview/.test(urls[1]) && src.site === 'rule34' && src.posts.length === 1, urls[1]);
  await a.fetchTagSourcePreview('blue_hair', [], 6);
  check('кэш источника: повторов нет', urls.filter((u) => /source-preview/.test(u)).length === 1,
    'calls=' + urls.length);
  check('сбой источника → null', (await a.fetchTagSourcePreview('broken', [], 6)) === null);

  // Добор до шести: клиент сообщает, сколько не хватает, и какие id уже
  // показаны — иначе бор вернёт те же посты и сетка распадётся на дубли.
  a._tagSourceCache.clear();
  await a.fetchTagSourcePreview('extra', [11, 12], 4);
  const topup = urls[urls.length - 1];
  check('добор: в запросе need и exclude',
    /need=4/.test(topup) && /exclude=11%2C12/.test(topup), topup);
  // need входит в ключ кэша: ответ на «добавь одну» нельзя переиспользовать
  // как ответ на «добавь шесть».
  a._tagSourceCache.clear();
  await a.fetchTagSourcePreview('extra', [], 6);
  check('кэш не путает разные need',
    urls[urls.length - 1] !== topup && /need=6/.test(urls[urls.length - 1]),
    urls[urls.length - 1]);

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
  check('обложка без inline-пропорций (квадратная ячейка)',
    !/aspect-ratio/.test(body), body.slice(0, 240));
  check('смежный тег кликабелен', /data-suggest="long_hair"/.test(body) && /90/.test(body), body.slice(0, 320));
  // Чипы не должны носить data-tag: иначе они снова становятся целями наведения
  // для document-слушателя, и подсказка начнёт подменять себя под курсором.
  check('чипы не помечены как цели наведения', !/data-tag=/.test(body), body.slice(0, 320));

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
  check('источник: сетка помечена как нескачанная', /tag-pop-cover-src/.test(fromSrc), fromSrc.slice(0, 200));
  check('источник: локальная пустота не показывается',
    !/нет скачанных|No downloaded/i.test(fromSrc), fromSrc.slice(0, 200));

  // ГЛАВНОЕ: смешанная сетка. Одна локальная обложка не должна отменять
  // остальные пять — раньше при непустой локальной части бор вообще не
  // спрашивался, и подсказка показывала 1–2 картинки вместо шести.
  {
    const mixed = a.renderTagPopoverBody({
      tag: 'mostly_undownloaded', count: 3,
      posts: [{ id: 11, thumb: '/api/thumb/11', width: 800, height: 600 }],
      related: [],
      source: {
        site: 'rule34', count: 5,
        posts: [21, 22, 23, 24, 25].map(id => ({
          id, thumb: '/api/proxy?url=' + id + '&kind=preview', width: 800, height: 600, site: 'rule34',
        })),
      },
    });
    const covers = (mixed.match(/data-post="/g) || []).length;
    check('сетка добрана до шести обложек', covers === 6, 'обложек: ' + covers);
    check('боровые обложки помечены поштучно, а не сетка целиком',
      (mixed.match(/tag-pop-cover-src/g) || []).length === 5,
      'помечено: ' + (mixed.match(/tag-pop-cover-src/g) || []).length);
    check('подпись про бор есть, раз в сетке есть боровые',
      /не скачано|not downloaded/i.test(mixed), mixed.slice(0, 200));
  }

  // Локальных уже шесть — бор не нужен вовсе, и подписи про бор быть не должно.
  {
    const full = a.renderTagPopoverBody({
      tag: 'full', count: 6,
      posts: [1, 2, 3, 4, 5, 6].map(id => ({ id, thumb: '/api/thumb/' + id, width: 8, height: 8 })),
      related: [], source: { site: 'rule34', count: 2, posts: [{ id: 7, thumb: '/x.jpg' }] },
    });
    check('шести локальных хватает — лишние боровые отброшены',
      (full.match(/data-post="/g) || []).length === 6,
      'обложек: ' + (full.match(/data-post="/g) || []).length);
    check('подпись про бор не показывается зря',
      !/не скачано|not downloaded/i.test(full), full.slice(0, 200));
  }

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
  // Третья перерисовка — это добор с бору: одной локальной обложке не хватает
  // до шести, поэтому подсказка идёт на источник. Раньше при непустой локальной
  // части бор не спрашивался вовсе, и на экране оставалась одна картинка.
  check('позиция пересчитана после данных и после добора с бору', places === 3, 'places=' + places);
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

  // Второй слой защиты: даже если элемент внутри всплывашки каким-то образом
  // нёс data-tag, наведение на него не должно ничего перерисовывать. Первый
  // слой — чипы вообще не носят data-tag (см. проверку разметки выше).
  const handlers = {};
  document.addEventListener = (ev, fn) => { (handlers[ev] = handlers[ev] || []).push(fn); };
  a.bindTagPopover();
  const chip = {
    isConnected: true,
    dataset: { tag: 'smile' },
    closest: (sel) => (sel === '.tag-popover' ? {} : sel === '[data-tag]' ? chip : null),
  };
  const shown = [];
  a.showTagPopover = (tag, anchor) => { shown.push([tag, anchor]); };
  handlers.mouseover[0]({ target: chip });
  await new Promise((r) => setTimeout(r, 260));
  check('наведение на чип ничего не перерисовывает', shown.length === 0, JSON.stringify(shown.map((s) => s[0])));

  // Клик по чипу ведёт к поиску — по data-suggest, а не по data-tag.
  const suggested = [];
  a.applySuggestion = (t) => suggested.push(t);
  let opened = 0;
  a.openPostById = () => { opened++; };
  a.onTagPopoverClick({ target: { closest: (s) => (s === '.tag-pop-related' ? { dataset: { suggest: 'smile' } } : null) }, preventDefault() {} });
  check('клик по чипу применяет подсказку', suggested[0] === 'smile', JSON.stringify({ suggested, opened }));
  a.onTagPopoverClick({ target: { closest: (s) => (s === '.tag-pop-cover' ? { dataset: { post: '7' } } : null) }, preventDefault() {} });
  check('клик по обложке открывает пост', opened === 1, JSON.stringify({ suggested, opened }));

  // Обычный тег на странице — наоборот, цель наведения и якорь.
  const pageTag = { isConnected: true, dataset: { tag: 'long_hair' }, closest: (s) => (s === '.tag-popover' ? null : s === '[data-tag]' ? pageTag : null) };
  handlers.mouseover[0]({ target: pageTag });
  await new Promise((r) => setTimeout(r, 260));
  check('тег на странице — цель и якорь', shown.length === 1 && shown[0][0] === 'long_hair' && shown[0][1] === pageTag,
    JSON.stringify(shown.map((s) => s[0])));

  // Главное: подсказка не должна прятаться, пока курсор в ней. Проверяем
  // решение по elementFromPoint, а не по воле событий.
  let under = null;
  document.elementFromPoint = () => under;
  a._tagPop = pop;
  const anchorChild = { closest: () => null };
  a._tagPopAnchor = { contains: (n) => n === anchorChild };
  a._tagPointer = { x: 10, y: 10 };

  const inPopChild = { closest: (s) => (s === '.tag-popover' ? {} : null) };
  under = inPopChild;
  check('курсор внутри всплывашки → не прячем', a._tagPointerOverTagUI() === true);
  under = { closest: () => null };
  check('курсор снаружи → прячем', a._tagPointerOverTagUI() === false);
  under = anchorChild;
  check('курсор на самом теге → не прячем', a._tagPointerOverTagUI() === true);

  let hidden = 0;
  a.hideTagPopover = () => { hidden++; };
  under = inPopChild;
  a.scheduleHideTagPopover();
  await new Promise((r) => setTimeout(r, 300));
  check('отложенное скрытие не срабатывает под курсором', hidden === 0, 'hidden=' + hidden);
  under = { closest: () => null };
  a.scheduleHideTagPopover();
  await new Promise((r) => setTimeout(r, 300));
  check('отложенное скрытие срабатывает, когда курсор ушёл', hidden === 1, 'hidden=' + hidden);
  delete a._tagPointer;
  check('без данных о курсоре не считаем, что он в зоне', a._tagPointerOverTagUI() === false);

  // Дальше нужны настоящие методы: выше они подменялись заглушками.
  delete a.fetchTagPreview;
  delete a.fetchTagSourcePreview;
  delete a.hideTagPopover;

  // Регресс: после Esc/клика подсказка обязана снова появляться на том же
  // теге. Пока активный тег не сбрасывался, повторное наведение считалось
  // «уже показанным» и подсказка просто не открывалась.
  a._tagPopTag = 'blue_hair';
  a._tagPopAnchor = { isConnected: true };
  a.hideTagPopover();
  check('скрытие сбрасывает активный тег', a._tagPopTag === null, String(a._tagPopTag));
  check('скрытие сбрасывает якорь', a._tagPopAnchor === null);
  let popped = 0;
  a.showTagPopover = () => { popped++; };
  handlers.mouseover[0]({ target: pageTag });
  await new Promise((r) => setTimeout(r, 260));
  check('после скрытия подсказка снова открывается', popped === 1, 'shown=' + popped);

  // И отложенное ПОКАЗЫВАНИЕ не должно выстреливать после закрытия.
  a.showTagPopover = function (tag, anchor) { shown.push([tag, anchor]); };
  const shownBeforeEsc = shown.length;
  handlers.mouseover[0]({ target: pageTag });
  a.hideTagPopover();
  await new Promise((r) => setTimeout(r, 300));
  check('Esc/клик отменяет и отложенное открытие', shown.length === shownBeforeEsc,
    'shown=' + shown.length);

  // Кэш обязан учитывать фильтр рейтинга: включил SFW — нельзя отдать обложки,
  // отобранные по старому правилу.
  a._tagCache = new Map();
  const before = urls.length;
  await a.fetchTagPreview('blue_hair');
  a.state.ratingFilter = 'sfw';
  await a.fetchTagPreview('blue_hair');
  check('смена фильтра рейтинга перезапрашивает', urls.length === before + 2,
    'added=' + (urls.length - before));
  check('rating уходит в URL', /rating=sfw/.test(urls[urls.length - 1]), urls[urls.length - 1]);
  a.state.ratingFilter = '';

  // Клик по обложке не имеет права заменять ленту одним постом: открываем
  // штатным openViewerByPostId, он лишь достраивает пост в конец выдачи.
  // Снимаем заглушку-счётчик выше, иначе она проглотит вызов.
  delete a.openPostById;
  const feed = [{ id: 1 }, { id: 2 }];
  a.state = { posts: feed.slice(), page: 5, ratingFilter: '' };
  let delegated = 0;
  a.openViewerByPostId = (id) => { delegated = id; return true; };
  a.openPostById(77);
  check('обложка открывается штатным помощником', delegated === 77, String(delegated));
  check('лента не тронута', a.state.posts.length === 2 && a.state.page === 5,
    JSON.stringify(a.state.posts));

  // Клик где угодно вне всплывашки закрывает её: на таче нет mouseout, иначе
  // подсказка залипала бы до перезагрузки.
  const outside = { closest: (s) => (s === '.tag-popover' ? null : outside) };
  const inside = { closest: (s) => (s === '.tag-popover' ? inside : null) };
  a.hideTagPopover = function () { this._tagPopAnchor = null; };
  handlers.click[0]({ target: outside });
  check('клик вне подсказки закрывает её', a._tagPopAnchor === null);
  a._tagPopAnchor = { isConnected: true };
  handlers.click[0]({ target: inside });
  check('клик по самой подсказке не рвёт обработку', a._tagPopAnchor !== null);
  delete a.hideTagPopover;

  // Числовые формы: «1 постов» — ошибка, должно быть «1 пост».
  check('форма 1 пост', /1 пост/.test(a.renderTagPopoverBody({ tag: 'x', count: 1, posts: [], related: [] })),
    a.renderTagPopoverBody({ tag: 'x', count: 1, posts: [], related: [] }).slice(0, 80));
  check('форма 2 поста', /2 поста/.test(a.renderTagPopoverBody({ tag: 'x', count: 2, posts: [], related: [] })));
  check('форма 5 постов', /5 постов/.test(a.renderTagPopoverBody({ tag: 'x', count: 5, posts: [], related: [] })));
  check('форма 21 пост', /21 пост/.test(a.renderTagPopoverBody({ tag: 'x', count: 21, posts: [], related: [] })));
  check('форма 112 постов', /112 постов/.test(a.renderTagPopoverBody({ tag: 'x', count: 112, posts: [], related: [] })));

  // Счётчик соседей экранируется, как и всё остальное из ответа сервера.
  const evilCount = a.renderTagPopoverBody({
    tag: 'x', count: 1, posts: [], related: [{ tag: 'smile', count: '"><b>1' }],
  });
  check('XSS: счётчик соседей экранирован', !evilCount.includes('<b>1'), evilCount.slice(0, 240));

  console.log(`\nИтог: ${passed} ok, ${failed} fail`);
  if (failed) process.exit(1);
})();
