// tournament_bracket.js — турнирная сетка: геометрия дерева, перемычки, матчи и финал.

import { App } from './state.js';
import { API } from './api.js';
import { t } from './i18n.js';
import { esc, icon } from './utils.js';

// Ближайший матч, который можно разыграть: уровень 0 засеян участниками,
// поэтому играем по порядку уровней и пар. Матч готов, когда оба его слота
// заняты — либо участниками (первый круг), либо победителями снизу.
//
// Проверять ТОЛЬКО уровень 0 нельзя: после первого круга сетка ещё не пуста,
// и турнир объявился бы завершённым, не разыграв второй круг.
App._currentMatch = function () {
  const tm = this._tournament;
  if (tm.mode === 'koth') {
    if (!tm.koth || tm.koth.kingIdx === null || tm.koth.challengerIdx === null) return null;
    return { round: 0, pair: 0, a: tm.koth.kingIdx, b: tm.koth.challengerIdx, isKoth: true };
  }
  for (let r = 0; r < tm.bracket.length; r++) {
    for (let i = 0; i < tm.bracket[r].length; i++) {
      const [a, b] = tm.bracket[r][i];
      if (a >= 0 && b >= 0 && !tm.resolved.has(r + ':' + i)) {
        return { round: r, pair: i, a, b, isKoth: false };
      }
    }
  }
  return null;
};

// Победитель матча поднимается на уровень выше. На последнем уровне он и есть
// чемпион турнира.
App._resolveMatch = function (match, winnerIdx) {
  const tm = this._tournament;
  if (match.isKoth) {
    const k = tm.koth;
    if (!k) return false;
    const isKingWinner = winnerIdx === k.kingIdx;
    const loserIdx = isKingWinner ? k.challengerIdx : k.kingIdx;
    tm.stats.played++;
    if (tm.posts[winnerIdx].score >= tm.posts[loserIdx].score) tm.stats.wins++;
    if (isKingWinner) {
      k.currentStreak++;
      k.bestStreak = Math.max(k.bestStreak, k.currentStreak);
    } else {
      k.kingIdx = winnerIdx;
      k.currentStreak = 1;
      k.bestStreak = Math.max(k.bestStreak, 1);
    }
    if (k.pool.length > 0) {
      k.challengerIdx = k.pool.shift();
    } else {
      tm.winner = k.kingIdx;
      k.challengerIdx = null;
    }
    return true;
  }
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

  if (match.round === tm.bracket.length - 2) {
    if (!tm.semiLosers) tm.semiLosers = [];
    tm.semiLosers.push(loserIdx);
  }

  tm.bracket[match.round][match.pair] = [winnerIdx, winnerIdx];
  const up = match.round + 1;
  if (up < tm.bracket.length) {
    tm.bracket[up][Math.floor(match.pair / 2)][match.pair % 2] = winnerIdx;
  } else {
    tm.winner = winnerIdx;
    tm.runnerUp = loserIdx;
  }
  return true;
};

