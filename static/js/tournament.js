// tournament.js — мини-игра «выбери лучшее»: турнирная сетка на выбывание.
//
// Пользователь выбирает пресет раундов (2..5, то есть 4..32 участника) и
// источник (оффлайн-библиотека или онлайн). Сервер отдаёт участников и пустую
// сетку, дальше всё считается на клиенте: состояние турнира на сервере хранить
// незачем, а значит игра не требует ни авторизации, ни базы.
//
// Правило отображения: score от буры виден ТОЛЬКО на финале. Во время матча
// показываем картинки и теги — иначе пользователь выбирал бы по цифре, а не глазами.

import { App } from './state.js';
import { API } from './api.js';
import { t } from './i18n.js';
import { esc, icon } from './utils.js';

const ROUNDS_PRESETS = [2, 3, 4, 5];

App._tournament = {
  open: false,
  loading: false,
  /** @type {object[] | null} участники сетки (TournamentPost с сервера) */ posts: null,
  /** @type {number[][][]} уровни сетки; уровень 0 — первый круг */ bracket: [],
  rounds: 3,
  source: 'offline',
  /**
   * Фильтр тегов турнира: строка в синтаксисе поиска (пробелы, '-' и '|').
   * Раньше он молча брался из поисковой строки, и пустая выдача «Из библиотеки»
   * объяснялась пользователю «нет постов», хотя виноват был забытый запрос.
   * Теперь фильтр виден на стартовом экране; значение подставляет openTournament.
   * @type {string}
   */
  tags: '',
  /**
   * Фильтр рейтинга турнира: '' (все), 'sfw', 'nsfw' («18+») — те же значения,
   * что у переключателя в шапке, чтобы словари рейтингов и ожидания пользователя
   * совпадали. Значения по умолчанию нет: открытие турнира подставляет глобальный
   * фильтр ленты (openTournament), иначе игра была бы вопреки настройке сайта.
   * @type {string}
   */
  rating: '',
  /** @type {Set<string>} ключи «уровень:пара» уже сыгранных матчей */ resolved: new Set(),
  winner: /** @type {number | null} */ (null),
  /** @type {string | null} ключ последнего сыгранного матча — для вспышек сетки */
  lastResolved: null,
  /** @type {number} уровень, баннер которого уже показан (анимация перехода) */
  seenRound: -1,
  /** @type {boolean} идёт анимация выбора — новые клики игнорируются */
  busy: false,
  stats: { played: 0, wins: 0 },
  /**
   * Посты участников из /api/tournament/posts, ключ — id поста. Именно эти
   * записи уходят во вьювер: сервер отдаёт сырые адреса источника (или
   * локальный файл для скачанного поста), и вьювер проксирует их сам.
   * В карточках сетки лежит уже завёрнутая миниатюра, повторно её
   * проксировать нельзя — /api/proxy?url=/api/proxy?url=… висит вечно.
   * @type {Record<number, object> | null}
   */
  links: null,
  /** @type {boolean} запрос ссылок участников уже идёт — второй раз не шлём */
  linksLoading: false,
};

App.openTournament = function () {
  this.closeViewer();
  this._tournament.open = true;
  this._tournament.posts = null;
  this._tournament.bracket = [];
  this._tournament.winner = null;
  this._tournament.resolved = new Set();
  this._tournament.lastResolved = null;
  this._tournament.seenRound = -1;
  this._tournament.busy = false;
  this._tournament.stats = { played: 0, wins: 0 };
  this._tournament.links = null;
  this._tournament.linksLoading = false;
  // Фильтр рейтинга по умолчанию — тот же, что выбран для ленты в шапке:
  // отдельная настройка в игре молча разошлась бы с настройкой сайта.
  this._tournament.rating = (this.state && this.state.ratingFilter) || '';
  // Теги — из поисковой строки на момент открытия: турнир обычно запускают по
  // тому, что человек ищет сейчас. Поле видно на стартовом экране и стирается
  // одной кнопкой, поэтому случайный фильтр больше не выглядит как «нет постов».
  this._tournament.tags = (this.state && this.state.query) || '';
  this.state.tournamentOpen = true;
  this.renderTournament();
  const root = this.els.tournamentRoot;
  if (root) root.focus();
};

App.closeTournament = function () {
  this._tournament.open = false;
  this._tournament.loading = false;
  this.state.tournamentOpen = false;
  const root = this.els.tournamentRoot;
  if (root) { root.innerHTML = ''; root.classList.add('hidden'); }
  this.invalidateFeedCache();
};

// Ближайший матч, который можно разыграть: уровень 0 засеян участниками,
// поэтому играем по порядку уровней и пар. Матч готов, когда оба его слота
// заняты — либо участниками (первый круг), либо победителями снизу.
//
// Проверять ТОЛЬКО уровень 0 нельзя: после первого круга сетка ещё не пуста,
// и турнир объявился бы завершённым, не разыграв второй круг.
App._currentMatch = function () {
  const tm = this._tournament;
  for (let r = 0; r < tm.bracket.length; r++) {
    for (let i = 0; i < tm.bracket[r].length; i++) {
      const [a, b] = tm.bracket[r][i];
      if (a >= 0 && b >= 0 && !tm.resolved.has(r + ':' + i)) {
        return { round: r, pair: i, a, b };
      }
    }
  }
  return null;
};

