'use strict';

const fs = require('fs');
const path = require('path');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const pending = new Map(); 
let rafCounter = 0;
global.requestAnimationFrame = (cb) => { const id = ++rafCounter; pending.set(id, cb); return id; };
global.cancelAnimationFrame = (id) => { pending.delete(id); };
global.window = {};
global.localStorage = { _s: {}, getItem(k) { return this._s[k] != null ? this._s[k] : null; }, setItem(k, v) { this._s[k] = String(v); } };
global.document = {
  addEventListener() {}, removeEventListener() {},
  getElementById() { return null; }, querySelector() { return null; },
  createElement() { return { style: {}, addEventListener() {}, appendChild() {}, innerHTML: '', querySelector() { return null; } }; },
};
function icon(name, size, solid) { return `<svg data-icon="${name}"></svg>` + (solid ? '-solid' : ''); }
global.icon = icon;

const App = {};
global.App = App;
const viewerPath = path.join(__dirname, '..', 'viewer.js');
eval(fs.readFileSync(viewerPath, 'utf8'));

App.state = { viewerOpen: true };
App.els = {
  viewerContent: {
    clientWidth: 1000, clientHeight: 800,
    parentElement: { clientWidth: 1000, clientHeight: 800 },
    querySelector: () => ({ naturalWidth: 1600, naturalHeight: 1200 }),
  },
};
let panCount = 0;
App.panBy = function () { panCount++; };

function runOneFrame() { 
  for (const [id, cb] of Array.from(pending.entries())) { pending.delete(id); cb(); return; }
}
function flushAll() { while (pending.size) runOneFrame(); }
function reset() {
  App._cancelPan();
  App._zoomActive = true; App._zoomScale = 2; App._zoomTx = 0; App._zoomTy = 0;
  App.state.viewerOpen = true; 
  panCount = 0;
  flushAll();
}

console.log('pan tests\n');

reset();
App.startPan(1, 0);
runOneFrame();
check('startPan(Right) запускает rAF-цикл', pending.size > 0, 'rAF not scheduled');
check('velocity.x == 1 после startPan(Right)', App._panVel.x === 1, JSON.stringify(App._panVel));

reset();
App.startPan(1, 0);
App.startPan(-1, 0);
App.stopPan(App.DIR.LEFT);
check('после tap Left ось x остаётся 1 (Right ещё зажата)', App._panVel.x === 1, JSON.stringify(App._panVel));
check('панорамирование продолжается (rAF активен)', pending.size > 0);
App.stopPan(App.DIR.RIGHT);
runOneFrame();
check('после отпускания всех клавиш цикл останавливается', pending.size === 0, `${pending.size} pending frames`);

reset();
App.startPan(0, -1); App.stopPan(App.DIR.UP);
App.startPan(0, 1); App.stopPan(App.DIR.DOWN);
check('быстрые вертикальные тапы дают vy==0', App._panVel.y === 0, JSON.stringify(App._panVel));
check('и цикл остановлен', pending.size === 0);

reset();
App.startPan(1, 0); App.startPan(0, 1);
check('диагональ даёт x=1, y=1', App._panVel.x === 1 && App._panVel.y === 1, JSON.stringify(App._panVel));
App.stopPan(App.DIR.DOWN); App.stopPan(App.DIR.RIGHT);

reset();
App.startPan(1, 0);
App._zoomActive = false;
runOneFrame();
check('зум выключен → цикл останавливается', pending.size === 0, `${pending.size} pending`);
App.stopPan(App.DIR.RIGHT);

reset();
App._zoomActive = false;
App.startPan(1, 0);
check('startPan без зума не создаёт скорость', !App._panVel || (App._panVel.x === 0 && App._panVel.y === 0));
check('startPan без зума не запускает rAF', pending.size === 0);

reset();
App.startPan(0, 1);
App.state.viewerOpen = false;
App._cancelPan();
check('_cancelPan останавливает rAF', pending.size === 0);
check('_cancelPan обнуляет скорость', (!App._panVel || (App._panVel.x === 0 && App._panVel.y === 0)));
check('_cancelPan очищает зажатые клавиши', !App._panKeys || App._panKeys.size === 0);

reset();
App.startPan(1, 0);
const before = panCount;
runOneFrame();
check('каждый кадр вызывает panBy', panCount > before, `panCount=${panCount}`);
App.stopPan(App.DIR.RIGHT);
flushAll();

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed ? 1 : 0);
