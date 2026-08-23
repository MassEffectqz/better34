// fullscreen_skip_reqs.test.js — F-режим отменяет запросы похожих/счётчиков.
import { App } from '../state.js';
import { API } from '../api.js';
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

globalThis.window = {};
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = {
  addEventListener() {}, removeEventListener() {},
  getElementById() { return null; },
  createElement() {
    return {
      style: {}, classList: makeClassList(), children: [],
      innerHTML: '', textContent: '',
      appendChild(c) { this.children.push(c); },
      addEventListener() {},
      querySelector() { return null; },
      remove() {},
    };
  },
  body: { style: {} },
};
globalThis.requestAnimationFrame = () => 1;
globalThis.cancelAnimationFrame = () => {};

// Патчим методы РЕАЛЬНОГО API (модули зовут API.get(...) через свойство).
const API_CALLS = [];
API.get = async function (url, opts) {
  API_CALLS.push(url);
  if (opts && opts.signal && opts.signal.aborted) {
    const e = new Error('Aborted');
    e.name = 'AbortError';
    throw e;
  }
  return {};
};
API.post = async function () { return {}; };
API.invalidate = function () {};

function makeViewerEl() {
  return {
    classList: makeClassList(),
    querySelector: () => null,
    appendChild() {}, innerHTML: '',
  };
}

function makeApp() {
  const a = Object.create(App);
  a.state = {
    posts: [{ id: 111, tags: '3girls akita_neru bikini', file_type: 'image', width: 100, height: 100 }],
    viewerIndex: 0, viewerOpen: true,
    profile: { fav_tags: [], hidden_tags: [] },
  };
  a.els = {
    viewerContent: makeViewerEl(),
    viewerTags: makeViewerEl(),
    viewerRelated: { classList: makeClassList(['hidden']), innerHTML: '' },
    viewerHead: { style: {} }, viewerFoot: { style: {} },
    prevBtn: { style: {} }, nextBtn: { style: {} },
    viewer: { classList: makeClassList() },
  };
  a.updateNavButtons = function () {};
  a.applyZoomTransform = function () {};
  a._saveTagCounts = function () {};
  a._tagCounts = {};
  return a;
}