// Победитель матча поднимается на уровень выше. На последнем уровне он и есть
// чемпион турнира.
App._resolveMatch = function (match, winnerIdx) {
  const tm = this._tournament;
  const key = match.round + ':' + match.pair;
  // Идемпотентность: двойной клик по уже сыгранному матчу (гонка касаний на
  // телефоне) не должен ни засчитать матч дважды, ни подвигать победителя
  // в другую ветку сетки.
  if (tm.resolved.has(key)) return false;
  tm.resolved.add(key);
  // Ключ последнего матча: по нему сетка понимает, какую ветку подсветить
  // вспышкой «победитель ушёл наверх».
  tm.lastResolved = key;
  tm.stats.played++;
  // Сравниваем с оценкой буры: «угадал» — значит выбранное оказалось лучше
  // второго и по мнению сообщества. Это и есть результат, ради которого игра.
  const loserIdx = winnerIdx === match.a ? match.b : match.a;
  if (tm.posts[winnerIdx].score >= tm.posts[loserIdx].score) tm.stats.wins++;

  tm.bracket[match.round][match.pair] = [winnerIdx, winnerIdx];
  const up = match.round + 1;
  if (up < tm.bracket.length) {
    tm.bracket[up][Math.floor(match.pair / 2)][match.pair % 2] = winnerIdx;
  } else {
    tm.winner = winnerIdx;
  }
  return true;
};

// Раскладывает участников по первому кругу и сбрасывает состояние матчей.
App._seedTournament = function (posts) {
  const tm = this._tournament;
  tm.posts = posts;
  tm.winner = null;
  tm.resolved = new Set();
  tm.lastResolved = null;
  tm.seenRound = -1;
  tm.busy = false;
  // Счётчик матчей обнуляем ЗДЕСЬ, а не в openTournament: кнопка «Ещё турнир»
  // на финальном экране зовёт _seedTournament напрямую, и без сброса новый
  // турнир продолжил бы статистику предыдущего (на первом же матче «2 / 7»).
  tm.stats = { played: 0, wins: 0 };
  const rounds = tm.rounds;
  tm.bracket = [];
  for (let r = 0; r < rounds; r++) {
    const pairs = 1 << (rounds - r - 1);
    const level = [];
    for (let i = 0; i < pairs; i++) level.push([-1, -1]);
    tm.bracket.push(level);
  }
  for (let i = 0; i < posts.length; i++) {
    tm.bracket[0][Math.floor(i / 2)][i % 2] = i;
  }
};

// Плавающие чипсы на фоне — приём со страницы авторизации. Слова намеренно
// нейтральные: теги участников выдавали бы картинки заранее.
const TR_CHIP_WORDS = ['1 vs 1', 'SCORE', 'раунд', '?', 'победа', 'ФИНАЛ', 'best', 'сравни'];

/** @this {AppType} */
App._trChips = function () {
  let out = '<div class="tr-chips">';
  for (let i = 0; i < TR_CHIP_WORDS.length; i++) {
    out += '<span class="tr-chip c' + (i + 1) + '">' + esc(TR_CHIP_WORDS[i]) + '</span>';
  }
  return out + '</div>';
};

// Дерево сетки: колонка на уровень, узел на пару. Показывает, сколько матчей
// уже сыграно и где игрок находится сейчас. Между колонками — перемычки: по
// ним видно, как победитель поднимается наверх.
App._trTree = function () {
  const tm = this._tournament;
  const cur = this._currentMatch();
  const rows = tm.bracket.length ? tm.bracket[0].length : 1;
  // Высота сцены считается отсюда: в первом круге бывает 2…16 узлов, и каждому
  // нужен свой пояс. Из этой высоты выведены проценты перемычек, поэтому линии
  // попадают в узлы при любом размере сетки, без замеров DOM.
  const pitch = rows > 8 ? 14 : rows > 4 ? 18 : 22;
  let out = '<div class="tr-tree' + (rows > 4 ? ' dense' : '') +
    '" style="--tr-tree-h:' + Math.max(88, rows * pitch) + 'px">';
  for (let r = 0; r < tm.bracket.length; r++) {
    out += '<div class="tr-col">';
    for (let i = 0; i < tm.bracket[r].length; i++) {
      const key = r + ':' + i;
      let cls = 'tr-node';
      if (tm.resolved.has(key)) cls += ' done';
      else if (cur && cur.round === r && cur.pair === i) cls += ' now';
      if (tm.lastResolved === key) cls += ' fresh';
      out += '<i class="' + cls + '" style="--tr-d:' + (r * 90 + 40) + 'ms"></i>';
    }
    out += '</div>';
    if (r + 1 < tm.bracket.length) out += this._trWires(r);
  }
  return out + '</div>';
};

// Перемычки между уровнями: на каждый узел следующего уровня одна ячейка —
// «скобка», где два луча сходятся в вертикаль, а из неё выходит луч к узлу.
// Геометрия не требует замеров: при justify-content:space-around центр i-го
// узла лежит ровно в (2i+1)/(2n) высоты, поэтому ячейка j (её дети — узлы 2j и
// 2j+1) занимает полосу от (4j+1)/(4M) до (4j+3)/(4M), где M — число узлов
// следующего уровня. Проценты считаем здесь и отдаём в CSS инлайном.
App._trWires = function (round) {
  const tm = this._tournament;
  const cells = tm.bracket[round].length / 2;
  const cur = this._currentMatch();
  let out = '<div class="tr-join">';
  for (let j = 0; j < cells; j++) {
    const top = ((4 * j + 1) / (4 * cells)) * 100;
    const height = 100 / (2 * cells);
    const kids = [round + ':' + (j * 2), round + ':' + (j * 2 + 1)];
    const lit = kids.filter((k) => tm.resolved.has(k)).length;
    let cls = 'tr-wire';
    if (lit === 2) cls += ' full';
    else if (lit === 1) cls += ' half';
    if (tm.lastResolved === kids[0] || tm.lastResolved === kids[1]) cls += ' fresh';
    if (cur && cur.round === round + 1 && cur.pair === j) cls += ' live';
    out += '<i class="' + cls + '" style="top:' + top.toFixed(3) + '%;height:' +
      height.toFixed(3) + '%;--tr-d:' + (round * 90 + 90) + 'ms"></i>';
  }
  return out + '</div>';
};

