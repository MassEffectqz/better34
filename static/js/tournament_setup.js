// tournament_setup.js — стартовый экран турнира: выбор раундов, теги, автодополнение.

import { App } from './state.js';
import { API } from './api.js';
import { t } from './i18n.js';
import { esc, icon } from './utils.js';

export const ROUNDS_PRESETS = [2, 3, 4, 5];

// Пауза перед запросом подсказок и предел списка — те же, что в шапке: там
// 200 мс и 12 строк. Разные значения в двух полях выглядели бы как «одно поле
// думает быстрее другого».
const TR_SUGG_DELAY_MS = 200;
const TR_SUGG_MAX = 12;

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

// ── Автодополнение тегов ────────────────────────────────────────────────────
// Словарь и ранжирование берём у поиска (suggestWord + mergeSuggestions +
// /suggest-local + /suggest), чтобы поле турнира подсказывало ровно то же, что
// и строка в шапке. Своих вызовов /suggest здесь нет намеренно: словарь тегов
// один на весь сайт, второй источник правды только разошёлся бы с шапкой.
//
// Гонок тут три, и каждая закрывается своим номером:
//  * sugSeq — пользователь набирает быстрее, чем отвечает сеть;
//  * sugToken — стартовый экран перерисован (сменили раунд/источник/чип), и
//    предыдущему ответу уже некуда положиться;
//  * debounce — не долбим /suggest на каждый символ.

// Значение подсказки: запись из /suggest либо голая строка.
App._trSuggValue = function (item) {
  if (item == null) return '';
  return String(item.value || item.label || item);
};

// Слово под кареткой + границы для подстановки. suggestWord уже умеет не
// отправлять служебные префиксы (-tag, ~tag) в запрос и находить слово по
// каретке, а не по концу строки, поэтому переиспользуем его, а не копируем.
App._trSuggestWord = function (inp) {
  if (typeof this.suggestWord === 'function') return this.suggestWord(inp);
  // Фолбэк на случай, если tournament.js загрузили без search.js (тесты).
  const v = inp.value || '';
  const pos = typeof inp.selectionStart === 'number' ? inp.selectionStart : v.length;
  let start = Math.max(0, Math.min(pos, v.length));
  let end = start;
  const isBreak = (c) => c === ' ' || c === '\t' || c === '\n' || c === '|';
  while (start > 0 && !isBreak(v[start - 1])) start--;
  while (end < v.length && !isBreak(v[end])) end++;
  const raw = v.slice(start, end);
  const m = raw.match(/^[-+~^]+/);
  const prefix = m ? m[0] : '';
  return { start, end, raw, prefix, word: raw.slice(prefix.length) };
};

// Прячем список и гасим отложенный запрос. Вызывается перед любой полной
// перерисовкой стартового экрана и при выходе из поля.
App._trHideSuggest = function () {
  const tm = this._tournament;
  clearTimeout(tm.sugTimer);
  tm.sugTimer = null;
  tm.sug = [];
  tm.sugIdx = -1;
  // Номер увеличиваем всегда: ответ на уже скрытый список обязан отброситься,
  // иначе он переоткроет список после того, как пользователь ушёл из поля.
  tm.sugSeq++;
  const root = this.els.tournamentRoot;
  const box = root && root.querySelector ? root.querySelector('[data-tr="sugg"]') : null;
  if (box) { box.innerHTML = ''; box.hidden = true; }
  const inp = root && root.querySelector ? root.querySelector('[data-tr="tags"]') : null;
  if (inp) {
    inp.setAttribute('aria-expanded', 'false');
    inp.removeAttribute('aria-activedescendant');
  }
};

