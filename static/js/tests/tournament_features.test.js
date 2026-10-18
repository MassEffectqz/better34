// tournament_features.test.js — тесты новых возможностей турнира:
// King of the Hill (Царь горы), Топ-3 пьедестал, отмена хода (Undo), выбор медиа высокого разрешения.
import { App } from '../state.js';
await import('../tournament.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeApp(mode = 'bracket') {
  const a = Object.create(App);
  a.els = {
    tournamentRoot: {
      innerHTML: '', classList: { add() {}, remove() {} },
      querySelectorAll: () => [], querySelector: () => null, focus() {},
    },
  };
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', mode, resolved: new Set(), winner: null,
    history: [], koth: null, stats: { played: 0, wins: 0 },
  };
  a.showToast = () => {};
  a.renderTournament = () => {};
  a.renderTournamentFinal = () => {};
  return a;
}

function posts(n) {
  return Array.from({ length: n }, (_, i) => ({
    id: i + 1, score: (i + 1) * 10, tags: 'tag' + (i + 1), thumb: '/t' + (i + 1) + '.jpg',
    sample_url: 'https://cdn.example.com/s' + (i + 1) + '.jpg',
    file_url: 'https://cdn.example.com/f' + (i + 1) + '.jpg', file_type: 'jpg',
    width: 1200, height: 800, rating: 's', downloaded: false, source: 'test',
  }));
}

console.log('Турнир: новые функции (KotH, Топ-3, Undo, Media)\n');

// ── 1. Расчёт Топ-3 (Пьедестал) в режиме сетки ──────────────────────────
{
  const a = makeApp('bracket');
  a._seedTournament(posts(8));
  // Проходим все матчи: выбираем участника с бóльшим индексом
  let match = a._currentMatch();
  while (match) {
    const winnerIdx = Math.max(match.a, match.b);
    a._resolveMatch(match, winnerIdx);
    match = a._currentMatch();
  }

  const top3 = a._trTop3();
  check('Топ-3: есть первое место (чемпион индекс 7)', top3.first === 7, 'first=' + top3.first);
  check('Топ-3: есть второе место (финалист индекс 3)', top3.second === 3, 'second=' + top3.second);
  check('Топ-3: есть полуфиналист (3-е место)', top3.third !== null && top3.third !== top3.first && top3.third !== top3.second, 'third=' + top3.third);
}

// ── 2. Режим «Царь горы» (King of the Hill) ─────────────────────────────
{
  const a = makeApp('koth');
  const pList = posts(5);
  a._seedTournament(pList);

  check('KotH инициализирован', a._tournament.koth !== null);
  check('KotH: Царь изначально индекс 0', a._tournament.koth.kingIdx === 0);
  check('KotH: Претендент изначально индекс 1', a._tournament.koth.challengerIdx === 1);
  check('KotH: Пул претендентов содержит индексы [2, 3, 4]',
    a._tournament.koth.pool.join(',') === '2,3,4', a._tournament.koth.pool.join(','));

  const m1 = a._currentMatch();
  check('KotH: первый матч 0 vs 1', m1 && m1.a === 0 && m1.b === 1);

  // Царь побеждает: streak растёт, царь остаётся, выходит следующий претендент
  a._resolveMatch(m1, 0);
  check('KotH: Царь остался 0', a._tournament.koth.kingIdx === 0);
  check('KotH: серия побед стала 1', a._tournament.koth.currentStreak === 1);
  check('KotH: рекорд серии 1', a._tournament.koth.bestStreak === 1);
  check('KotH: следующий претендент взят из пула (2)', a._tournament.koth.challengerIdx === 2);

  const m2 = a._currentMatch();
  check('KotH: второй матч 0 vs 2', m2 && m2.a === 0 && m2.b === 2);

  // Претендент побеждает: новый царь, серия сбрасывается в 1, рекорд сохраняется
  a._resolveMatch(m2, 2);
  check('KotH: новый Царь — индекс 2', a._tournament.koth.kingIdx === 2);
  check('KotH: серия сброшена в 1 для нового Царя', a._tournament.koth.currentStreak === 1);
  check('KotH: лучший рекорд остался 1', a._tournament.koth.bestStreak === 1);

  // Доигрываем оставшихся претендентов
  const m3 = a._currentMatch();
  a._resolveMatch(m3, 2); // серия 2
  const m4 = a._currentMatch();
  a._resolveMatch(m4, 2); // серия 3

  check('KotH: пул претендентов исчерпан', a._tournament.koth.pool.length === 0);
  check('KotH: матчей больше нет', a._currentMatch() === null);
  check('KotH: победитель турнира — итоговый Царь (2)', a._tournament.winner === 2);
  check('KotH: рекорд серии вырос до 3', a._tournament.koth.bestStreak === 3);
}

