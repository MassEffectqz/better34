// tournament.test.js — мини-игра «выбери лучшее»: геометрия сетки и разыгрывание.
// Логика сетки тут главное: баг «проверяем только первый уровень» обрывает
// турнир после первого круга, и снаружи это выглядит как «игра не работает».
import { App } from '../state.js';
await import('../tournament.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeApp() {
  const a = Object.create(App);
  // stats и resolved — свежие объекты на каждый экземпляр: иначе два теста
  // подряд делили бы счётчик и результат первого маскировал бы второй.
  a._tournament = {
    open: true, loading: false, posts: null, bracket: [], rounds: 3,
    source: 'offline', resolved: new Set(), winner: null, stats: { played: 0, wins: 0 },
  };
  return a;
}

// size участников -> посты с заведомо возрастающей оценкой буры.
function posts(n) {
  return Array.from({ length: n }, (_, i) => ({
    id: i + 1, score: (i + 1) * 10, tags: 'a b', thumb: '/t.jpg',
    file_url: '/f.jpg', file_type: 'jpg', width: 1, height: 1,
    rating: 's', downloaded: true, source: 'test',
  }));
}

console.log('Турнирная сетка\n');

// ── 1. Сетка строится правильной формы ──────────────────────────────────
{
  const a = makeApp();
  a._seedTournament(posts(8));
  check('уровней ровно rounds', a._tournament.bracket.length === 3, String(a._tournament.bracket.length));
  check('в первом круге 4 пары', a._tournament.bracket[0].length === 4, String(a._tournament.bracket[0].length));
  check('в финале 1 пара', a._tournament.bracket[2].length === 1, String(a._tournament.bracket[2].length));
  check('все 8 участников попали в первый круг',
    a._tournament.bracket[0].flat().join(',') === '0,1,2,3,4,5,6,7',
    a._tournament.bracket[0].flat().join(','));
}

// ── 2. Сетка на 4 и на 16 участников ────────────────────────────────────
{
  for (const [rounds, size] of [[2, 4], [4, 16]]) {
    const a = makeApp();
    a._tournament.rounds = rounds;
    a._seedTournament(posts(size));
    check(`сетка на ${size}: уровней ${rounds}`,
      a._tournament.bracket.length === rounds, String(a._tournament.bracket.length));
    check(`сетка на ${size}: пар первого круга ${size / 2}`,
      a._tournament.bracket[0].length === size / 2, String(a._tournament.bracket[0].length));
  }
}

// ── 3. КЛЮЧЕВОЕ: турнир проходит ВСЕ круги, а не только первый ──────────
{
  const a = makeApp();
  a._seedTournament(posts(8));
  // Всегда выбираем участника с максимальным score — он обязан дойти до финала.
  let guard = 0;
  let match = a._currentMatch();
  while (match && guard++ < 20) {
    const best = a._tournament.posts[match.a].score >= a._tournament.posts[match.b].score ? match.a : match.b;
    a._resolveMatch(match, best);
    match = a._currentMatch();
  }
  check('турнир доигран ровно за size-1 матчей',
    a._tournament.stats.played === 7, String(a._tournament.stats.played));
  check('после последнего матча матчей нет', a._currentMatch() === null, String(a._currentMatch()));
  check('победитель — лучший по score (индекс 7)', a._tournament.winner === 7, String(a._tournament.winner));
  check('защита от зацикливания сработала', guard < 20, String(guard));
}

// ── 4. Победитель проходит именно по ветке своей пары ──────────────────
{
  const a = makeApp();
  a._seedTournament(posts(8));
  // Первый матч — пара 0, участники с индексами 0 и 1. Выбираем индекс 1.
  // Он обязан оказаться в ПАРЕ 0 второго круга, слот 0 (левый).
  const m0 = a._currentMatch();
  check('первый матч — пара 0 первого круга',
    m0 && m0.round === 0 && m0.pair === 0 && m0.a === 0 && m0.b === 1,
    m0 ? JSON.stringify(m0) : 'null');
  a._resolveMatch(m0, 1);
  check('победитель пары 0 идёт в слот 0 пары 0 второго круга',
    a._tournament.bracket[1][0][0] === 1, JSON.stringify(a._tournament.bracket[1][0]));
  check('слот 1 пары 0 второго круга ещё пуст (пара не готова)',
    a._tournament.bracket[1][0][1] === -1, String(a._tournament.bracket[1][0][1]));
  // Пара 0 второго круга не готова: один слот занят — играем следующий матч
  // ПЕРВОГО круга, а не второй круг.
  const m1 = a._currentMatch();
  check('после одного матча играем ещё матч первого круга',
    m1 && m1.round === 0 && m1.pair === 1, m1 ? `round=${m1.round} pair=${m1.pair}` : 'null');
}

// ── 5. Счётчик совпадений с оценкой буры ────────────────────────────────
// posts(4) с оценками 10,20,30,40; первый матч — пара 0, участники 0 и 1.
{
  const a = makeApp();
  a._tournament.rounds = 2; // сетка на 4
  a._seedTournament(posts(4));
  const m = a._currentMatch();
  check('первый матч пары 0: участники 0 и 1', m.a === 0 && m.b === 1, JSON.stringify(m));
  // 20 >= 10 — выбираем более слабого участника.
  a._resolveMatch(m, 0);
  check('выбор более слабого не засчитан как совпадение',
    a._tournament.stats.wins === 0, 'wins=' + a._tournament.stats.wins);
  check('матч засчитан как сыгранный', a._tournament.stats.played === 1,
    'played=' + a._tournament.stats.played);

  const a2 = makeApp();
  a2._tournament.rounds = 2;
  a2._seedTournament(posts(4));
  const m2 = a2._currentMatch();
  a2._resolveMatch(m2, 1); // индекс 1, score 20 — лучший
  check('выбор лучшего засчитан как совпадение',
    a2._tournament.stats.wins === 1, 'wins=' + a2._tournament.stats.wins);
}

// ── 6. Повторное разрешение того же матча не считается дважды ───────────
{
  const a = makeApp();
  a._seedTournament(posts(8));
  const m = a._currentMatch();
  a._resolveMatch(m, m.a);
  a._resolveMatch(m, m.b); // «гонка двух кликов»
  check('повторный клик по сыгранному матчу не увеличивает счётчик',
    a._tournament.stats.played === 1, 'played=' + a._tournament.stats.played);
}

// ── 7. Смена раунда сбрасывает состояние ────────────────────────────────
{
  const a = makeApp();
  a._tournament.rounds = 4;
  a._seedTournament(posts(16));
  a._resolveMatch(a._currentMatch(), 0);
  a._tournament.rounds = 2;
  a._seedTournament(posts(4));
  check('после новой сетки счётчик обнулён',
    a._tournament.stats.played === 0, String(a._tournament.stats.played));
  check('победитель сброшен', a._tournament.winner === null, String(a._tournament.winner));
  check('уровней снова 2', a._tournament.bracket.length === 2, String(a._tournament.bracket.length));
  check('resolved очищен — первый матч снова доступен',
    a._currentMatch() !== null && a._currentMatch().round === 0);
}

console.log(`\nИтог: ${passed} ok, ${failed} fail`);
if (failed) process.exit(1);