/**
 * Теги и пресеты, которые имеет смысл предложить в турнире: избранные теги
 * профиля и сохранённые запросы.
 *
 * Раньше фильтр приходилось набирать руками, и это был единственный способ его
 * задать — опечатка давала «постов нет», а правильный тег вспомнить трудно.
 * Теперь теги берутся из профиля: это ровно то, чем человек уже пользуется.
 *
 * @returns {{tags: string[], presets: object[]}}
 */
App._trTagSources = function () {
  const p = (this.state && this.state.profile) || {};
  const tags = [];
  const seen = new Set();
  // Порядок профиля сохраняем: он уже отсортирован по частоте использования.
  for (const tag of p.fav_tags || []) {
    const clean = String(tag || '').trim();
    if (!clean) continue;
    const key = clean.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    tags.push(clean);
  }
  // Пресеты — это запросы целиком, а не теги, поэтому в общий список они не
  // попадают: у них своя кнопка (применяет весь запрос разом).
  const presets = (p.presets || []).filter(pr => pr && (pr.kind || 'query') === 'query' && String(pr.query || '').trim());
  return { tags, presets };
};

// Тег уже стоит в фильтре? Проверяем без учёта «-»/«+»: чип означает «этот
// тег», а не «этот тег с минусом».
App._trFilterHasTag = function (tag) {
  const want = String(tag || '').trim().toLowerCase();
  if (!want) return false;
  return String(this._tournament.tags || '')
    .split('|')
    .some(group => group.split(/\s+/).some(tok => tok.replace(/^[-+]/, '').toLowerCase() === want));
};

/**
 * Переключает тег в фильтре: если он уже есть — убираем (вместе с его
 * «-tag»), если нет — добавляем. Так один и тот же чип и добавляет, и убирает,
 * и не нужно отдельной кнопки очистки.
 */
App._trToggleTag = function (tag) {
  const tm = this._tournament;
  const clean = String(tag || '').trim();
  if (!clean) return;
  const cur = String(tm.tags || '').trim();
  const want = clean.toLowerCase();
  const groups = cur ? cur.split('|').map(g => g.trim()).filter(Boolean) : [];
  let found = false;
  const kept = [];
  for (const g of groups) {
    const toks = g.split(/\s+/).filter(Boolean).filter(tok => {
      if (tok.replace(/^[-+]/, '').toLowerCase() === want) {
        found = true;
        return false;
      }
      return true;
    });
    if (toks.length) kept.push(toks.join(' '));
  }
  // Найден был — фильтр без него, не найден — прежний плюс тег.
  tm.tags = found ? kept.join(' | ') : (cur ? cur + ' ' + clean : clean);
  this.renderTournamentSetup();
  // Фокус возвращаем в поле: после клика по чипу пользователь почти всегда
  // продолжает печатать, и без этого каретка «убегает» на страницу.
  const input = this.els.tournamentRoot.querySelector('[data-tr="tags"]');
  if (input && input.focus) {
    input.focus();
    if (typeof input.setSelectionRange === 'function') {
      const n = input.value.length;
      input.setSelectionRange(n, n);
    }
  }
};

// Пресет подставляет запрос целиком; повторный клик по уже применённому
// снимает фильтр (как в ленте, где повторный клик по пресету его снимает).
App._trApplyPreset = function (id) {
  const tm = this._tournament;
  const pr = ((this.state && this.state.profile && this.state.profile.presets) || [])
    .find(x => x && x.id === id);
  if (!pr) return;
  const q = String(pr.query || '').trim();
  tm.tags = (q && tm.tags === q) ? '' : q;
  this.renderTournamentSetup();
};

// Ряд быстрого выбора под полем тегов. Рисуется, только когда есть что
// предложить: пустая подпись «избранных тегов нет» — шум.
//
// Источник — state.profile, его грузит вход (auth.js). Отдельного запроса здесь
// намеренно нет: открытие турнира не должно ходить в сеть (и падать на офлайне),
// а пустой профиль даёт просто пустой ряд.
App._trTagPicker = function () {
  const { tags, presets } = this._trTagSources();
  if (!tags.length && !presets.length) return '';
  let out = '<div class="tr-tags-pick">';
  if (tags.length) {
    out += '<div class="tr-tags-pick-label">' + esc(t('tr.tagPick')) + '</div>';
    out += '<div class="tr-tags-pick-row">';
    for (const tag of tags) {
      const on = this._trFilterHasTag(tag);
      out += '<button type="button" class="tr-tagpick' + (on ? ' on' : '') + '"' +
        ' data-tr-tag="' + esc(tag) + '" aria-pressed="' + (on ? 'true' : 'false') + '"' +
        ' title="' + esc(tag) + '">' + esc(tag) + '</button>';
    }
    out += '</div>';
  }
  if (presets.length) {
    out += '<div class="tr-tags-pick-label">' + esc(t('tr.presetPick')) + '</div>';
    out += '<div class="tr-tags-pick-row">';
    for (const pr of presets) {
      const on = String(pr.query || '').trim() === String(this._tournament.tags || '').trim();
      out += '<button type="button" class="tr-tagpick preset' + (on ? ' on' : '') + '"' +
        ' data-tr-preset="' + esc(pr.id) + '" aria-pressed="' + (on ? 'true' : 'false') + '"' +
        ' title="' + esc(pr.query || '') + '">' + esc(pr.name || pr.query) + '</button>';
    }
    out += '</div>';
  }
  return out + '</div>';
};

