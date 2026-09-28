// Регрессии, найденные вручную: пресет печатал «NaN сравнений» (оператор «-»
// склеивался со строками), запрос уходил на /api/api/… → 404, и иконка кубка
// отсутствовала в наборе — пункт меню рендерился без глифа.

// icon() строит DOM через document.createElement — нужен минимальный мок,
// как в остальных тестах проекта.
function makeClassList() {
  const s = new Set();
  return {
    contains: (c) => s.has(c),
    add: (c) => s.add(c),
    remove: (c) => s.delete(c),
    toggle(c, f) { if (f == null) { if (s.has(c)) { s.delete(c); return false; } s.add(c); return true; } if (f) s.add(c); else s.delete(c); return !!f; },
  };
}
globalThis.document = {
  createElement: () => ({
    classList: makeClassList(), setAttribute() {}, appendChild() {},
    innerHTML: '', textContent: '', dataset: {},
  }),
  querySelector: () => null, querySelectorAll: () => [],
  getElementById: () => null, addEventListener() {}, body: { appendChild() {} },
};

// Статический импорт — обязателен: файл с одним лишь await import() TypeScript
// считает глобальным скриптом, и его `const App` начинает конфликтовать с
// глобальным объявлением App из types/app.d.ts (TS2451).
import { App } from '../state.js';
import { icon } from '../utils.js';
import { API } from '../api.js';
await import('../tournament.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// size участников -> посты с возрастающей оценкой буры (как в tournament.test.js).
function posts(n) {
  return Array.from({ length: n }, (_, i) => ({
    id: i + 1, score: (i + 1) * 10, tags: 'a b', thumb: '/t.jpg',
    file_url: '/f.jpg', file_type: 'jpg', width: 1, height: 1,
    rating: 's', downloaded: true, source: 'test',
  }));
}

console.log('Регрессии турнира\n');

// ── 1. Иконка кубка есть в наборе иконок ─────────────────────────────────
{
  const svg = icon('trophy', 16);
  check('иконка trophy отдаётся', typeof svg === 'string' && svg.length > 0);
  check('в иконке есть содержимое (не пустая обёртка)', /<path|<circle/.test(String(svg)));
}

// ── 2. Пресеты считают сравнения, а не печатают NaN ────────────────────
{
  // querySelector отдаёт null: это «разметка без разобранных элементов», и
  // проверяем мы только собранный HTML, а не работу обработчиков.
  const root = { innerHTML: '', querySelectorAll: () => [], querySelector: () => null };
  const a = Object.create(App);
  a.els = { tournamentRoot: root };
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  a.renderTournamentSetup();
  const html = root.innerHTML;
  check('нет NaN в разметке пресетов', !/NaN/.test(html),
    html.match(/.{0,60}NaN.{0,40}/) ? html.match(/.{0,60}NaN.{0,40}/)[0] : '');
  // 8 участников → 7 сравнений; 4 → 3; 16 → 15; 32 → 31.
  for (const n of [3, 7, 15, 31]) {
    check(`для сетки отображается «${n} сравнений»`, html.includes('>' + n + ' '));
  }
}

// ── 3. Запрос идёт без двойного /api ───────────────────────────────────
{
  let hitUrl = null;
  const origGet = API.get;
  API.get = async (u) => { hitUrl = u; return { rounds: 3, posts: [] }; };
  const a = Object.create(App);
  a.els = { tournamentRoot: { innerHTML: '', classList: { add() {}, remove() {} }, querySelectorAll: () => [], querySelector: () => null } };
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  a.state = { query: 'male' };
  a.showToast = () => {};
  a.invalidateFeedCache = () => {};
  await a.startTournament();
  API.get = origGet;
  check('URL не содержит /api/api', !!hitUrl && !hitUrl.includes('/api/api'), String(hitUrl));
  check('URL начинается с /tournament', !!hitUrl && hitUrl.startsWith('/tournament?'), String(hitUrl));
  check('раунды и источник переданы', !!hitUrl && /rounds=3/.test(hitUrl) && /source=offline/.test(hitUrl), String(hitUrl));
  check('текущий поисковый запрос передан', !!hitUrl && /tags=male/.test(hitUrl), String(hitUrl));
}

// Дизайн-контракт: оверлей турнира обязан держать визуальный язык страницы
// авторизации (стеклянная карточка, градиентный заголовок, плавающие чипсы) и
// показывать сетку. Без этого теста «прокачанный дизайн» тихо откатится
// обратно на серые прямоугольники.
{
  const root = { innerHTML: '', querySelectorAll: () => [], querySelector: () => null };
  const a = Object.create(App);
  a.els = { tournamentRoot: root };
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  a.renderTournamentSetup();
  const html = root.innerHTML;
  // .tr-root — это сам контейнер из index.html, а не класс внутри разметки,
  // поэтому проверяем чипсы и карточку, а не имя корневого оверлея.
  check('плавающие чипсы на фоне', html.includes('tr-chips') && /tr-chip c1/.test(html));
  check('стеклянная карточка вместо серой панели', html.includes('tr-card') && !html.includes('tr-panel'));
  check('градиентный заголовок', html.includes('tr-title'));
  check('надпись-бренд над заголовком', html.includes('tr-topline'));
  check('у кнопок свой стиль, а не btn-primary', html.includes('tr-btn') && !html.includes('btn-primary'));

  // Сетка: с 8 участниками дерево содержит 4+2+1 узлов.
  a._seedTournament(Array.from({ length: 8 }, (_, i) => ({
    id: i + 1, score: (i + 1) * 10, tags: 'a b', thumb: '/t.jpg', file_url: '/f.jpg',
    file_type: 'jpg', width: 1, height: 1, rating: 's', downloaded: true, source: 'test',
  })));
  const tree = a._trTree();
  const nodes = (tree.match(/tr-node/g) || []).length;
  check('дерево сетки содержит 7 узлов (4+2+1)', nodes === 7, 'узлов=' + nodes);
  check('текущий матч помечен', tree.includes('tr-node now'));
  // Экран матча: он держит дерево сетки и разделитель VS между картинками.
  a.els.tournamentRoot.classList = { add() {}, remove() {} };
  a.renderTournamentMatch(a._currentMatch());
  const mhtml = a.els.tournamentRoot.innerHTML;
  check('на матче рисуется дерево сетки', mhtml.includes('tr-tree'));
  check('между картинками стоит VS', mhtml.includes('tr-vs') && mhtml.includes('VS'));
  check('на матче обе картинки в разметке',
    (mhtml.match(/tr-side/g) || []).length === 2, 'сторон=' + (mhtml.match(/tr-side/g) || []).length);
  check('на матче есть прогресс-бар', mhtml.includes('tr-bar-fill'));
}

// ── 4. Анимационный слой: перемычки сетки и баннер раунда ────────────────
// «Вода» дизайна тоже имеет контракт: перемычек ровно столько, сколько узлов
// на следующих уровнях, и они стоят в поясах своих узлов. Без этого сетка
// снова становится россыпью точек без связей.
{
  const root = { innerHTML: '', classList: { add() {}, remove() {} }, querySelectorAll: () => [], querySelector: () => null };
  const a = Object.create(App);
  a.els = { tournamentRoot: root };
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  a._seedTournament(posts(8));
  const tree = a._trTree();
  const wires = (tree.match(/tr-wire/g) || []).length;
  check('перемычек столько же, сколько узлов уровней 2..N (2+1)', wires === 3, 'перемычек=' + wires);
  // Ячейка первого уровня склеивает узлы 0 и 1: их центры — 12.5% и 37.5%.
  check('ячейка перемычки стоит в поясе своих узлов',
    tree.includes('top:12.500%;height:25.000%'), (tree.match(/top:[\d.]+%;height:[\d.]+%/) || [''])[0]);
  check('высота сцены сетки выведена в переменную', /--tr-tree-h:\d+px/.test(tree),
    (tree.match(/--tr-tree-h:\d+px/) || [''])[0]);

  // Баннер раунда показывается один раз на переходе, а не на каждой отрисовке.
  a.renderTournamentMatch(a._currentMatch());
  check('баннер раунда виден при первом показе раунда',
    a.els.tournamentRoot.innerHTML.includes('tr-round-flash'));
  a.renderTournamentMatch(a._currentMatch());
  check('баннер раунда не повторяется на том же раунде',
    !a.els.tournamentRoot.innerHTML.includes('tr-round-flash'));

  // Первый круг доигран целиком: пара 0 сыграна, узел второго круга стал
  // текущим — значит перемычка к нему «живая», а свежий узел получил вспышку.
  a._resolveMatch(a._currentMatch(), 0);
  a._resolveMatch(a._currentMatch(), 2);
  a._resolveMatch(a._currentMatch(), 4);
  a._resolveMatch(a._currentMatch(), 6);
  const next = a._trTree();
  // Состояния — это классы, и у одной перемычки их сразу три (full fresh live);
  // поэтому сверяем токены целиком, а не подстрокой вида «tr-wire live».
  const cls = (h, prefix) => [...h.matchAll(new RegExp('class="(' + prefix + '[^"]*)"', 'g'))]
    .map((x) => x[1].split(' '));
  const has = (lists, name) => lists.some((c) => c.includes(name));
  const wireCls = cls(next, 'tr-wire');
  check('перемычка к играемому сейчас узлу — живая', has(wireCls, 'live'), wireCls.join(' | '));
  check('сыгранная пара светится перемычкой как полная', has(wireCls, 'full'), wireCls.join(' | '));
  check('узел, куда ушёл победитель, помечен вспышкой', has(cls(next, 'tr-node'), 'fresh'));
}

// ── 5. Гонка двух касаний и режим «уменьшить движение» ───────────────────
{
  const mkOptions = () => ({ innerHTML: '', classList: { add() {}, remove() {} }, querySelectorAll: () => [], querySelector: () => null });
  const mkApp = () => {
    const a = Object.create(App);
    a.els = { tournamentRoot: mkOptions() };
    a._tournament = {
      open: true, loading: false, posts: null, bracket: [], rounds: 2,
      source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
    };
    a._seedTournament(posts(4));
    return a;
  };

  // Второй тап приходит, пока идёт анимация выбора: матч не засчитывается.
  const busy = mkApp();
  busy._tournament.busy = true;
  busy._trPick(0);
  check('тап во время анимации выбора игнорируется',
    busy._tournament.stats.played === 0, 'played=' + busy._tournament.stats.played);

  // «Уменьшить движение»: результат применяется сразу, без таймера ожидания.
  globalThis.window = { matchMedia: () => ({ matches: true }) };
  const fast = mkApp();
  fast._trPick(0);
  check('при отключённых анимациях выбор применяется сразу',
    fast._tournament.stats.played === 1 && fast._tournament.busy === false,
    'played=' + fast._tournament.stats.played);

  // Устаревшая разметка: карточка из другого матча не должна засчитаться.
  const stale = mkApp();
  stale._trPick(2);
  check('индекс чужой карточки игнорируется',
    stale._tournament.stats.played === 0, 'played=' + stale._tournament.stats.played);

  // Хоткеи: 1/2 и ←/→ выбирают сторону, Esc закрывает игру, Enter запускает
  // турнир на стартовом экране. Enter по кнопке перехватывать нельзя — иначе
  // на финале не работала бы кнопка «Открыть пост».
  let started = 0;
  const press = (app, key, tag) => app.tournamentKey({
    key, target: { tagName: tag || 'DIV' }, preventDefault() {},
  });
  const keys = mkApp();
  keys.state = { tournamentOpen: true };
  keys.closeTournament = () => { keys._tournament.open = false; keys.state.tournamentOpen = false; };
  keys.startTournament = () => { started++; };
  press(keys, '1');
  check('клавиша 1 выбирает левую картинку',
    keys._tournament.stats.played === 1, 'played=' + keys._tournament.stats.played);
  press(keys, 'ArrowRight');
  check('клавиша → выбирает правую картинку следующего матча',
    keys._tournament.stats.played === 2, 'played=' + keys._tournament.stats.played);
  press(keys, 'Enter', 'BUTTON');
  check('Enter по кнопке не запускает новый турнир', started === 0, 'запусков=' + started);
  press(keys, 'Escape');
  check('Esc закрывает турнир',
    keys.state.tournamentOpen === false && keys._tournament.open === false);

  const setup = Object.create(App);
  setup.els = { tournamentRoot: mkOptions() };
  setup._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  setup.startTournament = () => { started++; };
  press(setup, 'Enter');
  check('Enter на стартовом экране запускает турнир', started === 1, 'запусков=' + started);
}

// ── 6. Финал: лучи, корона, искры и набегающая оценка ────────────────────
{
  const root = { innerHTML: '', querySelectorAll: () => [], querySelector: () => null };
  const a = Object.create(App);
  a.els = { tournamentRoot: root };
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 2,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  a._seedTournament(posts(4));
  let m = a._currentMatch();
  while (m) { a._resolveMatch(m, m.a); m = a._currentMatch(); }
  // Ссылки участников тянет сам финал — подменяем API, иначе тест пошёл бы в сеть.
  const origGet = API.get;
  API.get = async () => ({ posts: [] });
  a.renderTournamentFinal();
  API.get = origGet;
  const html = root.innerHTML;
  check('лучи вокруг чемпиона вынесены из карточки',
    html.includes('tr-final-wrap') && html.includes('tr-rays'));
  check('корона финала рисуется иконкой кубка', html.includes('tr-crown') && /<svg/.test(html));
  const sparks = (html.match(/tr-spark/g) || []).length;
  check('искры идут волной (не меньше 10)', sparks >= 10, 'искр=' + sparks);
  check('оценка буры лежит в элементе счётчика и начинается с нуля',
    html.includes('data-tr="score">0</b>'));
}

// ── 7. Финал: галерея всех участников и «Открыть пост» без двойного прокси ─
// Симптом, который здесь закреплён: вьювер уходил за постом на
// /api/proxy?url=/api/proxy?url=… и крутил спиннер бесконечно. Причина —
// клиент отдавал вьюверу миниатюру сетки, уже завёрнутую сервером в /api/proxy.
{
  const RAW = {
    id: 1, downloaded: false, file_url: 'https://r/1.jpg', preview_url: 'https://r/p1.jpg',
    sample_url: 'https://r/s1.jpg', file_type: 'jpg', tags: 'a b', width: 1, height: 1,
    score: 10, rating: 's', source: 'r',
  };
  let hitUrl = null;
  let hitOpts = null;
  const origGet = API.get;
  API.get = async (u, o) => {
    hitUrl = u; hitOpts = o;
    return { posts: [
      RAW,
      { id: 2, downloaded: true, file_url: '', preview_url: '', file_type: 'jpg', tags: 'c' },
    ], missing: [3, 4] };
  };

  const mkFinal = () => {
    const a = Object.create(App);
    a.els = { tournamentRoot: { innerHTML: '', querySelector: () => null, querySelectorAll: () => [] } };
    a._tournament = {
      open: true, loading: false, posts: null, bracket: [], rounds: 2, source: 'online',
      resolved: new Set(), winner: null, stats: { played: 0, wins: 0 }, links: null, linksLoading: false,
    };
    a._seedTournament(posts(4));
    let m = a._currentMatch();
    while (m) { a._resolveMatch(m, m.a); m = a._currentMatch(); }
    return a;
  };

  const a = mkFinal();
  await a._trFetchLinks();

  check('посты участников запрашиваются одним списком id',
    !!hitUrl && hitUrl.startsWith('/tournament/posts?') &&
      /ids=1,2,3,4/.test(decodeURIComponent(hitUrl)) && /source=online/.test(hitUrl),
    String(hitUrl));
  check('запрос постов участников идёт без двойного /api',
    !!hitUrl && !hitUrl.includes('/api/api'), String(hitUrl));
  check('кэш обходится (fresh), иначе галерея показала бы вчерашние ссылки',
    !!hitOpts && hitOpts.fresh === true);
  check('повторный вызов не шлёт второй запрос', await (async () => {
    hitUrl = null;
    const orig = API.get;
    API.get = async (u) => { hitUrl = u; return { posts: [] }; };
    const same = await a._trFetchLinks();
    API.get = orig;
    return hitUrl === null && same === undefined;
  })());

  // Сырой адрес источника заворачивается в прокси ровно один раз, локальный
  // скачанный пост — берётся из библиотеки.
  check('сырой preview_url заворачивается в /api/proxy один раз',
    a._trThumbOf(RAW) === '/api/proxy?url=' + encodeURIComponent('https://r/p1.jpg') + '&kind=preview',
    String(a._trThumbOf(RAW)));
  check('скачанный участник берёт локальную миниатюру',
    a._trThumbOf({ id: 2, downloaded: true }) === '/api/thumb/2');
  check('уже проксированный адрес не заворачивается второй раз',
    a._trThumbOf({ id: 5, downloaded: false, preview_url: '/api/proxy?url=https%3A%2F%2Fr%2Fp.jpg' }) ===
      '/api/proxy?url=https%3A%2F%2Fr%2Fp.jpg');
  check('без preview_url миниатюра берётся из sample_url',
    a._trThumbOf({ id: 6, downloaded: false, sample_url: 'https://r/s6.jpg' }) ===
      '/api/proxy?url=' + encodeURIComponent('https://r/s6.jpg') + '&kind=preview');

  // Галерея: плитка на каждого участника, удалённые — заглушкой, чемпион помечен.
  const tiles = a._trGalleryTiles();
  check('плиток столько же, сколько участников',
    (tiles.match(/class="tr-gal/g) || []).length === 4);
  check('чемпион помечен рамкой', tiles.includes('tr-gal me'));
  check('открываемые плитки — кнопки, удалённые посты — заглушки',
    (tiles.match(/<button/g) || []).length === 2 && (tiles.match(/tr-gal off/g) || []).length === 2);
  check('заглушка удалённого поста без картинки и без перехода',
    !/tr-gal off[^>]*><img/.test(tiles));

  // «Открыть пост»: вьюверу уходит СЫРОЙ пост, а не миниатюра сетки.
  const opened = [];
  const toasts = [];
  const app = mkFinal();
  app.state = { posts: [] };
  app.closeTournament = () => { app._tournament.open = false; };
  app.openViewer = (i) => { opened.push(i); };
  app.showToast = (msg, kind) => { toasts.push([msg, kind]); };
  await app._trFetchLinks();
  await app._trOpenPost(0);
  check('вьювер получает индекс 0', opened.length === 1 && opened[0] === 0);
  check('вьюверу отдан сырой файл источника, без /api/proxy',
    app.state.posts.length === 1 && app.state.posts[0].file_url === 'https://r/1.jpg',
    JSON.stringify(app.state.posts[0]));
  check('sample_url и file_size доехали до вьювера (тяжёлые картинки)',
    app.state.posts[0].sample_url === 'https://r/s1.jpg' && app.state.posts[0].downloaded === false);
  // Пост, которого больше нет на источнике: вьювер НЕ открываем — иначе он
  // остался бы со спиннером, а игрок не понял бы, что случилось.
  app.state.posts = [];
  app._tournament.open = true;
  await app._trOpenPost(2);
  check('удалённый пост не открывается во вьювере', opened.length === 1 && app.state.posts.length === 0);
  check('об удалённом посте игрок получает сообщение',
    toasts.length >= 1 && toasts[toasts.length - 1][1] === 'error');
  API.get = origGet;
}


// ── 8. Фильтр рейтинга турнира: All / SFW / 18+ ───────────────────────────
// Значения те же, что у переключателя в шапке (rating=), поэтому включённый
// в ленте SFW не должен вдруг получить турнир с explicit-постами.
{
  const mkApp = (rating) => {
    const a = Object.create(App);
    a.els = {
      tournamentRoot: {
        innerHTML: '', classList: { add() {}, remove() {} },
        querySelectorAll: () => [], querySelector: () => null, focus() {},
      },
    };
    a._tournament = {
      open: true, loading: false, posts: null, bracket: [], rounds: 3,
      source: 'online', rating, resolved: new Set(), winner: null,
      stats: { played: 0, wins: 0 }, links: null, linksLoading: false,
    };
    a.state = { query: '', tournamentOpen: true };
    a.showToast = () => {};
    a.invalidateFeedCache = () => {};
    a.closeViewer = () => {};
    return a;
  };
  const urlFor = async (rating) => {
    let hit = null;
    const origGet = API.get;
    API.get = async (u) => { hit = u; return { rounds: 3, posts: [] }; };
    const a = mkApp(rating);
    await a.startTournament();
    API.get = origGet;
    return hit;
  };

  check('SFW уходит в запрос', /rating=sfw/.test(await urlFor('sfw') || ''), String(await urlFor('sfw')));
  check('18+ уходит в запрос как rating=nsfw', /rating=nsfw/.test(await urlFor('nsfw') || ''));
  // «Все» — это отсутствие фильтра, а не rating= в URL: иначе два одинаковых
  // турнира давали бы разные ссылки и разные записи в кэше API.
  check('без фильтра rating в URL не едет', !/rating=/.test(await urlFor('') || ''), String(await urlFor('')));

  // Разметка стартового экрана: три кнопки, активная помечена.
  const ui = mkApp('sfw');
  ui.renderTournamentSetup();
  const html = ui.els.tournamentRoot.innerHTML;
  check('три режима рейтинга: All / SFW / 18+',
    /data-rating=""[^>]*>All</.test(html) && /data-rating="sfw"[^>]*>SFW</.test(html) && /data-rating="nsfw"[^>]*>18\+</.test(html),
    html.match(/data-rating="[^"]*"[^>]*>[^<]*/g) ? html.match(/data-rating="[^"]*"[^>]*>[^<]*/g).join(' | ') : '');
  check('выбранный SFW помечен как включённый',
    /class="tr-src on" data-rating="sfw" aria-pressed="true"/.test(html));
  check('невыбранные режимы помечены aria-pressed="false"',
    (html.match(/aria-pressed="false"/g) || []).length >= 2);

  // При открытии турнир подхватывает фильтр ленты — иначе игра шла бы вопреки
  // настройке, которую пользователь выставил в шапке.
  const g = mkApp('');
  g.state.ratingFilter = 'nsfw';
  g.renderTournament = () => {};
  g.openTournament();
  check('при открытии турнир берёт фильтр рейтинга ленты',
    g._tournament.rating === 'nsfw', String(g._tournament.rating));

  // Нехватка постов под фильтром объясняется фильтром, а не библиотекой:
  // «в библиотеке только 0» после выбора SFW в онлайне — бессмыслица, ведь
  // библиотека тут ни при чём, посты берутся с источника.
  const toastFor = async (rating, body) => {
    let msg = '';
    const origGet = API.get;
    API.get = async () => body;
    const a = mkApp(rating);
    a.showToast = (m) => { msg = m; };
    await a.startTournament();
    API.get = origGet;
    return msg;
  };
  const tooFew = { error: 'tournament_not_enough_posts', available: 0, size: 8 };
  check('при фильтре причина нехватки — фильтр, а не библиотека',
    /SFW/.test(await toastFor('sfw', tooFew) || ''), String(await toastFor('sfw', tooFew)));
  check('«18+» подписывается как 18+, а не как nsfw',
    /18\+/.test(await toastFor('nsfw', tooFew) || ''), String(await toastFor('nsfw', tooFew)));
  check('без фильтра остаётся прежнее сообщение про библиотеку',
    !/SFW|18\+/.test(await toastFor('', tooFew) || ''), String(await toastFor('', tooFew)));
}


console.log(`\nИтог: ${passed} ok, ${failed} fail`);
if (failed) process.exit(1);