// Отрисовка списка подсказок. Активная строка помечена и классом, и
// aria-selected: без aria-activedescendant скринридер не знает, какая
// подсказка будет подставлена по Enter.
App._trRenderSuggest = function () {
  const tm = this._tournament;
  const root = this.els.tournamentRoot;
  const box = root && root.querySelector ? root.querySelector('[data-tr="sugg"]') : null;
  if (!box) return;
  if (!tm.sug.length) { box.innerHTML = ''; box.hidden = true; return; }
  let out = '';
  for (let i = 0; i < tm.sug.length; i++) {
    const value = this._trSuggValue(tm.sug[i]);
    const on = i === tm.sugIdx;
    out += '<button type="button" class="tr-sugg-item' + (on ? ' on' : '') + '"' +
      ' role="option" id="tr-sugg-' + i + '"' +
      ' aria-selected="' + (on ? 'true' : 'false') + '"' +
      ' data-tr-sugg="' + esc(value) + '">' + esc(value) + '</button>';
  }
  box.innerHTML = out;
  box.hidden = false;
  const inp = root.querySelector('[data-tr="tags"]');
  if (inp) {
    inp.setAttribute('aria-expanded', 'true');
    if (tm.sugIdx >= 0 && tm.sug[tm.sugIdx]) {
      inp.setAttribute('aria-activedescendant', 'tr-sugg-' + tm.sugIdx);
    } else {
      inp.removeAttribute('aria-activedescendant');
    }
  }
};

// Запрос подсказок для слова `tail`. Локальная база отвечает почти мгновенно,
// удалённый источник медленнее — рисуем локальные сразу, не дожидаясь гонки.
App._trFetchSuggest = function (tail) {
  const tm = this._tournament;
  const seq = ++tm.sugSeq;
  const token = tm.sugToken;
  const localP = API.get('/suggest-local?q=' + encodeURIComponent(tail))
    .then(d => d.tags || []).catch(() => []);
  const remoteP = API.get('/suggest?q=' + encodeURIComponent(tail))
    .then(r => r.tags || []).catch(() => []);

  // Ответ годится, только если пользователь всё ещё в том же слове, экран не
  // перерисован и турнир открыт.
  const stale = () => tm.sugSeq !== seq || tm.sugToken !== token || !tm.open;
  localP.then(local => {
    if (stale() || !local.length) return;
    tm.sug = this.mergeSuggestions(local, [], tail).slice(0, TR_SUGG_MAX);
    tm.sugIdx = tm.sug.length ? 0 : -1;
    this._trRenderSuggest();
  });
  Promise.all([localP, remoteP]).then(([local, remote]) => {
    if (stale()) return;
    // Удалённый источник — правда по счётчикам, поэтому перерисовываем список
    // целиком, даже если локальные подсказки уже показаны.
    tm.sug = this.mergeSuggestions(local, remote || [], tail).slice(0, TR_SUGG_MAX);
    tm.sugIdx = tm.sug.length ? 0 : -1;
    this._trRenderSuggest();
  });
};

// Ввод в поле тегов: значение в состояние, подсказки — по слову под кареткой.
App._trOnTagsInput = function () {
  const tm = this._tournament;
  const root = this.els.tournamentRoot;
  const inp = root && root.querySelector ? root.querySelector('[data-tr="tags"]') : null;
  if (!inp) return;
  tm.tags = inp.value;
  const w = this._trSuggestWord(inp);
  clearTimeout(tm.sugTimer);
  tm.sugTimer = null;
  // Два символа — тот же порог, что в шапке: по одному символу словарь отдаёт
  // сотни совпадений, и такой список просто закрывает всё поле.
  if (w.word.length >= 2) {
    this._trHideSuggest();
    tm.sugTimer = setTimeout(() => this._trFetchSuggest(w.word), TR_SUGG_DELAY_MS);
  } else {
    this._trHideSuggest();
  }
};

// Клик мимо блока с полем закрывает список подсказок. Вынесено отдельным
// методом, чтобы обработчик документа был один (см. _trOutsideBound).
App._trDismissOnOutside = function (e) {
  const tm = this._tournament;
  if (!tm.sug.length) return;
  if (e.target && e.target.closest && e.target.closest('.tr-tags')) return;
  this._trHideSuggest();
};

// Навигация по списку и вставка. Выделенную подсказку вставляют и Enter, и
// Tab: во втором случае «Начать» перехватил бы клавишу и запустил турнир.
// Возвращаем true, если клавиша обработана, — вызывающий обязан это знать,
// чтобы не отдать её дальше в логику игры.
App._trSuggestKey = function (e) {
  const tm = this._tournament;
  const items = tm.sug || [];
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    if (!items.length) return false;
    e.preventDefault();
    e.stopPropagation();
    const n = items.length;
    tm.sugIdx = e.key === 'ArrowDown' ? (tm.sugIdx + 1) % n : (tm.sugIdx - 1 + n) % n;
    this._trRenderSuggest();
    return true;
  }
  if (e.key === 'Enter' || e.key === 'Tab') {
    if (!items.length) return false;
    e.preventDefault();
    e.stopPropagation();
    this._trApplySuggest(this._trSuggValue(items[tm.sugIdx >= 0 ? tm.sugIdx : 0]));
    return true;
  }
  if (e.key === 'Escape' && items.length) {
    // Первый Esc закрывает подсказки, а не весь турнир: список — часть поля.
    e.preventDefault();
    e.stopPropagation();
    this._trHideSuggest();
    return true;
  }
  return false;
};

