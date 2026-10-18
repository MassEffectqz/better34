// viewer_transitions.test.js — проверка жизненного цикла View Transitions:
//  * отсутствие одновременных дубликатов view-transition-name: hero-post;
//  * при openViewer hero-post переходит от карточки к вьюверу;
//  * при closeViewer hero-post переходит от вьювера к карточке;
//  * при отсутствии карточки в DOM (deeplink / unrendered) hero-post не назначается;
//  * отклонённые промисы (InvalidStateError / прерванная анимация) глушатся;
//  * активный переход сбрасывается через skipTransition() при новом вызове.
import { App } from '../state.js';
await import('../viewer.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

class FakeEl {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.style = {};
    const classes = new Set();
    this.classList = {
      add: (c) => classes.add(c),
      remove: (c) => classes.delete(c),
      contains: (c) => classes.has(c),
      toggle: (c, force) => {
        if (force !== undefined) { if (force) classes.add(c); else classes.delete(c); }
        else if (classes.has(c)) classes.delete(c); else classes.add(c);
      },
    };
    this.listeners = {};
    this.children = [];
    this._attrs = {};
  }
  addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); }
  removeEventListener(t, fn) {
    if (this.listeners[t]) this.listeners[t] = this.listeners[t].filter(f => f !== fn);
  }
  setAttribute(k, v) { this._attrs[k] = String(v); }
  removeAttribute(k) { delete this._attrs[k]; }
  getAttribute(k) { return this._attrs[k] || null; }
  appendChild(c) { this.children.push(c); return c; }
  querySelector(sel) {
    if (sel === 'img') return this.children.find(c => c.tagName === 'IMG') || null;
    return null;
  }
  querySelectorAll() { return []; }
  remove() {}
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { location: { href: 'http://localhost:3000' } });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
defG('document', {
  body: { style: {} },
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: (tag) => new FakeEl(tag),
});

console.log('Вьювер: View Transitions & hero-post\n');