// Стартовый экран: пресет раундов, источник картинок и фильтр рейтинга.
App.renderTournamentSetup = function () {
  const root = this.els.tournamentRoot;
  const tm = this._tournament;
  const roundsBtns = ROUNDS_PRESETS.map((r) => {
    const size = 1 << r;
    // Скобки обязательны: без них `size - 1` склеивается с соседними строками
    // оператором «-» и на пресете печатается «NaN сравнений».
    const matches = size - 1;
    const on = tm.rounds === r;
    return '<button type="button" class="tr-preset' + (on ? ' on' : '') + '" data-rounds="' + r + '"' +
      ' aria-pressed="' + (on ? 'true' : 'false') + '">' +
      '<b>' + size + '</b><span>' + esc(t('tr.players')) + '</span>' +
      '<em>' + matches + ' ' + esc(t('tr.matches')) + '</em></button>';
  }).join('');

  // Ключи достаются литералами: i18n-чекер ищет по исходнику, и динамика
  // t(cond ? 'tr.a' : 'tr.b') молча выкинула бы их из RU/EN как неиспользуемые.
  const srcHint = tm.source === 'online' ? t('tr.onlineHint') : t('tr.offlineHint');
  // Переключатель рейтинга — тот же набор All/SFW/18+, что в шапке, и теми же
  // классами, что источник: словари рейтингов у боров общие, а переключатель в
  // шапке уже приучил пользователя к этому виду. Подписи не переводим (как и в
  // шапке) — «SFW» и «18+» говорят сами за себя в любой локали.
  const rating = tm.rating || '';
  const ratingBtn = (val, label) =>
    '<button type="button" class="tr-src' + (rating === val ? ' on' : '') + '" data-rating="' + val +
    '" aria-pressed="' + (rating === val) + '">' + label + '</button>';
  root.innerHTML = this._trChips() +
    '<div class="tr-card tr-champion">' +
      '<div class="tr-topline">' + esc(t('tr.brand')) + '</div>' +
      '<h2 class="tr-title">' + esc(t('tr.title')) + '</h2>' +
      '<p class="tr-sub">' + esc(t('tr.subtitle')) + '</p>' +
      '<div class="tr-section"><div class="tr-label">' + esc(t('tr.rounds')) + '</div>' +
        '<div class="tr-presets">' + roundsBtns + '</div></div>' +
      '<div class="tr-section"><div class="tr-label">' + esc(t('tr.source')) + '</div>' +
        '<div class="tr-sources">' +
          '<button type="button" class="tr-src' + (tm.source === 'offline' ? ' on' : '') + '" data-source="offline" aria-pressed="' + (tm.source === 'offline') + '">' + esc(t('tr.srcOffline')) + '</button>' +
          '<button type="button" class="tr-src' + (tm.source === 'online' ? ' on' : '') + '" data-source="online" aria-pressed="' + (tm.source === 'online') + '">' + esc(t('tr.srcOnline')) + '</button>' +
        '</div>' +
        '<p class="tr-hint">' + esc(srcHint) + '</p>' +
      '</div>' +
      // Теги. Пустое поле — вся библиотека (или свежая выдача источника).
      // Именно невидимый фильтр из поисковой строки давал «в библиотеке нет
      // постов», поэтому фильтр показан, правится и стирается кнопкой.
      '<div class="tr-section"><div class="tr-label">' + esc(t('tr.tags')) + '</div>' +
        '<div class="tr-tags">' +
          '<input type="text" class="tr-input" data-tr="tags" autocomplete="off" spellcheck="false"' +
            ' value="' + esc(tm.tags || '') + '"' +
            ' placeholder="' + esc(t('tr.tagsPh')) + '"' +
            ' aria-label="' + esc(t('tr.tags')) + '">' +
          (tm.tags ? '<button type="button" class="tr-clear" data-tr="tags-clear"' +
            ' title="' + esc(t('tr.tagsClear')) + '" aria-label="' + esc(t('tr.tagsClear')) + '">' +
            icon('x', 14) + '</button>' : '') +
        '</div>' +
        // Быстрый выбор из профиля: избранные теги и пресеты запросов.
        this._trTagPicker() +
      '</div>' +
      // Рейтинг. Для оффлайн-турнира он сужает библиотеку прямо в SQL, для
      // онлайна — метатеги в запросе плюс досчистка на сервере; «доступно» в
      // обоих случаях считается по отфильтрованному набору, и при нехватке
      // постов сервер сам предложит уменьшить сетку.
      '<div class="tr-section"><div class="tr-label">' + esc(t('rating.label')) + '</div>' +
        '<div class="tr-sources" role="group">' +
          ratingBtn('', 'All') + ratingBtn('sfw', 'SFW') + ratingBtn('nsfw', '18+') +
        '</div>' +
      '</div>' +
      '<div class="tr-actions">' +
        '<button type="button" class="tr-btn" data-tr="start">' + esc(t('tr.start')) + '</button>' +
        '<button type="button" class="tr-btn ghost" data-tr="close">' + esc(t('tr.cancel')) + '</button>' +
      '</div>' +
    '</div>';

  for (const b of root.querySelectorAll('[data-rounds]')) {
    b.addEventListener('click', () => {
      tm.rounds = parseInt(b.dataset.rounds, 10) || 3;
      this.renderTournamentSetup();
    });
  }
  for (const b of root.querySelectorAll('[data-source]')) {
    b.addEventListener('click', () => {
      tm.source = b.dataset.source === 'online' ? 'online' : 'offline';
      this.renderTournamentSetup();
    });
  }
  for (const b of root.querySelectorAll('[data-rating]')) {
    b.addEventListener('click', () => {
      // data-rating="" — это «все»: пустая строка и есть отсутствие фильтра,
      // ровно как её понимает сервер.
      tm.rating = b.dataset.rating || '';
      this.renderTournamentSetup();
    });
  }
  // Поле тегов. Значение пишем в состояние сразу, но разметку НЕ перерисовываем:
  // иначе на каждом символе терялся бы фокус и каретка прыгала бы в начало.
  const tagsInput = root.querySelector('[data-tr="tags"]');
  if (tagsInput) {
    tagsInput.addEventListener('input', () => { tm.tags = tagsInput.value; });
  }
  const tagsClear = root.querySelector('[data-tr="tags-clear"]');
  if (tagsClear) {
    tagsClear.addEventListener('click', () => {
      tm.tags = '';
      this.renderTournamentSetup();
    });
  }
  // Чипы из профиля. Делегирование по контейнеру, а не на каждый чип: набор
  // меняется при каждой перерисовке, и слушатели на старых кнопках остались бы
  // висеть в памяти (и на кнопке, которой уже нет в DOM).
  const picks = root.querySelector('.tr-tags-pick');
  if (picks) {
    picks.addEventListener('click', (ev) => {
      const tagBtn = ev.target.closest('[data-tr-tag]');
      if (tagBtn) { this._trToggleTag(tagBtn.dataset.trTag); return; }
      const presetBtn = ev.target.closest('[data-tr-preset]');
      if (presetBtn) this._trApplyPreset(presetBtn.dataset.trPreset);
    });
  }
  const start = root.querySelector('[data-tr="start"]');
  if (start) start.addEventListener('click', () => this.startTournament());
  const close = root.querySelector('[data-tr="close"]');
  if (close) close.addEventListener('click', () => this.closeTournament());
};