const flush = () => new Promise(res => setTimeout(res, 30));
(async () => {
  console.log('fullscreen skip-requests tests\n');

  // 1. scheduleRelated in fullscreen: no timer, no request
  {
    const a = makeApp();
    API_CALLS.length = 0;
    a._viewerIsFullscreen = () => true;
    a.scheduleRelated(a.state.posts[0]);
    check('scheduleRelated в полном экране не запускает таймер/запрос',
      !a._relTimer && !API_CALLS.some(u => u.includes('/related')), '(timer/requests none)');
  }

  // 2. loadRelated in fullscreen: no /related request
  {
    const a = makeApp();
    API_CALLS.length = 0;
    a._viewerIsFullscreen = () => true;
    a.loadRelated(a.state.posts[0]);
    await flush();
    check('loadRelated в полном экране не вызывает /related',
      !API_CALLS.some(u => u.includes('/related')), JSON.stringify(API_CALLS));
  }

  // 3. renderViewerTags in fullscreen: no /tag-counts request
  {
    const a = makeApp();
    API_CALLS.length = 0;
    a._viewerIsFullscreen = () => true;
    a._renderViewerTags();
    await flush();
    check('_renderViewerTags в полном экране не вызывает /tag-counts',
      !API_CALLS.some(u => u.includes('/tag-counts')), JSON.stringify(API_CALLS));
  }

  // 4. renderViewerTags outside fullscreen: /tag-counts requested
  {
    const a = makeApp();
    API_CALLS.length = 0;
    a._viewerIsFullscreen = () => false;
    a._renderViewerTags();
    await flush();
    check('_renderViewerTags вне полного экрана вызывает /tag-counts',
      API_CALLS.some(u => u.includes('/tag-counts')), JSON.stringify(API_CALLS));
  }

  // 5. toggleFullscreen enter: aborts inflight requests and bumps related token
  {
    const a = makeApp();
    let relAborted = false, cntAborted = false;
    a._relAbort = { abort() { relAborted = true; } };
    a._countAbort = { abort() { cntAborted = true; } };
    a._relToken = 5;
    a.toggleFullscreen();
    check('toggleFullscreen enter отменяет в-полёте запрос похожих', relAborted);
    check('toggleFullscreen enter отменяет в-полёте запрос счётчиков', cntAborted);
    check('toggleFullscreen enter инвалидирует токен похожих', a._relToken === 6, '_relToken=' + a._relToken);
    check('fullscreen класс установлен', a.els.viewerContent.classList.contains('fullscreen'));
  }

  // 6. toggleFullscreen exit: re-renders tags and schedules related if not yet loaded
  {
    const a = makeApp();
    a.toggleFullscreen(); // enter
    a._relLoadedFor = null;
    let relScheduled = 0;
    a.scheduleRelated = function () { relScheduled++; };
    API_CALLS.length = 0;
    a.toggleFullscreen(); // exit
    await flush();
    check('toggleFullscreen exit догружает счётчики тегов',
      API_CALLS.some(u => u.includes('/tag-counts')), JSON.stringify(API_CALLS));
    check('toggleFullscreen exit планирует похожие для текущего поста',
      relScheduled === 1, 'relScheduled=' + relScheduled);
  }

  // 7. toggleFullscreen exit: does NOT re-request related if already loaded for the post
  {
    const a = makeApp();
    a.toggleFullscreen(); // enter
    a._relLoadedFor = 111;
    let relScheduled = 0;
    a.scheduleRelated = function () { relScheduled++; };
    API_CALLS.length = 0;
    a.toggleFullscreen(); // exit
    await flush();
    check('toggleFullscreen exit не перезапрашивает уже загруженные похожие',
      relScheduled === 0 && !API_CALLS.some(u => u.includes('/related')), 'relScheduled=' + relScheduled);
    check('fullscreen класс снят', !a.els.viewerContent.classList.contains('fullscreen'));
  }

  // 8. preloadVideo предзагружает видеопост в скрытый <video>
  {
    const a = makeApp();
    const fake = { src: '' };
    a._preloadVideoEl = fake;
    a.preloadVideo({ id: 222, file_type: 'video', downloaded: false, file_url: 'https://cdn/v.mp4' });
    check('preloadVideo ставит src скрытому видео (proxy url)',
      typeof fake.src === 'string' && fake.src.indexOf('/api/proxy') >= 0, String(fake.src));
    check('preloadVideo запоминает url, чтобы не перезагружать тот же пост',
      a._preloadVideoUrl.indexOf('v.mp4') >= 0, String(a._preloadVideoUrl));
    a.preloadVideo({ id: 222, file_type: 'video', downloaded: false, file_url: 'https://cdn/v.mp4' });
    check('preloadVideo повторно для того же url не сбрасывает src',
      fake.src.indexOf('/api/proxy') >= 0, String(fake.src));
  }

  // 9. preloadNeighbor диспатчит: видео → preloadVideo, картинка → preloadImage
  {
    const a = makeApp();
    let videoPrefetched = 0, photoPrefetched = 0;
    a.preloadVideo = function () { videoPrefetched++; };
    a.preloadImage = function () { photoPrefetched++; };
    a.preloadNeighbor({ id: 3, file_type: 'video' });
    a.preloadNeighbor({ id: 4, file_type: 'image' });
    check('preloadNeighbor: видео уходит в preloadVideo, картинка в preloadImage',
      videoPrefetched === 1 && photoPrefetched === 1, `video=${videoPrefetched} photo=${photoPrefetched}`);
    a.preloadNeighbor(null);
    check('preloadNeighbor(null) безопасен',
      videoPrefetched === 1 && photoPrefetched === 1, `video=${videoPrefetched} photo=${photoPrefetched}`);
  }

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  process.exit(failed ? 1 : 0);
})();