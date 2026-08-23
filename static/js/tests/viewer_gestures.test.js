'use strict';

const fs = require('fs');
const path = require('path');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

class FakeEl {
  constructor() {
    this.listeners = {};
    this.classList = {
      _s: new Set(),
      add(c) { this._s.add(c); },
      remove(c) { this._s.delete(c); },
      toggle(c, f) { if (f === undefined) { if (this._s.has(c)) this._s.delete(c); else this._s.add(c); } else if (f) this._s.add(c); else this._s.delete(c); },
      contains(c) { return this._s.has(c); },
    };
    this.style = {};
  }
  addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); }
  removeEventListener(t, fn) { if (this.listeners[t]) this.listeners[t] = this.listeners[t].filter(f => f !== fn); }
  fire(t, ev) { (this.listeners[t] || []).forEach(fn => fn(ev)); }
  querySelector() { return null; }
  closest() { return this; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 1000, height: 800 }; }
  setPointerCapture() {}
  appendChild() {}
}

global.window = {};
global.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
global.document = { addEventListener() {}, removeEventListener() {} };
function icon(name, size, solid) { return `<svg data-icon="${name}"></svg>` + (solid ? '-solid' : ''); }
global.icon = icon;

const App = {};
global.App = App;
const viewerPath = path.join(__dirname, '..', 'viewer.js');
eval(fs.readFileSync(viewerPath, 'utf8'));
// video.js добавляет видео-методы на App (renderViewer их вызывает).
eval(fs.readFileSync(path.join(__dirname, '..', 'video.js'), 'utf8'));

const wrap = new FakeEl();
const vc = new FakeEl();
const img = new FakeEl();
img.naturalWidth = 1600; img.naturalHeight = 1200;
vc.querySelector = (sel) => (sel.includes('img') ? img : null);
vc.clientWidth = 1000; vc.clientHeight = 800;
vc.parentElement = { clientWidth: 1000, clientHeight: 800 };
const viewer = new FakeEl();
const backdrop = new FakeEl();
viewer.querySelector = () => backdrop;

App.els = { viewer, viewerContent: vc, viewerContainer: wrap };
App.state = {
  viewerOpen: true, viewerIndex: 1, posts: [{ id: 1 }, { id: 2 }, { id: 3 }],
  relatedChain: false, query: '', hasMore: true, loading: false,
};
App._ctxStack = [];
App._nextVisibleIndex = (from, dir) => {
  const n = from + dir;
  return (n >= 0 && n < App.state.posts.length) ? n : -1;
};
App._navCalls = [];
App.navigateViewer = function (dir) { App._navCalls.push(dir); };
App._closed = false;
App.closeViewer = function () { App._closed = true; };
App._cancelInertia = function () {};
App._startPanInertia = function () {};
App.panBy = function () {};

App.initViewerTouch();

const ev = (x, y, id) => ({
  pointerId: id != null ? id : 1, clientX: x, clientY: y,
  pointerType: 'touch', button: 0, preventDefault() {},
});
const tap = (x, y, id) => { wrap.fire('pointerdown', ev(x, y, id)); wrap.fire('pointerup', ev(x, y, id)); };
const sleep = (ms) => new Promise(r => setTimeout(r, ms));

async function main() {
  console.log('viewer gesture tests\n');

  App.state.viewerIndex = 1;
  wrap.fire('pointerdown', ev(100, 100));
  wrap.fire('pointermove', ev(300, 100));
  check('drag: изображение следует за пальцем', img.style.transform === 'translate(200px, 0px)', img.style.transform);
  wrap.fire('pointerup', ev(300, 100));
  check('drag: свайп вправо → предыдущий пост', App._navCalls[App._navCalls.length - 1] === -1, JSON.stringify(App._navCalls));

  App.state.viewerIndex = 0;
  wrap.fire('pointerdown', ev(100, 100));
  wrap.fire('pointermove', ev(300, 100));
  check('edge: на первом посте свайп вправо получает резистенцию 0.3', img.style.transform === 'translate(60px, 0px)', img.style.transform);
  const navBefore = App._navCalls.length;
  wrap.fire('pointerup', ev(300, 100));
  check('edge: пост не сменился', App._navCalls.length === navBefore, JSON.stringify(App._navCalls));

  App.state.viewerIndex = 1;
  wrap.fire('pointerdown', ev(100, 100));
  wrap.fire('pointermove', ev(100, 300));
  check('close-sheet: свайп вниз двигает картинку с фактором 0.55', /translate\(0px, 11[01](\.\d+)?px\)/.test(img.style.transform), img.style.transform);
  wrap.fire('pointerup', ev(100, 300));
  check('close-sheet: свайп вниз закрывает просмотрщик', App._closed, 'not closed');

  App._closed = false;
  App.state.viewerIndex = 1;
  wrap.fire('pointerdown', ev(100, 100));
  await sleep(150);
  wrap.fire('pointermove', ev(100, 160));
  wrap.fire('pointerup', ev(100, 160));
  check('короткий свайп вниз не закрывает', !App._closed, 'closed');
  check('короткий свайп возвращает transform', img.style.transform === 'none', img.style.transform);

  tap(500, 400);
  await sleep(350);
  check('тап скрывает HUD', viewer.classList.contains('viewer-hud-hidden'), 'hud visible');
  tap(500, 400);
  await sleep(350);
  check('второй тап показывает HUD', !viewer.classList.contains('viewer-hud-hidden'), 'hud hidden');

  tap(500, 400);
  tap(500, 400);
  check('дабл-тап включает зум', !!App._zoomActive, 'zoom not active');
  check('дабл-тап зум до ~2x', Math.abs((App._zoomScale || 0) - 2) < 0.01, 'scale=' + App._zoomScale);
  tap(500, 400);
  tap(500, 400);
  check('дабл-тап в зуме выключает зум', !App._zoomActive, 'zoom still active');

  App._navCalls.length = 0;
  wrap.fire('pointerdown', ev(100, 100));
  wrap.fire('pointermove', ev(300, 100));
  wrap.fire('pointercancel', ev(300, 100));
  check('pointercancel сбрасывает жест без навигации', App._navCalls.length === 0, JSON.stringify(App._navCalls));
  check('pointercancel возвращает transform', img.style.transform === 'none', img.style.transform);

  App._navCalls.length = 0;
  App.state.viewerIndex = 2;
  App.state.hasMore = true;
  App.state.loading = false;
  wrap.fire('pointerdown', ev(700, 100));
  wrap.fire('pointermove', ev(300, 100));
  wrap.fire('pointerup', ev(300, 100));
  check('edge+hasMore: свайп на последнем посте вызывает navigateViewer(1) → loadMore', App._navCalls[App._navCalls.length - 1] === 1, JSON.stringify(App._navCalls));

  App._navCalls.length = 0;
  App.state.hasMore = false;
  wrap.fire('pointerdown', ev(700, 100));
  wrap.fire('pointermove', ev(300, 100));
  wrap.fire('pointerup', ev(300, 100));
  check('edge+hasMore=false: свайп на последнем посте не вызывает навигацию', App._navCalls.length === 0, JSON.stringify(App._navCalls));

  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(failed ? 1 : 0);
}

main();