// Почему постов не хватило — по данным ответа, а не догадкой. Порядок важен:
// пустая библиотека объясняет всё остальное («доступно 0» бывает и при пустой
// библиотеке, и когда всё отсеяли теги), а в онлайне библиотека ни при чём —
// там виноват фильтр рейтинга или источник.
/** @this {AppType} */
App._trNotEnoughMsg = function (d) {
  const size = d.size, available = d.available;
  // Источник берём из ответа, а при его отсутствии — из состояния игры: ответ
  // авторитетнее, но и запрос без поля source (старый сервер, тесты) не должен
  // превращать онлайн-турнир в «библиотека пуста».
  const src = d.source || this._tournament.source;
  if (src !== 'online') {
    // liked/scored приходят без фильтров: по одному available «лайков нет»
    // не отличить от «всё лайкнутое без оценки буры».
    if (d.liked === 0) return t('tr.emptyLib');
    if (d.scored === 0) return t('tr.noScores', { liked: d.liked });
  }
  if (this._tournament.rating) {
    return t('tr.notEnoughRating', {
      rating: this._tournament.rating === 'nsfw' ? '18+' : 'SFW', available, size,
    });
  }
  return t('tr.notEnough', { available, size });
};

// Загрузка участников и засев сетки.
App.startTournament = async function () {
  const tm = this._tournament;
  tm.loading = true;
  this.renderTournament();
  const qs = new URLSearchParams({ rounds: String(tm.rounds), source: tm.source });
  // rating уходит только когда он выбран: пустой «все» не должен ездить в URL,
  // иначе отличать две разные ссылки одного и того же турнира невозможно.
  if (tm.rating) qs.set('rating', tm.rating);
  // Теги — из поля стартового экрана. tm.tags === '' — это осознанное «без
  // фильтра», поэтому на поисковую строку падаем только когда поля нет вовсе
  // (вызов мимо openTournament: deep link, тесты).
  const tags = (tm.tags == null ? (this.state.query || '') : tm.tags).trim();
  if (tags) qs.set('tags', tags);
  try {
    // Путь БЕЗ /api: префикс подставляет API._fetchGet сам, иначе уходит
    // /api/api/tournament → 404.
    const d = await API.get('/tournament?' + qs.toString(), { fresh: true });
    if (d.error === 'tournament_not_enough_posts') {
      this.showToast(this._trNotEnoughMsg(d), 'error');
      tm.loading = false;
      this.renderTournamentSetup();
      return;
    }
    if (!d.posts || !d.posts.length) {
      this.showToast(t('err.provider_unavailable'), 'error');
      tm.loading = false;
      this.renderTournamentSetup();
      return;
    }
    tm.rounds = d.rounds;
    this._seedTournament(d.posts);
  } catch (err) {
    this.showToast(String(err && err.message || err), 'error');
    tm.loading = false;
    this.renderTournamentSetup();
    return;
  }
  tm.loading = false;
  this.renderTournament();
};

/** @this {AppType} */
App.renderTournament = function () {
  const root = this.els.tournamentRoot;
  if (!root) return;
  const tm = this._tournament;
  if (!tm.open) { root.innerHTML = ''; root.classList.add('hidden'); return; }
  root.classList.remove('hidden');

  if (tm.loading) {
    root.innerHTML = '<div class="tr-center">' + esc(t('tr.loading')) + '</div>';
    return;
  }
  if (!tm.posts) { this.renderTournamentSetup(); return; }
  const match = this._currentMatch();
  if (!match) { this.renderTournamentFinal(); return; }
  this.renderTournamentMatch(match);
};