// Расккладывает участников по первому кругу и сбрасывает состояние матчей.
App._seedTournament = function (posts) {
  const tm = this._tournament;
  tm.posts = posts;
  tm.winner = null;
  tm.runnerUp = null;
  tm.semiLosers = [];
  tm.resolved = new Set();
  tm.lastResolved = null;
  tm.seenRound = -1;
  tm.busy = false;
  tm.history = [];
  tm.inspecting = null;
  // Счётчик матчей обнуляем ЗДЕСЬ, а не в openTournament: кнопка «Ещё турнир»
  // на финальном экране зовёт _seedTournament напрямую, и без сброса новый
  // турнир продолжил бы статистику предыдущего (на первом же матче «2 / 7»).
  tm.stats = { played: 0, wins: 0 };

  if (tm.mode === 'koth') {
    tm.koth = {
      kingIdx: 0,
      challengerIdx: posts.length > 1 ? 1 : null,
      currentStreak: 0,
      bestStreak: 0,
      pool: posts.map((_, i) => i).slice(2)
    };
    return;
  }

  tm.koth = null;
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

App._trMediaUrl = function (p) {
  if (!p) return '';
  if (p.downloaded) return '/api/thumb/' + p.id;
  const raw = p.sample_url || p.file_url || p.preview_url || '';
  if (!raw) return p.thumb || '';
  if (raw.indexOf('/api/') === 0) return raw;
  const kind = p.sample_url ? 'sample' : 'preview';
  return '/api/proxy?url=' + encodeURIComponent(raw) + '&kind=' + kind;
};

App.tournamentUndo = function () {
  const tm = this._tournament;
  if (!tm.history || !tm.history.length) return;
  const prev = tm.history.pop();
  if (!prev) return;
  tm.bracket = prev.bracket;
  tm.resolved = new Set(prev.resolved);
  tm.stats = prev.stats;
  tm.lastResolved = prev.lastResolved;
  tm.seenRound = prev.seenRound;
  tm.winner = null;
  tm.runnerUp = prev.runnerUp ?? null;
  tm.semiLosers = prev.semiLosers ?? [];
  if (prev.koth) tm.koth = prev.koth;
  this.renderTournament();
};

App.tournamentFinishKoth = function () {
  const tm = this._tournament;
  if (tm.mode === 'koth' && tm.koth) {
    tm.winner = tm.koth.kingIdx;
    tm.koth.challengerIdx = null;
    this.renderTournamentFinal();
  }
};

App._trQuickInspect = function (idx) {
  const tm = this._tournament;
  const p = tm.posts && tm.posts[idx];
  if (!p) return;
  tm.inspecting = idx;
  const root = this.els.tournamentRoot;
  if (!root) return;
  let overlay = root.querySelector('.tr-inspect-overlay');
  if (!overlay) {
    overlay = document.createElement('div');
    overlay.className = 'tr-inspect-overlay';
    root.appendChild(overlay);
  }
  const isVideo = p.file_type === 'webm' || p.file_type === 'mp4' || (p.file_url && (p.file_url.endsWith('.webm') || p.file_url.endsWith('.mp4')));
  const mediaUrl = this._trMediaUrl(p);
  overlay.innerHTML =
    '<div class="tr-inspect-modal">' +
      '<button type="button" class="tr-inspect-close" data-tr="close-inspect" aria-label="' + esc(t('confirm.cancel')) + '">×</button>' +
      '<div class="tr-inspect-media">' +
        (isVideo
          ? '<video class="tr-inspect-video" src="' + esc(mediaUrl) + '" poster="' + esc(p.thumb) + '" autoplay loop controls playsinline></video>'
          : '<img class="tr-inspect-img" src="' + esc(mediaUrl) + '" alt="" loading="eager">') +
      '</div>' +
      '<div class="tr-inspect-meta">' +
        '<span class="tr-inspect-title">#' + p.id + '</span>' +
        '<button type="button" class="tr-btn tr-inspect-pick" data-idx="' + idx + '">' + esc(t('tr.pick')) + '</button>' +
      '</div>' +
    '</div>';
  overlay.querySelector('[data-tr="close-inspect"]')?.addEventListener('click', () => this._trCloseInspect());
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay) this._trCloseInspect();
  });
  overlay.querySelector('.tr-inspect-pick')?.addEventListener('click', () => {
    this._trCloseInspect();
    this._trPick(idx);
  });
};

App._trCloseInspect = function () {
  const tm = this._tournament;
  tm.inspecting = null;
  const root = this.els.tournamentRoot;
  const overlay = root && root.querySelector ? root.querySelector('.tr-inspect-overlay') : null;
  if (overlay) overlay.remove();
};

