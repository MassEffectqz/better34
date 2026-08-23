// zz_adversarial_viewer_loader.test.js — F1: повторный рендер не перезагружает медиа.
import { App } from '../state.js';
await import('../viewer.js');
await import('../video.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeClassList(initial) {
  const s = new Set(initial || []);
  return {
    contains(c) { return s.has(c); },
    add(c) { s.add(c); },
    remove(c) { s.delete(c); },
    toggle(c, force) {
      const on = force != null ? !!force : !s.has(c);
      if (on) s.add(c); else s.delete(c);
      return on;
    },
  };
}

function makeEl() {
  return {
    style: {}, classList: makeClassList(), children: [],
    innerHTML: '', textContent: '',
    appendChild(c) { this.children.push(c); },
    addEventListener() {},
    querySelector() { return makeEl(); },
    remove() {},
  };
}

globalThis.window = { innerWidth: 1400, innerHeight: 800, location: { href: 'http://localhost/' } };
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = {
  addEventListener() {}, removeEventListener() {},
  getElementById() { return null; },
  querySelector() { return null; },
  createElement: () => makeEl(),
  createDocumentFragment: () => ({ children: [], appendChild(c) { this.children.push(c); } }),
};
globalThis.requestAnimationFrame = () => 1;
globalThis.cancelAnimationFrame = () => {};

console.log('viewer loader (F1) tests\n');

// Браузер превращает относительный src в АБСОЛЮТНЫЙ URL (mediaEl.src),
// поэтому условие `mediaEl.src !== fileUrl` в renderViewer ВСЕГДА истинно:
// при повторном рендере того же поста лоадер снова показывается, а картинка
// перезагружается (src присваивается заново) + копятся once-листенеры.
function makeApp() {
  const a = Object.create(App);
  const loader = { classList: makeClassList() };
  let srcAssigns = 0, loadListeners = 0;
  const img = {
    tagName: 'IMG',
    alt: '', draggable: false,
    style: {}, classList: makeClassList(),
    naturalWidth: 100, naturalHeight: 100,
    get src() { return 'http://localhost' + (this._src || ''); },
    set src(v) { this._src = v; srcAssigns++; },
    addEventListener(ev, fn, opts) { if (ev === 'load' || ev === 'error') loadListeners++; },
  };
  const viewerContent = {
    classList: makeClassList(),
    innerHTML: '',
    clientWidth: 1000, clientHeight: 800,
    parentElement: { clientWidth: 1000, clientHeight: 800, appendChild() {} },
    appendChild() {}, removeChild() {},
    querySelector(sel) { return sel.indexOf('slideshow-controls') >= 0 ? null : img; },
  };
  a.state = {
    posts: [{ id: 1, file_type: 'jpg', file_url: 'https://cdn.example/1.jpg', downloaded: false }],
    viewerIndex: 0, viewerOpen: true,
    profile: { liked_posts: [], hidden_tags: [], fav_tags: [] },
    slideshowSpeed: 5000,
  };
  a.els = {
    viewerContent,
    viewerInfo: { textContent: '' },
    viewerProgress: { textContent: '' },
    viewerLoader: loader,
    viewerLike: { innerHTML: '' },
    viewerLikeM: null,
    viewerTags: { innerHTML: '' },
    ssSpeedInput: { value: '' },
    prevBtn: { style: {} }, nextBtn: { style: {} },
  };
  a.updateNavButtons = function () {};
  a.applyZoomTransform = function () {};
  a._viewerIsFullscreen = function () { return false; };
  a._tagCounts = {};
  a._saveTagCounts = function () {};
  a._zoomScale = 1; a._zoomActive = false; a._zoomTx = 0; a._zoomTy = 0;
  a._zoomMetrics = () => ({ W: 1000, H: 800, fw: 100, fh: 100 });
  a._updateZoomHud = function () {};
  a._updateZoomLabel = function () {};
  a.toggleViewerZoom = function () {};
  return { a, img, loader, stats: () => ({ srcAssigns, loadListeners }) };
}

const first = makeApp();
first.a.renderViewer();
check('первый рендер показывает лоадер', first.loader.classList.contains('active'), 'loader должен быть active');
check('первый рендер выставляет src картинки', first.stats().srcAssigns === 1, 'srcAssigns=' + first.stats().srcAssigns);

first.a.renderViewer();
check('F1: повторный рендер ТОГО ЖЕ поста не должен показывать лоадер',
  !first.loader.classList.contains('active'),
  'loader active после повторного рендера — src(абсолютный) !== fileUrl(относительный) всегда истинно');
check('F1: повторный рендер не должен переустанавливать src (не перезагружать картинку)',
  first.stats().srcAssigns === 1, 'srcAssigns=' + first.stats().srcAssigns);
check('F1: повторный рендер не должен добавлять новые load/error-листенеры',
  first.stats().loadListeners === 2, 'loadListeners=' + first.stats().loadListeners);

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);