// Матч: две картинки рядом, оценка буры и теги НЕ показываются — выбирать надо
// глазами, иначе игра превращается в угадывание цифр.
App.renderTournamentMatch = function (match) {
  const root = this.els.tournamentRoot;
  const tm = this._tournament;
  const total = (1 << tm.rounds) - 1;
  const pct = Math.round((tm.stats.played / total) * 100);
  // Карточки входят со сдвигом: сначала левая, потом правая — иначе обе
  // вспыхивают одновременно и выбор читается как «одна кнопка».
  const card = (idx, side) => {
    const p = tm.posts[idx];
    return '<button type="button" class="tr-side" data-idx="' + idx +
      '" style="--tr-d:' + (side * 110 + 60) + 'ms">' +
      '<img class="tr-img" src="' + esc(p.thumb) + '" alt="" loading="eager" decoding="async">' +
      '<span class="tr-hint-key">' + esc(t('tr.pick')) + '</span>' +
    '</button>';
  };
  // Баннер раунда показываем только на переходе: tm.seenRound помнит уровень,
  // для которого он уже проигран, поэтому обычные перерисовки его не повторяют.
  const freshRound = match.round !== tm.seenRound;
  tm.seenRound = match.round;
  root.innerHTML = this._trChips() +
    '<div class="tr-stage">' +
      (freshRound
        ? '<div class="tr-round-flash"><span>' + esc(t('tr.round', { n: match.round + 1 })) + '</span></div>'
        : '') +
      '<div class="tr-meta">' +
        '<button type="button" class="tr-close" data-tr="close" aria-label="' + esc(t('tr.cancel')) + '">×</button>' +
        '<span>' + esc(t('tr.round', { n: match.round + 1 })) + '</span>' +
        '<span>' + (tm.stats.played + 1) + ' / ' + total + '</span>' +
      '</div>' +
      this._trTree() +
      '<div class="tr-match">' + card(match.a, 0) +
        '<div class="tr-vs"><i>VS</i><span>' + esc(t('tr.pick')) + '</span></div>' +
        card(match.b, 1) +
      '</div>' +
      '<div class="tr-bar"><div class="tr-bar-fill" style="width:' + pct + '%"></div></div>' +
      '<div class="tr-keys"><kbd>1</kbd><kbd>2</kbd><i>' + esc(t('tr.keys')) + '</i></div>' +
    '</div>';

  // Полоса прогресса «докатывается» до текущего значения: свежий узел стартует
  // с нуля, а CSS-переход доводит ширину — иначе счётчик прыгает без движения.
  const fill = root.querySelector('.tr-bar-fill');
  if (fill && typeof requestAnimationFrame === 'function') {
    fill.style.width = '0%';
    requestAnimationFrame(() => { fill.style.width = pct + '%'; });
  }
  for (const b of root.querySelectorAll('.tr-side')) {
    b.addEventListener('click', () => this._trPick(parseInt(b.dataset.idx, 10)));
  }
  const close = root.querySelector('[data-tr="close"]');
  if (close) close.addEventListener('click', () => this.closeTournament());
};

// Выбор карточки — единственная точка входа: её зовут и мышь, и клавиатура.
// Решение фиксируется сразу (гонка двух касаний не засчитала бы матч дважды),
// а перерисовка откладывается, чтобы карточки успели проиграть победу и
// проигрыш. Пока идёт анимация, tm.busy глушит новые клики — иначе второй тап
// попал бы уже в следующий матч, который игрок ещё не видел.
App._trPick = function (idx) {
  const tm = this._tournament;
  if (tm.busy || !(idx >= 0)) return;
  const cur = this._currentMatch();
  // Карточка обязана принадлежать текущему матчу: за время анимации состояние
  // могло уйти вперёд, и чужой индекс засчитал бы не тот выбор.
  if (!cur || (idx !== cur.a && idx !== cur.b)) return;
  tm.busy = true;
  this._resolveMatch(cur, idx);
  const root = this.els.tournamentRoot;
  if (root) {
    for (const c of root.querySelectorAll('.tr-side')) {
      c.classList.add(parseInt(c.dataset.idx, 10) === idx ? 'picked' : 'dropped');
    }
  }
  const done = () => { tm.busy = false; this.renderTournament(); };
  if (this._trMotionOff()) { done(); return; }
  setTimeout(done, 460);
};

// Клавиши турнира: игра занимает весь экран, поэтому её хоткеи работают без
// модификаторов. 1/2 и ←/→ выбирают сторону текущего матча, Enter запускает
// турнир на стартовом экране, Esc закрывает игру.
App.tournamentKey = function (e) {
  if (e.key === 'Escape') { e.preventDefault(); this.closeTournament(); return; }
  if (this._tournament.loading) return;
  const tag = e.target && e.target.tagName;
  // Enter по кнопке должен нажимать саму кнопку (Start, «Открыть пост»), а не
  // запускать новый турнир поверх финального экрана.
  if (tag === 'BUTTON' || tag === 'A') return;
  // Поле тегов на стартовом экране: набор текста, каретка, выделение — его дело.
  // Без этого «1girl» выбирало бы сторону матча (цифры 1/2 — хоткеи игры), а
  // стрелки уводили бы выбор с поля. Enter из поля запускает турнир: это то же
  // самое, что кнопка «Начать», и ожидаемо для поля фильтра.
  if ((tag === 'INPUT' || tag === 'TEXTAREA') && e.key !== 'Enter') return;
  const match = this._currentMatch();
  if (!match) {
    if (e.key === 'Enter') { e.preventDefault(); this.startTournament(); }
    return;
  }
  const side = e.key === '1' || e.key === 'ArrowLeft' ? match.a
    : e.key === '2' || e.key === 'ArrowRight' ? match.b : -1;
  if (side >= 0) { e.preventDefault(); this._trPick(side); }
};

// Системная настройка «уменьшить движение» отключает задержку на анимацию
// выбора и счётчик на финале: результат должен появляться мгновенно.
App._trMotionOff = function () {
  return typeof window !== 'undefined' && !!window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;
};