App._trPreloadNext = function () {
  if (typeof Image === 'undefined') return;
  const tm = this._tournament;
  if (!tm.posts) return;
  if (tm.mode === 'koth') {
    if (tm.koth && tm.koth.pool && tm.koth.pool.length > 0) {
      const p = tm.posts[tm.koth.pool[0]];
      if (p) {
        const url = this._trMediaUrl(p);
        if (url) { const img = new Image(); img.src = url; }
      }
    }
    return;
  }
  const cur = this._currentMatch();
  if (!cur) return;
  for (let r = cur.round; r < tm.bracket.length; r++) {
    const startPair = (r === cur.round) ? cur.pair + 1 : 0;
    for (let i = startPair; i < tm.bracket[r].length; i++) {
      const [a, b] = tm.bracket[r][i];
      if (a >= 0 && b >= 0 && !tm.resolved.has(r + ':' + i)) {
        for (const idx of [a, b]) {
          const p = tm.posts[idx];
          if (p) {
            const url = this._trMediaUrl(p);
            if (url) { const img = new Image(); img.src = url; }
          }
        }
        return;
      }
    }
  }
};

App._bindSwipe = function (matchEl, match) {
  if (!matchEl) return;
  let startX = 0;
  let startY = 0;
  let isSwiping = false;

  matchEl.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1) return;
    startX = e.touches[0].clientX;
    startY = e.touches[0].clientY;
    isSwiping = false;
  }, { passive: true });

  matchEl.addEventListener('touchmove', (e) => {
    if (e.touches.length !== 1) return;
    const curX = e.touches[0].clientX;
    const curY = e.touches[0].clientY;
    const dx = curX - startX;
    const dy = curY - startY;
    if (!isSwiping && Math.abs(dx) > 10 && Math.abs(dx) > Math.abs(dy)) {
      isSwiping = true;
    }
    if (isSwiping) {
      if (e.cancelable) e.preventDefault();
      matchEl.style.transform = 'translateX(' + (dx * 0.75) + 'px) rotate(' + (dx * 0.04) + 'deg)';
      matchEl.classList.toggle('tr-swiping-left', dx < -30);
      matchEl.classList.toggle('tr-swiping-right', dx > 30);
    }
  }, { passive: false });

  const endSwipe = (e) => {
    if (!isSwiping) return;
    isSwiping = false;
    const curX = (e.changedTouches && e.changedTouches[0]) ? e.changedTouches[0].clientX : startX;
    const dx = curX - startX;
    matchEl.style.transition = 'transform 0.25s ease, opacity 0.25s ease';
    matchEl.classList.remove('tr-swiping-left', 'tr-swiping-right');
    if (dx < -80) {
      matchEl.style.transform = 'translateX(-120vw) rotate(-20deg)';
      setTimeout(() => {
        matchEl.style.transition = '';
        matchEl.style.transform = '';
        this._trPick(match.a);
      }, 200);
    } else if (dx > 80) {
      matchEl.style.transform = 'translateX(120vw) rotate(20deg)';
      setTimeout(() => {
        matchEl.style.transition = '';
        matchEl.style.transform = '';
        this._trPick(match.b);
      }, 200);
    } else {
      matchEl.style.transform = '';
      setTimeout(() => { matchEl.style.transition = ''; }, 250);
    }
  };

  matchEl.addEventListener('touchend', endSwipe, { passive: true });
  matchEl.addEventListener('touchcancel', endSwipe, { passive: true });
};

