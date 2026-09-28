// feed_swipe.test.js — свайп по карточке в гриде на телефоне:
//  * горизонтальный свайп открывает пост (а не «тап с задержкой»);
//  * если вьювер уже открыт — листает по соседним постам;
//  * вертикальный жест остаётся прокруткой и ничего не открывает;
//  * досланный после свайпа click не открывает пост повторно.
import { App } from '../state.js';
await import('../feed.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// Минимальная карточка: умеет только отдавать зарегистрированные слушатели.
function makeCard() {
  const ls = {};
  return {
    ls,
    addEventListener(type, fn) { (ls[type] = ls[type] || []).push(fn); },
    fire(type, ev) { (ls[type] || []).forEach((fn) => fn(ev)); },
  };
}
function touch(x, y) { return { touches: [{ clientX: x, clientY: y }] }; }
function swipe(card, dx, dy) {
  card.fire('touchstart', touch(200, 200));
  card.fire('touchmove', touch(200 + dx, 200 + dy));
  card.fire('touchend', { changedTouches: [{ clientX: 200 + dx, clientY: 200 + dy }] });
}

function makeApp() {
  const a = Object.create(App);
  a.state = { viewerOpen: false, posts: [] };
  a._isTouch = () => true;
  a.opened = [];
  a.navigated = [];
  a.openViewer = function (i) { a.opened.push(i); a.state.viewerOpen = true; };
  a.navigateViewer = function (d) { a.navigated.push(d); };
  return a;
}

console.log('Свайп в гриде\n');

// ── 1. Свайп влево открывает пост ────────────────────────────────────────
{
  const a = makeApp();
  const card = makeCard();
  a._bindCardSwipe(card, () => 3);
  swipe(card, -120, 10);
  check('свайп влево открывает пост с карточки',
    a.opened.length === 1 && a.opened[0] === 3, JSON.stringify(a.opened));
  check('свайп влево задаёт направление «вперёд» (1)',
    a._swipeDir === 1, String(a._swipeDir));
}

// ── 2. Свайп вправо открывает пост с направлением «назад» ────────────────
{
  const a = makeApp();
  const card = makeCard();
  a._bindCardSwipe(card, () => 7);
  swipe(card, 120, 10);
  check('свайп вправо задаёт направление «назад» (-1)',
    a._swipeDir === -1, String(a._swipeDir));
  check('свайп вправо открывает тот же пост', a.opened[0] === 7, JSON.stringify(a.opened));
}

// ── 3. Вертикальный жест — это прокрутка, а не свайп ─────────────────────
{
  const a = makeApp();
  const card = makeCard();
  a._bindCardSwipe(card, () => 2);
  swipe(card, 10, -150);
  check('вертикальный свайп не открывает пост', a.opened.length === 0, JSON.stringify(a.opened));
}

// ── 4. Диагональ под 45° — ещё не свайп ──────────────────────────────────
{
  const a = makeApp();
  const card = makeCard();
  a._bindCardSwipe(card, () => 2);
  swipe(card, 60, -60);
  check('диагональный жест не считается свайпом', a.opened.length === 0, JSON.stringify(a.opened));
}

// ── 5. Короткий «тычок» — тоже не свайп ──────────────────────────────────
{
  const a = makeApp();
  const card = makeCard();
  a._bindCardSwipe(card, () => 2);
  swipe(card, -15, 2);
  check('слишком короткое движение не считается свайпом', a.opened.length === 0, JSON.stringify(a.opened));
}

// ── 6. При открытом вьювере свайп листает, а не открывает заново ─────────
{
  const a = makeApp();
  a.state.viewerOpen = true;
  const card = makeCard();
  a._bindCardSwipe(card, () => 5);
  swipe(card, -120, 5);
  check('свайп при открытом вьювере листает вперёд',
    a.navigated.length === 1 && a.navigated[0] === 1, JSON.stringify(a.navigated));
  check('и не открывает пост заново', a.opened.length === 0, JSON.stringify(a.opened));
}

// ── 7. Досланный после свайпа click не открывает пост повторно ───────────
{
  const a = makeApp();
  const card = makeCard();
  a._bindCardSwipe(card, () => 4);
  swipe(card, -120, 5);
  check('после свайпа click подавлен', a._suppressCardClick === true, String(a._suppressCardClick));
  await new Promise((r) => setTimeout(r, 400));
  check('подавление снимается само (click снова работает)',
    a._suppressCardClick === false, String(a._suppressCardClick));
}

// ── 8. На десктопе жесты не навешиваются вовсе ───────────────────────────
{
  const a = makeApp();
  a._isTouch = () => false;
  const card = makeCard();
  a._bindCardSwipe(card, () => 1);
  check('на десктопе слушатели не добавляются',
    Object.keys(card.ls).length === 0, Object.keys(card.ls).join(','));
}

console.log(`\nИтог: ${passed} ok, ${failed} fail`);
if (failed) process.exit(1);