// ── Посты участников для галереи финала и «Открыть пост» ──────────────────
//
// Участников сетки сервер отдаёт в «игровом» виде: миниатюра уже завёрнута в
// /api/proxy, а для скачанных постов вместо адреса лежит /api/thumb/:id.
// Просмотрщику такой объект отдавать нельзя: он проксирует адреса сам, и
// повторная обёртка превращалась в /api/proxy?url=/api/proxy?url=… — запрос
// висел, а вьювер крутил спиннер бесконечно. Поэтому на финале запрашиваем
// участников отдельным эндпоинтом: он отвечает сырыми адресами источника и
// признаком downloaded, то есть ровно тем, что ждёт вьювер.

// Уникальные id участников в порядке сетки — ids для /api/tournament/posts.
App._trParticipantIds = function () {
  const seen = new Set();
  const ids = [];
  for (const p of this._tournament.posts || []) {
    const id = p && Number(p.id);
    if (!id || seen.has(id)) continue;
    seen.add(id);
    ids.push(id);
  }
  return ids;
};

// Загрузка постов участников. Один запрос на всю галерею (сервер сам решает,
// сколько обращений к источнику нужно), ответ кладём в tm.links по id.
App._trFetchLinks = async function () {
  const tm = this._tournament;
  if (tm.links || tm.linksLoading) return;
  const ids = this._trParticipantIds();
  if (!ids.length) return;
  tm.linksLoading = true;
  try {
    // Путь БЕЗ /api: префикс подставляет API._fetchGet сам.
    const qs = new URLSearchParams({ ids: ids.join(','), source: tm.source });
    const d = await API.get('/tournament/posts?' + qs.toString(), { fresh: true });
    const map = {};
    for (const p of (d && d.posts) || []) {
      if (p && p.id) map[p.id] = p;
    }
    tm.links = map;
  } catch (err) {
    // Ссылки не подъехали: галерея скажет об этом, а «Открыть пост» честно
    // откажется открывать пустоту (см. _trOpenPost).
    tm.links = null;
  }
  tm.linksLoading = false;
  this._trPaintGallery();
};

// Пост участника по его индексу в сетке. null — ссылок ещё нет либо пост
// пропал на источнике (сервер вернул его id в missing).
App._trLinkedPost = function (idx) {
  const tm = this._tournament;
  const p = tm.posts && tm.posts[idx];
  if (!p || !tm.links) return null;
  return tm.links[p.id] || null;
};

// Миниатюра участника: скачанный пост берём из библиотеки, остальным —
// сырой preview_url источника, завёрнутый в /api/proxy ОДИН раз (kind=preview
// даёт immutable Cache-Control и cache-first в service worker).
App._trThumbOf = function (post) {
  if (post.downloaded) return '/api/thumb/' + post.id;
  const src = post.preview_url || post.sample_url || post.file_url || '';
  if (!src) return '';
  // Уже проксированный адрес не заворачиваем второй раз — именно на этом
  // вьювер и висел со спиннером.
  if (src.indexOf('/api/') === 0) return src;
  return '/api/proxy?url=' + encodeURIComponent(src) + '&kind=preview';
};

// Плитки галереи: все участники турнира, а не только чемпион. Отдельная
// функция — чтобы после ответа сервера перерисовать только галерею, не
// перезапуская счётчик оценки и искры финала.
App._trGalleryTiles = function () {
  const tm = this._tournament;
  const list = tm.posts || [];
  if (!tm.links) {
    // Ссылки ещё едут: плейсхолдеры вместо «пост недоступен», иначе уже
    // известные участники выглядели бы потерянными.
    let wait = '';
    for (let i = 0; i < list.length; i++) wait += '<span class="tr-gal load" aria-hidden="true"></span>';
    return wait;
  }
  let tiles = '';
  for (let i = 0; i < list.length; i++) {
    const post = this._trLinkedPost(i);
    if (!post) {
      // Пост исчез на источнике: плитка говорит об этом, но вьювер не
      // открывает — иначе он остался бы со спиннером навсегда.
      tiles += '<span class="tr-gal off" title="' + esc(t('tr.postGone')) + '">' + icon('image', 14) + '</span>';
      continue;
    }
    const thumb = this._trThumbOf(post);
    tiles += '<button type="button" class="tr-gal' + (i === tm.winner ? ' me' : '') +
      '" data-idx="' + i + '" aria-label="' + esc(t('tr.openParticipant')) + '">' +
      (thumb ? '<img src="' + esc(thumb) + '" alt="" loading="lazy" decoding="async">' : '') +
    '</button>';
  }
  return tiles;
};

// Галерея целиком: заголовок и контейнер плиток с якорем для точечной
// перерисовки.
App._trGallery = function () {
  return '<div class="tr-gallery">' +
    '<div class="tr-label">' + esc(t('tr.allParticipants')) + '</div>' +
    '<div class="tr-gal-grid" data-tr="gallery">' + this._trGalleryTiles() + '</div>' +
  '</div>';
};

// Точечная перерисовка галереи после ответа /api/tournament/posts. Полный
// renderTournamentFinal() перезапустил бы «набегание» оценки и искры — финал
// выглядел бы как переигранный.
App._trPaintGallery = function () {
  const root = this.els.tournamentRoot;
  const box = root && root.querySelector ? root.querySelector('[data-tr="gallery"]') : null;
  if (!box) return;
  box.innerHTML = this._trGalleryTiles();
  this._trWireGallery(box);
};