// Матч: две картинки рядом, оценка буры и теги НЕ показываются.
App.renderTournamentMatch = function (match) {
  const root = this.els.tournamentRoot;
  const tm = this._tournament;
  const isKoth = match.isKoth;
  const total = isKoth ? (tm.posts.length - 1) : ((1 << tm.rounds) - 1);
  const pct = Math.min(100, Math.round((tm.stats.played / Math.max(1, total)) * 100));

  const card = (idx, side) => {
    const p = tm.posts[idx];
    const isKing = isKoth && side === 0;
    const isChallenger = isKoth && side === 1;
    const media = this._trMediaUrl(p);
    const isVideo = p.file_type === 'webm' || p.file_type === 'mp4' || (p.file_url && (p.file_url.endsWith('.webm') || p.file_url.endsWith('.mp4')));

    return '<div class="tr-card-wrap">' +
      '<button type="button" class="tr-side' + (isKing ? ' tr-side-king' : '') + (isChallenger ? ' tr-side-challenger' : '') + '" data-idx="' + idx +
        '" style="--tr-d:' + (side * 110 + 60) + 'ms">' +
        (isKing ? '<div class="tr-king-tag">' + icon('crown', 16) + ' <span>' + esc(t('tr.king')) + '</span></div>' : '') +
        (isChallenger ? '<div class="tr-challenger-tag"><span>' + esc(t('tr.challenger')) + '</span></div>' : '') +
        (isVideo
          ? '<video class="tr-img tr-video" src="' + esc(media) + '" poster="' + esc(p.thumb) + '" autoplay loop muted playsinline></video>'
          : '<img class="tr-img" src="' + esc(media) + '" alt="" loading="eager" decoding="async">') +
        '<span class="tr-hint-key">' + esc(t('tr.pick')) + '</span>' +
      '</button>' +
      '<button type="button" class="tr-inspect-btn" data-inspect="' + idx + '" title="' + esc(t('tr.inspect')) + '" aria-label="' + esc(t('tr.inspect')) + '">' +
        icon('zoomIn', 18) +
      '</button>' +
    '</div>';
  };

  const freshRound = !isKoth && match.round !== tm.seenRound;
  if (!isKoth) tm.seenRound = match.round;

  const hasHistory = tm.history && tm.history.length > 0;
  const undoBtn = '<button type="button" class="tr-undo-btn' + (hasHistory ? '' : ' disabled') + '" data-tr="undo" title="' + esc(t('tr.undo')) + '" aria-label="' + esc(t('tr.undo')) + '">' +
    icon('undo', 16) + ' <span>' + esc(t('tr.undo')) + '</span></button>';

  const kothFinishBtn = isKoth
    ? '<button type="button" class="tr-finish-btn" data-tr="finish" title="' + esc(t('tr.finish')) + '">' +
        esc(t('tr.finish')) +
      '</button>'
    : '';

  const metaMiddle = isKoth
    ? '<span class="tr-streak-tag">' + icon('flame', 16) + ' ' + esc(t('tr.streak', { n: tm.koth.currentStreak })) + '</span>'
    : '<span>' + esc(t('tr.round', { n: match.round + 1 })) + '</span>';

  const metaRight = '<span>' + (tm.stats.played + 1) + ' / ' + total + '</span>';

  root.innerHTML = this._trChips() +
    '<div class="tr-stage">' +
      (freshRound
        ? '<div class="tr-round-flash"><span>' + esc(t('tr.round', { n: match.round + 1 })) + '</span></div>'
        : '') +
      '<div class="tr-meta">' +
        '<div class="tr-meta-left">' +
          '<button type="button" class="tr-close" data-tr="close" aria-label="' + esc(t('tr.cancel')) + '">×</button>' +
          undoBtn +
        '</div>' +
        metaMiddle +
        '<div class="tr-meta-right">' +
          kothFinishBtn +
          metaRight +
        '</div>' +
      '</div>' +
      (!isKoth ? this._trTree() : '') +
      '<div class="tr-match">' + card(match.a, 0) +
        '<div class="tr-vs"><i>VS</i><span>' + esc(t('tr.pick')) + '</span></div>' +
        card(match.b, 1) +
      '</div>' +
      '<div class="tr-bar"><div class="tr-bar-fill" style="width:' + pct + '%"></div></div>' +
      '<div class="tr-keys"><kbd>1</kbd><kbd>2</kbd><kbd>←</kbd><kbd>→</kbd><i>' + esc(t('tr.keys')) + '</i></div>' +
    '</div>';

  const fill = root.querySelector('.tr-bar-fill');
  if (fill && typeof requestAnimationFrame === 'function') {
    fill.style.width = '0%';
    requestAnimationFrame(() => { fill.style.width = pct + '%'; });
  }

  for (const b of root.querySelectorAll('.tr-side')) {
    b.addEventListener('click', (ev) => {
      if (ev.isPrimary === false) return;
      this._trPick(parseInt(b.dataset.idx, 10));
    });
  }

  for (const b of root.querySelectorAll('[data-inspect]')) {
    b.addEventListener('click', () => {
      this._trQuickInspect(parseInt(b.dataset.inspect, 10));
    });
  }

  const close = root.querySelector('[data-tr="close"]');
  if (close) close.addEventListener('click', () => this.closeTournament());

  const undo = root.querySelector('[data-tr="undo"]');
  if (undo) undo.addEventListener('click', () => this.tournamentUndo());

  const finish = root.querySelector('[data-tr="finish"]');
  if (finish) finish.addEventListener('click', () => this.tournamentFinishKoth());

  this._trPreloadNext();
  this._bindSwipe(root.querySelector('.tr-match'), match);
};

