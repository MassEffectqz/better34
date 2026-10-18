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
import { t } from './i18n.js';
import { esc, icon } from './utils.js';
import './tournament_bracket.js';
import './tournament_setup.js';

export { ROUNDS_PRESETS } from './tournament_setup.js';

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
  /**
   * Вьювер открыт из галереи участников. Турнир при этом НЕ закрывается: он
   * остаётся под вьювером целиком, иначе посмотреть второго-третьего участника
   * было бы нельзя (closeTournament сбрасывал open и всю сетку).
   * @type {boolean}
   */
  viewerOpen: false,
  /** @type {object[] | null} выдача ленты, которую подменил вьювер участника */
  viewerReturnPosts: null,
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
  /**
   * Автодополнение тегов на стартовом экране. Работает по тому же словарю,
   * что и поиск в шапке, но рисуется внутри оверлея турнира.
   * @type {object[]} подсказки текущего слова (сырые записи /suggest)
   */
  sug: [],
  /** @type {number} индекс активной подсказки, -1 — ни одна не выбрана */
  sugIdx: -1,
  /** @type {number} номер запроса: ответы с устаревшим seq молча отбрасываются */
  sugSeq: 0,
  /** @type {any} отложенный запуск подсказок (debounce) */
  sugTimer: null,
  /**
   * @type {number} токен отрисовки. Стартовый экран перерисовывается целиком
   * (смена раунда, источника, чип тега), и ответ подсказок, пришедший на
   * предыдущую разметку, обязан быть отброшен — иначе он отрисовал бы список
   * в узде, которого больше нет.
   */
  sugToken: 0,
};

App.openTournament = function () {
  // Если перед открытием висел просмотр участника — closeViewer вернёт нас в
  // турнир (см. _trReturnFromViewer), а дальше мы всё равно переинициализируем
  // состояние под новую игру. Порядок важен: сброс флагов — после закрытия,
  // иначе closeViewer решит, что открыт «старый» турнир, и вернёт его поверх.
  this.closeViewer();
  this._tournament.open = true;
  this._tournament.viewerOpen = false;
  this._tournament.viewerReturnPosts = null;
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
  this._tournament.mode = 'bracket';
  this._tournament.collectionId = '';
  this._tournament.history = [];
  this._tournament.koth = null;
  this._tournament.inspecting = null;
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
  // Просмотр участника живёт ПОВЕРХ турнира (см. _trOpenPost): закрываем его
  // первым, иначе оверлей вьювера остался бы висеть поверх уже закрытой игры.
  // Порядок важен — closeViewer вернул бы нас в турнир, который мы закрываем.
  if (this._tournament.viewerOpen) {
    this._tournament.viewerOpen = false;
    this._tournament.viewerReturnPosts = null;
    this.closeViewer();
  }
  this._tournament.open = false;
  this._tournament.loading = false;
  this.state.tournamentOpen = false;
  if (typeof this._trCloseInspect === 'function') this._trCloseInspect();
  // Отложенный запрос /suggest обязан умереть вместе с экраном: ответ придёт
  // уже в закрытый турнир и без sugSeq-guard переоткроет список поверх пустоты.
  this._trHideSuggest();
  const root = this.els.tournamentRoot;
  if (root) { root.innerHTML = ''; root.classList.add('hidden'); }
  this.invalidateFeedCache();
};

/** @this {AppType} */
App.renderTournament = function () {
  const root = this.els.tournamentRoot;
  if (!root) return;
  const tm = this._tournament;
  if (!tm.open) { root.innerHTML = ''; root.classList.add('hidden'); return; }
  root.classList.remove('hidden');

  if (tm.loading) {
    if (tm.source === 'online') {
      root.innerHTML =
        '<div class="tr-stage">' +
          '<div class="tr-card tr-loading-card">' +
            '<div class="tr-spinner">' + icon('loader', 32) + '</div>' +
            '<div class="tr-title" style="font-size:1.3rem;margin-top:14px">' +
              esc('Подбор участников онлайн') +
            '</div>' +
            '<div class="tr-sub">' +
              esc('Запрашиваем посты на источнике и формируем турнирную сетку…') +
            '</div>' +
            '<div class="tr-actions" style="margin-top:16px;max-width:200px">' +
              '<button type="button" class="tr-btn ghost" data-tr="cancel-load">' +
                esc(t('tr.cancel') || 'Отмена') +
              '</button>' +
            '</div>' +
          '</div>' +
        '</div>';
      root.querySelector('[data-tr="cancel-load"]')?.addEventListener('click', () => {
        tm.loading = false;
        this.renderTournamentSetup();
      });
      return;
    }
    root.innerHTML = '<div class="tr-center">' + esc(t('tr.loading')) + '</div>';
    return;
  }
  if (!tm.posts) { this.renderTournamentSetup(); return; }
  const match = this._currentMatch();
  if (!match) { this.renderTournamentFinal(); return; }
  this.renderTournamentMatch(match);
};

// Клавиши турнира: игра занимает весь экран, поэтому её хоткеи работают без
// модификаторов. 1/2, A/D и ←/→ выбирают сторону текущего матча, Enter запускает
// турнир на стартовом экране, Esc закрывает игру, Пробел открывает быстрый осмотр,
// Ctrl+Z / Backspace / Z откатывают последний ход.
App.tournamentKey = function (e) {
  // Вьювер открыт ПОВЕРХ турнира: клавиши принадлежат вьюверу.
  if (this._tournament.viewerOpen) return;
  if (this._tournament.inspecting !== null) {
    if (e.key === 'Escape' || e.key === ' ') {
      e.preventDefault();
      this._trCloseInspect();
      return;
    }
  }
  if (e.key === 'Escape') { e.preventDefault(); this.closeTournament(); return; }
  if (this._tournament.loading) return;
  const tag = e.target && e.target.tagName;
  // Enter по кнопке должен нажимать саму кнопку (Start, «Открыть пост»), а не
  // запускать новый турнир поверх финального экрана.
  if (tag === 'BUTTON' || tag === 'A') return;
  // Поле тегов на стартовом экране: набор текста, каретка, выделение — его дело.
  if ((tag === 'INPUT' || tag === 'TEXTAREA') && e.key !== 'Enter') return;
  const match = this._currentMatch();
  if (!match) {
    if (e.key === 'Enter') { e.preventDefault(); this.startTournament(); }
    return;
  }
  if ((e.ctrlKey && (e.key === 'z' || e.key === 'Z')) || e.key === 'Backspace' || e.key === 'z' || e.key === 'Z') {
    e.preventDefault();
    if (typeof this.tournamentUndo === 'function') this.tournamentUndo();
    return;
  }
  if (e.key === ' ') {
    e.preventDefault();
    if (typeof this._trQuickInspect === 'function') this._trQuickInspect(match.a);
    return;
  }
  const side = (e.key === '1' || e.key === 'ArrowLeft' || e.key === 'a' || e.key === 'A') ? match.a
    : (e.key === '2' || e.key === 'ArrowRight' || e.key === 'd' || e.key === 'D') ? match.b : -1;
  if (side >= 0) { e.preventDefault(); this._trPick(side, true); }
};