// Подставить подсказку вместо слова под кареткой. Префикс (-, ~, +) сохраняем:
// по «-brea» пользователь ждёт исключение тега, а не новый запрос. Хвост после
// каретки не трогаем — правка тега в середине запроса не должна его стирать.
App._trApplySuggest = function (value) {
  const tm = this._tournament;
  const root = this.els.tournamentRoot;
  const inp = root && root.querySelector ? root.querySelector('[data-tr="tags"]') : null;
  if (!inp) return;
  const w = this._trSuggestWord(inp);
  const v = inp.value || '';
  const after = v.slice(w.end);
  const next = (v.slice(0, w.start) + w.prefix + value + (after ? ' ' + after : ' '))
    .replace(/\s{2,}/g, ' ');
  inp.value = next;
  tm.tags = next;
  if (typeof inp.setSelectionRange === 'function') {
    const pos = (inp.value.indexOf(value, w.start) + value.length) || next.length;
    try { inp.setSelectionRange(pos, pos); } catch { /* некоторые input не умеют */ }
  }
  if (inp.focus) inp.focus();
  this._trHideSuggest();
};

App.renderTournamentSetup = function () {
  const root = this.els.tournamentRoot;
  const tm = this._tournament;
  // Любая полная перерисовка обесценивает список подсказок: ответ, пришедший
  // на старую разметку, обязан отброситься (см. sugToken).
  this._trHideSuggest();
  tm.sugToken++;
  if (!tm.mode) tm.mode = 'bracket';
  const userCols = (this.state && this.state.profile && this.state.profile.collections) || [];
  if (tm.source === 'collection' && !tm.collectionId && userCols.length > 0) {
    tm.collectionId = userCols[0].id;
  }

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
  let srcHint = tm.source === 'online' ? t('tr.onlineHint') : t('tr.offlineHint');
  if (tm.mode === 'koth') {
    srcHint = t('tr.kothHint');
  } else if (tm.source === 'collection') {
    srcHint = t('tr.colSelect');
  }

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
      '<div class="tr-section"><div class="tr-label">' + esc(t('tr.mode')) + '</div>' +
        '<div class="tr-sources">' +
          '<button type="button" class="tr-src' + (tm.mode !== 'koth' ? ' on' : '') + '" data-mode="bracket" aria-pressed="' + (tm.mode !== 'koth') + '">' + esc(t('tr.modeBracket')) + '</button>' +
          '<button type="button" class="tr-src' + (tm.mode === 'koth' ? ' on' : '') + '" data-mode="koth" aria-pressed="' + (tm.mode === 'koth') + '">' + esc(t('tr.modeKoth')) + '</button>' +
        '</div>' +
      '</div>' +
      '<div class="tr-section"><div class="tr-label">' + esc(t('tr.rounds')) + '</div>' +
        '<div class="tr-presets">' + roundsBtns + '</div></div>' +
      '<div class="tr-section"><div class="tr-label">' + esc(t('tr.source')) + '</div>' +
        '<div class="tr-sources">' +
          '<button type="button" class="tr-src' + (tm.source === 'offline' ? ' on' : '') + '" data-source="offline" aria-pressed="' + (tm.source === 'offline') + '">' + esc(t('tr.srcOffline')) + '</button>' +
          '<button type="button" class="tr-src' + (tm.source === 'online' ? ' on' : '') + '" data-source="online" aria-pressed="' + (tm.source === 'online') + '">' + esc(t('tr.srcOnline')) + '</button>' +
          (userCols.length > 0 ? '<button type="button" class="tr-src' + (tm.source === 'collection' ? ' on' : '') + '" data-source="collection" aria-pressed="' + (tm.source === 'collection') + '">' + esc(t('tr.srcCollection')) + '</button>' : '') +
        '</div>' +
        (tm.source === 'collection' && userCols.length > 0 ?
          '<div style="margin-top:10px">' +
            '<div class="tr-label">' + esc(t('tr.colSelect')) + '</div>' +
            '<select class="tr-input" data-tr="col-select">' +
              userCols.map(c => '<option value="' + esc(c.id) + '"' + (c.id === tm.collectionId ? ' selected' : '') + '>' + esc(c.name) + ' (' + c.count + ')</option>').join('') +
            '</select>' +
          '</div>' : '') +
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
            ' role="combobox" aria-expanded="false" aria-autocomplete="list"' +
            ' aria-controls="tr-sugg-list" aria-label="' + esc(t('tr.tags')) + '">' +
          (tm.tags ? '<button type="button" class="tr-clear" data-tr="tags-clear"' +
            ' title="' + esc(t('tr.tagsClear')) + '" aria-label="' + esc(t('tr.tagsClear')) + '">' +
            icon('x', 14) + '</button>' : '') +
          // Список подсказок: роль listbox, значение поля — combobox. Подсказки
          // приходят из того же словаря, что и в шапке (локальная база + бор),
          // поэтому «1girl» дописывается здесь так же, как в строке поиска.
          '<div class="tr-sugg" id="tr-sugg-list" data-tr="sugg" role="listbox" aria-label="' + esc(t('tr.suggAria')) + '" hidden></div>' +
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

  for (const b of root.querySelectorAll('[data-mode]')) {
    b.addEventListener('click', () => {
      tm.mode = b.dataset.mode === 'koth' ? 'koth' : 'bracket';
      this.renderTournamentSetup();
    });
  }
  for (const b of root.querySelectorAll('[data-rounds]')) {
    b.addEventListener('click', () => {
      tm.rounds = parseInt(b.dataset.rounds, 10) || 3;
      this.renderTournamentSetup();
    });
  }
  for (const b of root.querySelectorAll('[data-source]')) {
    b.addEventListener('click', () => {
      tm.source = b.dataset.source;
      this.renderTournamentSetup();
    });
  }
  const colSelect = root.querySelector('[data-tr="col-select"]');
  if (colSelect) {
    colSelect.addEventListener('change', () => {
      tm.collectionId = colSelect.value;
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
    tagsInput.addEventListener('input', () => this._trOnTagsInput());
    // Клавиши перехватываем на самом поле: глобальный onKeydown отдал бы Enter
    // турниру (tournamentKey запускает игру по Enter из поля), а нужен выбор
    // подсказки. stopPropagation в _trSuggestKey не даёт событию дойти дальше.
    tagsInput.addEventListener('keydown', (e) => { this._trSuggestKey(e); });
    // Клик мышью по самому полю не должен закрывать только что открытый список:
    // фокус уже здесь, пересчитывать слово незачем.
    tagsInput.addEventListener('click', () => {
      const tm2 = this._tournament;
      if (tm2.sug.length) this._trRenderSuggest();
    });
  }
  // Клик по подсказке. Делегирование по контейнеру — список перерисовывается
  // целиком, и слушатель на каждой строке накапливался бы в памяти.
  const suggBox = root.querySelector('[data-tr="sugg"]');
  if (suggBox) {
    suggBox.addEventListener('mousedown', (e) => {
      // mousedown, а не click: по умолчанию поле теряет фокус ДО click, список
      // скрывается, и клик уже никому не достаётся — подсказка не выбиралась.
      const btn = e.target.closest('[data-tr-sugg]');
      if (!btn) return;
      e.preventDefault();
      this._trApplySuggest(btn.dataset.trSugg);
    });
  }
  // Клик мимо поля закрывает список (как в шапке), но НЕ отменяет саму
  // обработку клика: кнопки «Начать»/источник живут в этом же оверлее.
  // Слушатель ставится ОДИН раз на документ: renderTournamentSetup вызывается
  // на каждый чих (раунд, источник, чип), и новый слушатель в каждой отрисовке
  // копился бы в памяти, срабатывая по несколько раз на один клик.
  if (!App._trOutsideBound) {
    App._trOutsideBound = true;
    document.addEventListener('mousedown', function (e) {
      if (typeof this._trDismissOnOutside === 'function') this._trDismissOnOutside(e);
    });
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
  if (tm.source === 'collection' && tm.collectionId) {
    qs.set('collection', tm.collectionId);
  }
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