App._trSaveHistory = function () {
  const tm = this._tournament;
  if (!tm.history) tm.history = [];
  tm.history.push({
    bracket: JSON.parse(JSON.stringify(tm.bracket || [])),
    resolved: Array.from(tm.resolved),
    stats: { ...tm.stats },
    lastResolved: tm.lastResolved,
    seenRound: tm.seenRound,
    runnerUp: tm.runnerUp,
    semiLosers: tm.semiLosers ? [...tm.semiLosers] : [],
    koth: tm.koth ? JSON.parse(JSON.stringify(tm.koth)) : null
  });
};

App._trPick = function (idx, fromKey) {
  const tm = this._tournament;
  const now = Date.now();
  if (tm.busy || (!fromKey && tm._lastPickTime && now - tm._lastPickTime < 280) || !(idx >= 0)) return;
  const cur = this._currentMatch();
  if (!cur || (idx !== cur.a && idx !== cur.b)) return;
  tm._lastPickTime = now;
  tm.busy = true;

  this._trSaveHistory();

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
  } catch {
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
//
// Турнир НЕ закрываем: он остаётся под вьювером и возвращается вместе с ним
// (см. _trReturnFromViewer). Раньше здесь был closeTournament(), и тогда
// посмотреть второго участника было невозможно — после первого «закрыл-открыл»
// турнир исчезал вместе с сеткой.
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
  const tm = this._tournament;
  // Выдачу ленты запоминаем: вьювер работает по state.posts, и без этого
  // после возврата лента осталась бы из одного участника турнира.
  if (!tm.viewerOpen) tm.viewerReturnPosts = this.state.posts;
  tm.viewerOpen = true;
  this.state.posts = [post];
  // Оверлей турнира прячем, а НЕ закрываем: open оставляем, иначе renderTournament
  // при возврате вышел бы в финал/матч по обнулённой сетке.
  const root = this.els.tournamentRoot;
  if (root) root.classList.add('hidden');
  this.state.tournamentOpen = true;
  this.openViewer(0);
};

// Возврат из вьювера обратно в турнир: показываем оверлей, восстанавливаем
// выдачу ленты и перерисовываем экран (обычно это финал с галереей).
// Вызывается из closeViewer.
App._trReturnFromViewer = function () {
  const tm = this._tournament;
  if (!tm.viewerOpen) return;
  tm.viewerOpen = false;
  if (tm.viewerReturnPosts) {
    this.state.posts = tm.viewerReturnPosts;
    tm.viewerReturnPosts = null;
  }
  this.state.tournamentOpen = true;
  // open=true сохраняется: турнир продолжается с того же места.
  this.renderTournament();
};