App._trWireGallery = function (box) {
  if (!box || !box.querySelectorAll) return;
  for (const b of box.querySelectorAll('.tr-gal')) {
    // Плейсхолдер и «пост недоступен» — не кнопки, обработчик им не нужен.
    if (!b.dataset || b.dataset.idx === undefined) continue;
    b.addEventListener('click', () => this._trOpenPost(parseInt(b.dataset.idx, 10)));
  }
};

// Открыть пост участника во вьювере. Вьюверу отдаём запись как есть — сырые
// адреса источника и downloaded: проксирование его дело, клиент адреса не
// переписывает (иначе получается двойной /api/proxy и вечная загрузка).
App._trOpenPost = async function (idx) {
  let post = this._trLinkedPost(idx);
  if (!post) {
    // Ссылок ещё нет (или предыдущий запрос не удался) — тянем по требованию,
    // чтобы кнопка не открывала пустоту.
    await this._trFetchLinks();
    post = this._trLinkedPost(idx);
  }
  if (!post) {
    this.showToast(t('tr.postGone'), 'error');
    return;
  }
  this.closeTournament();
  this.state.posts = [post];
  this.openViewer(0);
};

// Финал: чемпион, его оценка буры и «сколько раз вы совпали с мнением
// сообщества». Раскрываем теги только здесь — это и награда, и объяснение.
App.renderTournamentFinal = function () {
  const root = this.els.tournamentRoot;
  const tm = this._tournament;
  if (tm.winner === null || !tm.posts || !tm.posts[tm.winner]) {
    // Аварийный выход: сетка дошла до конца, но чемпион не записан.
    root.innerHTML = '<div class="tr-panel"><p>' + esc(t('tr.loading')) + '</p></div>';
    return;
  }
  const p = tm.posts[tm.winner];
  const ratio = tm.stats.played ? Math.round((tm.stats.wins / tm.stats.played) * 100) : 0;
  const tags = (p.tags || '').split(' ').filter(Boolean).slice(0, 12);
  // Искры вокруг чемпиона: позиции и задержки считаем по индексу, а не
  // случайно, — разметка остаётся предсказуемой при каждой перерисовке.
  let sparks = '';
  for (let i = 0; i < 12; i++) {
    sparks += '<i class="tr-spark' + (i % 3 === 0 ? ' big' : '') +
      '" style="--tr-x:' + (8 + ((i * 37) % 84)) + '%;--tr-d:' + (i * 160) + 'ms"></i>';
  }
  root.innerHTML = this._trChips() +
    '<div class="tr-final-wrap">' +
      '<i class="tr-rays"></i>' +
      '<div class="tr-card tr-champion">' +
        '<div class="tr-topline">' + esc(t('tr.brand')) + '</div>' +
        '<h2 class="tr-title">' + esc(t('tr.champion')) + '</h2>' +
        '<div class="tr-crown">' + icon('trophy', 20) + '</div>' +
        '<div class="tr-final-img"><img src="' + esc(p.thumb) + '" alt="" decoding="async"></div>' +
        '<div class="tr-score"><b data-tr="score">0</b><span>' + esc(t('tr.booruScore')) + '</span></div>' +
        '<p class="tr-final-stat">' + esc(t('tr.agree', { wins: tm.stats.wins, played: tm.stats.played, pct: ratio })) + '</p>' +
        '<div class="tr-final-tags">' + tags.map((tg) => '<span class="tr-tag">' + esc(tg) + '</span>').join('') + '</div>' +
        '<div class="tr-actions">' +
          '<button type="button" class="tr-btn" data-tr="again">' + esc(t('tr.again')) + '</button>' +
          '<button type="button" class="tr-btn ghost" data-tr="open">' + esc(t('tr.openPost')) + '</button>' +
          '<button type="button" class="tr-btn ghost" data-tr="close">' + esc(t('tr.done')) + '</button>' +
        '</div>' +
        // Галерея всех участников: финал — единственный экран, где видно всю
        // сетку разом, и вернуться к проигравшему по-другому нельзя.
        this._trGallery() +
        '<div class="tr-sparks">' + sparks + '</div>' +
      '</div>' +
    '</div>';

  // Оценка буры «набегает»: цифра читается как результат, а не как вёрстка.
  const score = root.querySelector('[data-tr="score"]');
  if (score) this._trCountUp(score, p.score);
  const again = root.querySelector('[data-tr="again"]');
  if (again) again.addEventListener('click', () => { this._tournament.posts = null; this.renderTournament(); });
  const open = root.querySelector('[data-tr="open"]');
  if (open) open.addEventListener('click', () => this._trOpenPost(tm.winner));
  this._trWireGallery(root.querySelector('[data-tr="gallery"]'));
  // Ссылки на посты тянем сразу: и галерея, и «Открыть пост» берут адреса
  // оттуда, а в карточках сетки лежат миниатюры, уже завёрнутые в /api/proxy.
  this._trFetchLinks();
  const close = root.querySelector('[data-tr="close"]');
  if (close) close.addEventListener('click', () => this.closeTournament());
};

// Оценка чемпиона «набегает» от нуля: так цифра читается как результат игры,
// а не как ещё один элемент вёрстки. easeOutCubic — быстрый разгон и мягкая
// остановка на финальном значении; при выключенных анимациях значение
// ставится сразу, без кадров.
App._trCountUp = function (el, target) {
  const goal = Number(target) || 0;
  if (this._trMotionOff() || typeof requestAnimationFrame !== 'function') {
    el.textContent = String(goal);
    return;
  }
  const dur = 900;
  const t0 = performance.now();
  const step = (now) => {
    const k = Math.min(1, (now - t0) / dur);
    el.textContent = String(Math.round(goal * (1 - Math.pow(1 - k, 3))));
    if (k < 1) requestAnimationFrame(step);
  };
  el.textContent = '0';
  requestAnimationFrame(step);
};

