// viewer_preview_placeholder.test.js — проверка отображения превью во вьювере во время загрузки.
import { App } from '../state.js';
await import('../viewer.js');
await import('../viewer_preload.js');
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

function makeEl(tag = 'div') {
  const el = {
    tagName: tag.toUpperCase(),
    style: {},
    classList: makeClassList(),
    children: [],
    innerHTML: '',
    textContent: '',
    id: '',
    className: '',
    listeners: {},
    appendChild(c) {
      this.children.push(c);
      c.parentElement = this;
    },
    insertBefore(c, ref) {
      const idx = this.children.indexOf(ref);
      if (idx >= 0) this.children.splice(idx, 0, c);
      else this.children.push(c);
      c.parentElement = this;
    },
    removeChild(c) {
      const idx = this.children.indexOf(c);
      if (idx >= 0) this.children.splice(idx, 1);
      c.parentElement = null;
    },
    remove() {
      if (this.parentElement) this.parentElement.removeChild(this);
    },
    querySelector(sel) {
      if (sel === '.viewer-loader') return this.children.find(c => c.classList.contains('viewer-loader') || c.id === 'viewer-loader') || null;
      if (sel === '#viewer-preview' || sel === '.viewer-preview') return this.children.find(c => c.id === 'viewer-preview' || c.classList.contains('viewer-preview')) || null;
      if (sel === 'img') return this.children.find(c => c.tagName === 'IMG') || null;
      if (sel === 'img, video') return this.children.find(c => c.tagName === 'IMG' || c.tagName === 'VIDEO') || null;
      return null;
    },
    querySelectorAll(sel) {
      return this.children.filter(c => {
        if (sel === '.viewer-orig') return c.classList.contains('viewer-orig');
        return false;
      });
    },
    addEventListener(ev, fn, _opts) {
      this.listeners[ev] = this.listeners[ev] || [];
      this.listeners[ev].push(fn);
    },
    dispatchEvent(ev) {
      const list = this.listeners[ev] || [];
      this.listeners[ev] = [];
      list.forEach(fn => fn());
    },
  };
  return el;
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
  createElement: (tag) => makeEl(tag),
  createDocumentFragment: () => makeEl('fragment'),
};

console.log('viewer preview placeholder tests\n');

// 1. Тест _postThumbUrl
check('_postThumbUrl: скачанный пост с миниатюрой',
  App._postThumbUrl({ id: 10, downloaded: true, thumb_path: '10.jpg' }) === '/api/thumb/10');
check('_postThumbUrl: удалённый пост оборачивается в proxy с kind=preview',
  App._postThumbUrl({ id: 11, preview_url: 'https://cdn.example/p.jpg' }) === '/api/proxy?url=https%3A%2F%2Fcdn.example%2Fp.jpg&kind=preview');
check('_postThumbUrl: уже проксированный URL не проксируется повторно',
  App._postThumbUrl({ id: 12, preview_url: '/api/proxy?url=test&kind=preview' }) === '/api/proxy?url=test&kind=preview');
check('_postThumbUrl: пост без preview_url берет sample_url',
  App._postThumbUrl({ id: 13, sample_url: 'https://cdn.example/s.jpg' }) === '/api/proxy?url=https%3A%2F%2Fcdn.example%2Fs.jpg');

// 2. Тест renderViewer: создание превью во время загрузки изображения
const a = Object.create(App);
const loader = makeEl('div');
loader.id = 'viewer-loader';
loader.classList.add('viewer-loader');

const viewerContent = makeEl('div');
viewerContent.appendChild(loader);

a.state = {
  posts: [
    { id: 101, file_type: 'jpg', file_url: 'https://cdn.example/101.jpg', preview_url: 'https://cdn.example/p101.jpg', downloaded: false },
    { id: 102, file_type: 'jpg', file_url: 'https://cdn.example/102.jpg', preview_url: 'https://cdn.example/p102.jpg', downloaded: false },
  ],
  viewerIndex: 0,
  viewerOpen: true,
  profile: { liked_posts: [], hidden_tags: [], fav_tags: [] },
  slideshowSpeed: 5000,
};

a.els = {
  viewerContent,
  viewerInfo: makeEl('span'),
  viewerProgress: makeEl('div'),
  viewerLoader: loader,
  viewerLike: makeEl('button'),
  viewerLikeM: null,
  viewerTags: makeEl('div'),
  ssSpeedInput: { value: '' },
  prevBtn: { style: {} }, nextBtn: { style: {} },
};
a.updateNavButtons = () => {};
a.applyZoomTransform = () => {};
a._viewerIsFullscreen = () => false;

// Первый рендер поста 101
a.renderViewer();

const preview = viewerContent.querySelector('#viewer-preview');
check('превью создано во время загрузки', !!preview, 'preview missing');
check('контейнер получил класс image-loading', viewerContent.classList.contains('image-loading'), 'image-loading missing');
check('лоадер активен', loader.classList.contains('active'), 'loader not active');
check('фон превью содержит thumbnail URL', preview && preview.style.backgroundImage.includes('https%3A%2F%2Fcdn.example%2Fp101.jpg'), preview?.style.backgroundImage);

const img = viewerContent.querySelector('img');
check('основное изображение создано', !!img);
check('основное изображение скрыто классом loading пока грузится', img && img.classList.contains('loading'));

// Эмуляция завершения загрузки изображения
img.dispatchEvent('load');

check('после загрузки image-loading снят', !viewerContent.classList.contains('image-loading'));
check('после загрузки класс loading снят с картинки', !img.classList.contains('loading'));
check('после загрузки добавлен класс fade-in', img.classList.contains('fade-in'));
check('лоадер скрыт после загрузки', !loader.classList.contains('active'));
check('превью помечено fade-out после загрузки', preview.classList.contains('fade-out'));

// Переключение на пост 102
a.state.viewerIndex = 1;
a.renderViewer();

const preview2 = viewerContent.querySelector('#viewer-preview');
check('для нового поста создано новое превью', !!preview2);
check('новое превью содержит thumbnail URL нового поста', preview2 && preview2.style.backgroundImage.includes('https%3A%2F%2Fcdn.example%2Fp102.jpg'));
check('контейнер снова получил image-loading', viewerContent.classList.contains('image-loading'));

console.log('\n' + passed + ' passed, ' + failed + ' failed');
if (failed) throw new Error(`${failed} checks failed`);