App._trTop3 = function () {
  const tm = this._tournament;
  if (!tm.posts || tm.winner === null) return null;
  const first = tm.winner;
  let second = null;
  let third = null;

  if (tm.mode === 'koth') {
    const remaining = tm.posts.map((p, idx) => ({ idx, p })).filter(x => x.idx !== first);
    remaining.sort((a, b) => (b.p.score || 0) - (a.p.score || 0));
    if (remaining.length > 0) second = remaining[0].idx;
    if (remaining.length > 1) third = remaining[1].idx;
  } else {
    second = tm.runnerUp;
    if (tm.semiLosers && tm.semiLosers.length > 0) {
      const candidates = [...tm.semiLosers].filter(i => i !== first && i !== second);
      candidates.sort((a, b) => ((tm.posts[b] && tm.posts[b].score) || 0) - ((tm.posts[a] && tm.posts[a].score) || 0));
      third = candidates[0] ?? null;
    }
  }
  if (second == null && tm.posts.length > 1) second = (first === 0 ? 1 : 0);
  if (third == null && tm.posts.length > 2) {
    third = [0, 1, 2].find(i => i !== first && i !== second) ?? null;
  }
  return { first, second, third };
};

App._trToggleChampionLike = function (postId, btn) {
  const profile = (this.state && this.state.profile) || {};
  const likedPosts = profile.liked_posts || [];
  const isLiked = likedPosts.includes(postId);
  const nextLiked = !isLiked;
  this.optimisticLike(postId, nextLiked);
  const nextLabel = nextLiked ? t('tr.liked') : t('tr.like');
  if (btn) {
    btn.classList.toggle('liked', nextLiked);
    btn.innerHTML = icon('heart', 16, nextLiked) + ' <span>' + esc(nextLabel) + '</span>';
  }
  API.post('/like/' + postId, { liked: nextLiked }).then(() => {
    API.invalidate('/profile');
    this.showToast(nextLabel, 'success');
  }).catch((err) => {
    this.optimisticLike(postId, isLiked);
    this.showToast(String(err && err.message || err), 'error');
  });
};