// ── 3. Досрочное завершение «Царя горы» (Finish KotH) ────────────────────
{
  const a = makeApp('koth');
  a._seedTournament(posts(6));
  const m = a._currentMatch();
  a._resolveMatch(m, 0); // Царь 0, streak 1
  check('KotH: турнир ещё не закончен', a._tournament.winner === null);

  a.tournamentFinishKoth();
  check('KotH: досрочный финиш устанавливает победителем текущего Царя',
    a._tournament.winner === 0);
}

// ── 4. Отмена хода (Undo) ───────────────────────────────────────────────
{
  const a = makeApp('bracket');
  a._seedTournament(posts(4));

  const m0 = a._currentMatch();
  // В реальной игре перед _resolveMatch делается снимок:
  a._trSaveHistory();
  a._resolveMatch(m0, 1);
  check('После 1 матча в истории 1 снимок', a._tournament.history.length === 1);
  check('Сыгран 1 матч', a._tournament.stats.played === 1);

  // Выполняем Undo
  a.tournamentUndo();
  check('После Undo история пуста', a._tournament.history.length === 0);
  check('Счётчик сыгранных матчей уменьшен до 0', a._tournament.stats.played === 0);
  const revertedM = a._currentMatch();
  check('Текущий матч вернулся к m0 (0 vs 1)', revertedM && revertedM.a === 0 && revertedM.b === 1);
}

// ── 5. Отмена хода в режиме «Царь горы» ──────────────────────────────────
{
  const a = makeApp('koth');
  a._seedTournament(posts(4));
  const m = a._currentMatch();
  a._trSaveHistory();
  a._resolveMatch(m, 1); // Новый царь 1
  check('KotH: Царь стал 1', a._tournament.koth.kingIdx === 1);

  a.tournamentUndo();
  check('KotH Undo: Царь вернулся к 0', a._tournament.koth.kingIdx === 0);
  check('KotH Undo: серия вернулась к 0', a._tournament.koth.currentStreak === 0);
  check('KotH Undo: пул претендентов восстановлен', a._tournament.koth.pool.length === 2);
}

// ── 6. Разрешение медиа-ссылок (_trMediaUrl) ────────────────────────────
{
  const a = makeApp();
  const pOnline = {
    id: 10,
    thumb: 'https://cdn.example.com/thumb.jpg',
    sample_url: 'https://cdn.example.com/sample.jpg',
    downloaded: false,
  };
  const urlOnline = a._trMediaUrl(pOnline);
  check('Для онлайн-поста с sample_url возвращается проксированный sample_url с kind=sample',
    urlOnline === '/api/proxy?url=' + encodeURIComponent('https://cdn.example.com/sample.jpg') + '&kind=sample',
    urlOnline);

  const pDownloaded = {
    id: 11,
    thumb: 'https://cdn.example.com/thumb.jpg',
    sample_url: 'https://cdn.example.com/sample.jpg',
    downloaded: true,
  };
  const urlDownloaded = a._trMediaUrl(pDownloaded);
  check('Для скачанного поста берётся локальный /api/thumb/11',
    urlDownloaded === '/api/thumb/11',
    urlDownloaded);

  const pPreviewOnly = {
    id: 12,
    thumb: 'https://cdn.example.com/thumb.jpg',
    preview_url: 'https://cdn.example.com/preview.jpg',
    downloaded: false,
  };
  const urlPreview = a._trMediaUrl(pPreviewOnly);
  check('Если нет sample_url, но есть preview_url, берётся preview_url с kind=preview',
    urlPreview === '/api/proxy?url=' + encodeURIComponent('https://cdn.example.com/preview.jpg') + '&kind=preview',
    urlPreview);

  const pThumbOnly = {
    id: 13,
    thumb: 'https://cdn.example.com/thumb.jpg',
    downloaded: false,
  };
  const urlThumb = a._trMediaUrl(pThumbOnly);
  check('Если нет других полей, берётся thumb',
    urlThumb === 'https://cdn.example.com/thumb.jpg',
    urlThumb);
}

console.log(`\nИтог: ${passed} ok, ${failed} fail`);
if (failed) process.exit(1);