(async () => {
  // 1. _clearHeroTransitions очищает style.viewTransitionName
  {
    const a = Object.create(App);
    const el1 = new FakeEl('img');
    const el2 = new FakeEl('img');
    a._setHeroTransition(el1);
    a._setHeroTransition(el2);
    check('el1 получил hero-post', el1.style.viewTransitionName === 'hero-post');
    check('el2 получил hero-post', el2.style.viewTransitionName === 'hero-post');
    a._clearHeroTransitions();
    check('el1 очищен после _clearHeroTransitions', el1.style.viewTransitionName === '');
    check('el2 очищен после _clearHeroTransitions', el2.style.viewTransitionName === '');
  }

  // 2. openViewer: старый снимок имеет ТОЛЬКО cardImg, новый — ТОЛЬКО vImg
  {
    const a = Object.create(App);
    const cardImg = new FakeEl('img');
    const vImg = new FakeEl('img');
    const viewerContent = new FakeEl('div');
    viewerContent.children.push(vImg);

    let oldSnapshotCardName = null;
    let oldSnapshotVImgName = null;
    let newSnapshotCardName = null;
    let newSnapshotVImgName = null;

    a.state = { posts: [{ id: 101, file_type: 'image' }], viewerIndex: 0, viewerOpen: false };
    a.els = {
      viewer: new FakeEl('div'),
      viewerContent,
      viewerInfo: new FakeEl('div'),
      viewerProgress: new FakeEl('div'),
      viewerLoader: new FakeEl('div'),
    };
    a.stopSlideshow = () => {};
    a._resetFullscreen = () => {};
    a._showViewerHud = () => {};
    a.scheduleRelated = () => {};
    a.preloadNeighbor = () => {};
    a.initViewerTouch = () => {};
    a.pushState = () => {};
    a._recMarkViewed = () => {};
    a._markViewed = () => {};
    a.renderViewer = () => {};

    // Подменяем document.querySelector для карточки
    const origQuerySelector = document.querySelector;
    document.querySelector = (sel) => {
      if (sel.includes('101')) return cardImg;
      return null;
    };

    // Симулируем браузерный document.startViewTransition
    document.startViewTransition = (cb) => {
      // Момент захвата старого снимка (Old state)
      oldSnapshotCardName = cardImg.style.viewTransitionName;
      oldSnapshotVImgName = vImg.style.viewTransitionName;
      // Применяем обновление DOM
      cb();
      // Момент захвата нового снимка (New state)
      newSnapshotCardName = cardImg.style.viewTransitionName;
      newSnapshotVImgName = vImg.style.viewTransitionName;
      return {
        ready: Promise.resolve(),
        updateCallbackDone: Promise.resolve(),
        finished: Promise.resolve(),
      };
    };

    a.openViewer(0);

    check('Old snapshot: cardImg имеет hero-post', oldSnapshotCardName === 'hero-post');
    check('Old snapshot: vImg НЕ имеет hero-post', !oldSnapshotVImgName);
    check('New snapshot: cardImg НЕ имеет hero-post (очищен)', !newSnapshotCardName);
    check('New snapshot: vImg имеет hero-post', newSnapshotVImgName === 'hero-post');

    await new Promise(r => setTimeout(r, 10));
    check('После завершения перехода vImg очищен', !vImg.style.viewTransitionName);
    check('После завершения перехода cardImg очищен', !cardImg.style.viewTransitionName);

    document.querySelector = origQuerySelector;
  }

  // 3. closeViewer: старый снимок имеет ТОЛЬКО vImg, новый — ТОЛЬКО cardImg
  {
    const a = Object.create(App);
    const cardImg = new FakeEl('img');
    const vImg = new FakeEl('img');
    const viewerContent = new FakeEl('div');
    viewerContent.children.push(vImg);

    let oldSnapshotCardName = null;
    let oldSnapshotVImgName = null;
    let newSnapshotCardName = null;
    let newSnapshotVImgName = null;

    a.state = { posts: [{ id: 202, file_type: 'image' }], viewerIndex: 0, viewerOpen: true };
    a.els = {
      viewer: new FakeEl('div'),
      viewerContent,
      viewerRelated: new FakeEl('div'),
    };
    a.stopSlideshow = () => {};
    a._resetFullscreen = () => {};
    a._cancelPreloads = () => {};
    a._stashCurrentVideoTime = () => {};
    a.currentVideo = () => null;
    a._stopVideoEl = () => {};
    a._closeModalHistory = () => {};
    a._cancelPan = () => {};
    a._cancelInertia = () => {};
    a.focusCard = () => {};
    a.scrollToFocused = () => {};

    const origQuerySelector = document.querySelector;
    document.querySelector = (sel) => {
      if (sel.includes('202')) return cardImg;
      return null;
    };

    document.startViewTransition = (cb) => {
      oldSnapshotCardName = cardImg.style.viewTransitionName;
      oldSnapshotVImgName = vImg.style.viewTransitionName;
      cb();
      newSnapshotCardName = cardImg.style.viewTransitionName;
      newSnapshotVImgName = vImg.style.viewTransitionName;
      return {
        ready: Promise.resolve(),
        updateCallbackDone: Promise.resolve(),
        finished: Promise.resolve(),
      };
    };

    a.closeViewer();

    check('Close Old snapshot: vImg имеет hero-post', oldSnapshotVImgName === 'hero-post');
    check('Close Old snapshot: cardImg НЕ имеет hero-post', !oldSnapshotCardName);
    check('Close New snapshot: vImg НЕ имеет hero-post (очищен)', !newSnapshotVImgName);
    check('Close New snapshot: cardImg имеет hero-post', newSnapshotCardName === 'hero-post');

    await new Promise(r => setTimeout(r, 10));
    check('После закрытия hero-post полностью очищен с cardImg', !cardImg.style.viewTransitionName);
    check('После закрытия hero-post полностью очищен с vImg', !vImg.style.viewTransitionName);

    document.querySelector = origQuerySelector;
  }

  // 4. Deeplink без карточки в DOM: hero-post не назначается ни старому, ни новому
  {
    const a = Object.create(App);
    const vImg = new FakeEl('img');
    const viewerContent = new FakeEl('div');
    viewerContent.children.push(vImg);

    let oldSnapshotVImgName = null;
    let newSnapshotVImgName = null;

    a.state = { posts: [{ id: 303, file_type: 'image' }], viewerIndex: 0, viewerOpen: false };
    a.els = {
      viewer: new FakeEl('div'),
      viewerContent,
      viewerInfo: new FakeEl('div'),
      viewerProgress: new FakeEl('div'),
      viewerLoader: new FakeEl('div'),
    };
    a.stopSlideshow = () => {};
    a._resetFullscreen = () => {};
    a._showViewerHud = () => {};
    a.scheduleRelated = () => {};
    a.preloadNeighbor = () => {};
    a.initViewerTouch = () => {};
    a.pushState = () => {};
    a._recMarkViewed = () => {};
    a._markViewed = () => {};
    a.renderViewer = () => {};

    // document.querySelector возвращает null (поста нет в ленте)
    const origQuerySelector = document.querySelector;
    document.querySelector = () => null;

    document.startViewTransition = (cb) => {
      oldSnapshotVImgName = vImg.style.viewTransitionName;
      cb();
      newSnapshotVImgName = vImg.style.viewTransitionName;
      return {
        ready: Promise.resolve(),
        updateCallbackDone: Promise.resolve(),
        finished: Promise.resolve(),
      };
    };

    a.openViewer(0);

    check('Deeplink без карточки: Old snapshot не содержит hero-post', !oldSnapshotVImgName);
    check('Deeplink без карточки: New snapshot не содержит hero-post', !newSnapshotVImgName);

    document.querySelector = origQuerySelector;
  }

  // 5. withViewTransition ловит InvalidStateError без Unhandled Rejection
  {
    const a = Object.create(App);
    let rejectedHandled = true;

    const unhandledListener = (_err) => {
      rejectedHandled = false;
    };
    process.on('unhandledRejection', unhandledListener);

    document.startViewTransition = (cb) => {
      cb();
      const abortError = new Error('Transition was aborted because of invalid state');
      abortError.name = 'InvalidStateError';
      return {
        ready: Promise.reject(abortError),
        updateCallbackDone: Promise.resolve(),
        finished: Promise.reject(abortError),
        skipTransition: () => {},
      };
    };

    let executed = false;
    const vt = a.withViewTransition(() => {
      executed = true;
    });

    check('Коллбэк transition выполнился', executed);
    check('Возвращён объект transition', !!vt);

    await new Promise(r => setTimeout(r, 50));
    process.off('unhandledRejection', unhandledListener);

    check('InvalidStateError не приводит к unhandledRejection', rejectedHandled);
  }

  // 6. Активный переход прерывается через skipTransition() при запуске следующего
  {
    const a = Object.create(App);
    let skipped = false;

    document.startViewTransition = (cb) => {
      cb();
      return {
        ready: Promise.resolve(),
        updateCallbackDone: Promise.resolve(),
        finished: new Promise(() => {}), // долгоиграющий промис
        skipTransition: () => { skipped = true; },
      };
    };

    a.withViewTransition(() => {});
    check('Первый переход активен, ещё не skipped', !skipped);

    a.withViewTransition(() => {});
    check('Второй переход вызвал skipTransition() у первого', skipped);
  }

  console.log(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
})();