App._trOpenCollectModal = async function (postId) {
  let cols = [];
  try {
    const d = await API.get('/collections?post_id=' + encodeURIComponent(postId), { fresh: true });
    cols = d.collections || [];
  } catch {}
  const root = this.els.tournamentRoot;
  if (!root) return;
  const overlay = document.createElement('div');
  overlay.className = 'batch-collect-overlay';
  overlay.setAttribute('role', 'dialog');
  overlay.setAttribute('aria-modal', 'true');
  overlay.innerHTML =
    '<div class="batch-collect-panel">' +
      '<div class="batch-collect-head">' +
        '<span>' + esc(t('batch.pickCollection')) + '</span>' +
        '<button type="button" class="btn-icon" data-bc-close aria-label="' + esc(t('btn.close')) + '">×</button>' +
      '</div>' +
      '<div class="batch-collect-list">' +
        (cols.length ? cols.map(c =>
          '<button type="button" class="collect-menu-item' + (c.has ? ' active' : '') + '" data-col-id="' + esc(c.id) + '">' +
            '<span class="cm-check">' + (c.has ? icon('check', 13) : '') + '</span>' +
            '<span class="cm-name">' + esc(c.name) + '</span>' +
            '<span class="cm-count">' + c.count + '</span>' +
          '</button>'
        ).join('') : '<div class="collect-menu-empty">' + esc(t('collections.empty')) + '</div>') +
      '</div>' +
    '</div>';
  root.appendChild(overlay);
  const close = () => overlay.remove();
  overlay.querySelector('[data-bc-close]')?.addEventListener('click', close);
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
  overlay.querySelectorAll('[data-col-id]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const colId = btn.dataset.colId;
      try {
        await API.post('/collection/' + colId + '/post', { id: postId });
        API.invalidate('/profile');
        this.loadProfile();
        this.showToast(t('batch.addedToCol', { n: 1 }), 'success');
        close();
      } catch (err) {
        this.showToast(String(err.message || err), 'error');
      }
    });
  });
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

  const top3 = this._trTop3();
  let podiumHtml = '';
  if (top3) {
    const step = (idx, placeClass, titleText, medalName, placeNum) => {
      if (idx === null || !tm.posts[idx]) return '';
      const post = tm.posts[idx];
      const thumb = post.thumb;
      return '<button type="button" class="tr-podium-step ' + placeClass + '" data-idx="' + idx + '" title="' + esc(titleText) + '">' +
        '<div class="tr-podium-card">' +
          '<div class="tr-podium-badge">' + icon(medalName, 18) + ' ' + placeNum + '</div>' +
          '<img class="tr-podium-thumb" src="' + esc(thumb) + '" alt="" loading="lazy">' +
          '<div class="tr-podium-score"><b>' + (post.score || 0) + '</b></div>' +
        '</div>' +
        '<div class="tr-podium-base"><span>' + placeNum + '</span></div>' +
      '</button>';
    };
    podiumHtml = '<div class="tr-podium-wrap">' +
      '<div class="tr-label">' + esc(t('tr.podium')) + '</div>' +
      '<div class="tr-podium">' +
        (top3.second !== null ? step(top3.second, 'silver', t('tr.place2'), 'medal', 2) : '') +
        step(top3.first, 'gold', t('tr.place1'), 'trophy', 1) +
        (top3.third !== null ? step(top3.third, 'bronze', t('tr.place3'), 'medal', 3) : '') +
      '</div>' +
    '</div>';
  }

  const profile = (this.state && this.state.profile) || {};
  const isLiked = ((profile.liked_posts) || []).includes(p.id);
  const likeTitle = isLiked ? t('tr.liked') : t('tr.like');
  const champActions = '<div class="tr-champ-actions">' +
    '<button type="button" class="tr-btn tr-champ-btn tr-like-btn' + (isLiked ? ' liked' : '') + '" data-tr="like" title="' + esc(likeTitle) + '">' +
      icon('heart', 16, isLiked) + ' <span>' + esc(likeTitle) + '</span>' +
    '</button>' +
    '<button type="button" class="tr-btn tr-champ-btn ghost" data-tr="collect" title="' + esc(t('tr.collect')) + '">' +
      icon('folderPlus', 16) + ' <span>' + esc(t('tr.collect')) + '</span>' +
    '</button>' +
    '<button type="button" class="tr-btn tr-champ-btn ghost" data-tr="download" title="' + esc(t('tr.download')) + '">' +
      icon('download', 16) + ' <span>' + esc(t('tr.download')) + '</span>' +
    '</button>' +
  '</div>';

  const kothStat = (tm.mode === 'koth' && tm.koth)
    ? '<p class="tr-final-stat">' + esc(t('tr.bestStreak', { n: tm.koth.bestStreak })) + '</p>'
    : '';

  root.innerHTML = this._trChips() +
    '<div class="tr-final-wrap">' +
      '<i class="tr-rays"></i>' +
      '<div class="tr-card tr-champion">' +
        '<div class="tr-topline">' + esc(t('tr.brand')) + '</div>' +
        '<h2 class="tr-title">' + esc(t('tr.champion')) + '</h2>' +
        '<div class="tr-crown">' + icon('trophy', 20) + '</div>' +
        '<div class="tr-final-img"><img src="' + esc(p.thumb) + '" alt="" decoding="async"></div>' +
        '<div class="tr-score"><b data-tr="score">0</b><span>' + esc(t('tr.booruScore')) + '</span></div>' +
        champActions +
        podiumHtml +
        kothStat +
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

  const likeBtn = root.querySelector('[data-tr="like"]');
  if (likeBtn) likeBtn.addEventListener('click', () => this._trToggleChampionLike(p.id, likeBtn));
  const collectBtn = root.querySelector('[data-tr="collect"]');
  if (collectBtn) collectBtn.addEventListener('click', () => this._trOpenCollectModal(p.id));
  const downloadBtn = root.querySelector('[data-tr="download"]');
  if (downloadBtn) downloadBtn.addEventListener('click', () => this.downloadPost(p));

  root.querySelectorAll('.tr-podium-step').forEach(btn => {
    btn.addEventListener('click', () => this._trOpenPost(parseInt(btn.dataset.idx, 10)));
  });

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
