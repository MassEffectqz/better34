// viewer_related_modes.test.js — проверка вкладок похожих постов (все, персонаж, визуально) и фильтра рейтинга (все, sfw, nsfw)
import { App } from '../state.js';
import { API } from '../api.js';
await import('../viewer.js');

let failed = 0;
function check(name, cond, detail) {
  if (cond) { console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

class FakeEl {
  constructor(tag) {
    this.tagName = String(tag || 'div').toUpperCase();
    this.listeners = {};
    this.children = [];
    this.dataset = {};
    this.style = {};
    this.className = '';
    this.textContent = '';
    this.innerHTML = '';
    const s = new Set();
    this.classList = {
      add(c) { s.add(c); },
      remove(c) { s.delete(c); },
      contains(c) { return s.has(c); },
      toggle(c) { if (s.has(c)) s.delete(c); else s.add(c); },
    };
  }
  addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); }
  removeEventListener() {}
  appendChild(c) { this.children.push(c); return c; }
}

globalThis.window = {
  innerWidth: 1024,
  innerHeight: 768,
  addEventListener() {},
  removeEventListener() {},
};
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};
globalThis.document = {
  createElement: (tag) => new FakeEl(tag),
  addEventListener() {},
  removeEventListener() {},
};

const lastUrls = [];
API.get = (url) => {
  lastUrls.push(url);
  return Promise.resolve({
    posts: [{ id: 101, preview_url: 'http://img/101.jpg' }],
    mode: url.includes('mode=char') ? 'char' : (url.includes('mode=visual') ? 'visual' : 'all'),
    rating: url.includes('rating=sfw') ? 'sfw' : (url.includes('rating=nsfw') ? 'nsfw' : ''),
    has_artist: true,
    has_character: true,
    tags: ['rem_(re:zero)', 'maid'],
  });
};

const relContainer = new FakeEl('div');
App.els = App.els || {};
App.els.viewerRelated = relContainer;
App.state = App.state || {};
App.state.viewerOpen = true;
App.state.viewerIndex = 0;
App.state.posts = [{ id: 100, tags: 'rem_(re:zero) maid' }];

console.log('Viewer related modes and ratings tests');

// 1. По умолчанию открывает 'all' и rating=''
localStorage.removeItem('briefly_rel_mode');
localStorage.removeItem('briefly_rel_rating');
lastUrls.length = 0;
await App.loadRelated(App.state.posts[0]);
check('URL содержит mode=all', lastUrls[0].includes('mode=all'), lastUrls[0]);
check('HTML содержит rel-mode-tabs и rel-rating-tabs', relContainer.innerHTML.includes('rel-mode-tabs') && relContainer.innerHTML.includes('rel-rating-tabs'));
check('HTML содержит вкладки Персонаж и Визуально', relContainer.innerHTML.includes('data-mode="char"') && relContainer.innerHTML.includes('data-mode="visual"'));
check('HTML содержит фильтры рейтинга SFW и 18+', relContainer.innerHTML.includes('data-rating="sfw"') && relContainer.innerHTML.includes('data-rating="nsfw"'));

// 2. Клик по вкладке Персонаж сохраняет настройки в localStorage
App.onRelatedClick({
  preventDefault() {},
  target: {
    closest: (sel) => sel === '.rel-tab' ? { dataset: { mode: 'char' } } : null,
  },
});
check('localStorage сохранил mode=char', localStorage.getItem('briefly_rel_mode') === 'char');
check('Запрос ушёл с mode=char', lastUrls[lastUrls.length - 1].includes('mode=char'), lastUrls[lastUrls.length - 1]);

// 3. Клик по фильтру рейтинга SFW сохраняет в localStorage и отправляет rating=sfw
App.onRelatedClick({
  preventDefault() {},
  target: {
    closest: (sel) => sel === '.rel-tab' ? { dataset: { rating: 'sfw' } } : null,
  },
});
check('localStorage сохранил rating=sfw', localStorage.getItem('briefly_rel_rating') === 'sfw');
check('Запрос ушёл с rating=sfw', lastUrls[lastUrls.length - 1].includes('rating=sfw'), lastUrls[lastUrls.length - 1]);
check('Запрос сохранил текущий mode=char', lastUrls[lastUrls.length - 1].includes('mode=char'), lastUrls[lastUrls.length - 1]);

// 4. Клик по фильтру рейтинга 18+ переключает на nsfw
App.onRelatedClick({
  preventDefault() {},
  target: {
    closest: (sel) => sel === '.rel-tab' ? { dataset: { rating: 'nsfw' } } : null,
  },
});
check('localStorage сохранил rating=nsfw', localStorage.getItem('briefly_rel_rating') === 'nsfw');
check('Запрос ушёл с rating=nsfw', lastUrls[lastUrls.length - 1].includes('rating=nsfw'), lastUrls[lastUrls.length - 1]);

// 5. scheduleRelated берет сохраненные значения
App.scheduleRelated(App.state.posts[0]);
check('scheduleRelated выставил сохраненный _relCurrentMode=char', App._relCurrentMode === 'char');
check('scheduleRelated выставил сохраненный _relCurrentRating=nsfw', App._relCurrentRating === 'nsfw');

if (failed > 0) {
  process.exit(1);
}
