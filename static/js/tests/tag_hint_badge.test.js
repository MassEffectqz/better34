// tag_hint_badge.test.js — значок «?» в кружочке у тегов с объяснением:
//  * значок ставится только у тегов, у которых есть описание;
//  * ставится для всех трёх уровней, включая note (без цвета);
//  * у обычного тега значка нет;
//  * в значке и на самом теге — один и тот же текст описания.
import { App } from '../state.js';
import { API } from '../api.js';
import { OddTags } from '../odd_tags.js';
await import('../viewer.js');

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeClassList(initial) {
  const s = new Set(initial || []);
  return {
    contains(c) { return s.has(c); }, add(c) { s.add(c); }, remove(c) { s.delete(c); },
    toggle(c, f) { const on = f != null ? !!f : !s.has(c); if (on) s.add(c); else s.delete(c); return on; },
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
      style: {}, classList: makeClassList(), children: [], attrs: {},
      innerHTML: '', textContent: '', title: '',
      appendChild(c) { this.children.push(c); },
      setAttribute(k, v) { this.attrs[k] = v; },
      addEventListener() {}, removeEventListener() {},
      querySelector() { return null; }, remove() {},
    };
  },
  body: { style: {} },
};
globalThis.requestAnimationFrame = () => 1;
globalThis.cancelAnimationFrame = () => {};
API.get = async () => ({});
API.post = async () => ({});
API.invalidate = () => {};

const savedFetch = globalThis.fetch;
globalThis.fetch = async () => ({
  ok: true,
  json: async () => ({
    yellow: { tomboy: 'Девушка с мальчишеской внешностью' },
    red: { furry: 'Человекоподобное существо с чертами животного' },
    note: { solo: 'В кадре один персонаж' },
  }),
});
await OddTags.load();
globalThis.fetch = savedFetch;

function render(tags) {
  const a = Object.create(App);
  a.state = {
    posts: [{ id: 1, tags, file_type: 'image' }],
    viewerIndex: 0, viewerOpen: true,
    query: '',
    profile: { fav_tags: [], hidden_tags: [] },
  };
  const host = {
    children: [], innerHTML: '',
    appendChild(c) { this.children.push(c); },
    addEventListener() {}, removeEventListener() {},
  };
  a.els = { viewerTags: host, viewerHead: { style: {} }, viewerFoot: { style: {} } };
  a.OddTags = OddTags;
  a._tagCounts = {};
  a._viewerIsFullscreen = () => true; // не дёргаем /tag-counts
  a._saveTagCounts = () => {};
  a._renderViewerTags();
  return host.children;
}

const badge = span => (span.children || []).find(c => c.className === 'tag-hint');
const byTag = (spans, name) => spans.find(s => s._brieflyTag === name);

console.log('tag hint badge tests\n');

{
  const spans = render('solo furry tomboy 1girl highres');
  check('значок есть у note-тега (solo)', !!badge(byTag(spans, 'solo')));
  check('значок есть у red-тега (furry)', !!badge(byTag(spans, 'furry')));
  check('значок есть у yellow-тега (tomboy)', !!badge(byTag(spans, 'tomboy')));
  check('у обычного тега (1girl) значка нет', !badge(byTag(spans, '1girl')));
  check('у тега без описания (highres) значка нет', !badge(byTag(spans, 'highres')));
  check('в значке текст «?»', badge(byTag(spans, 'solo')).textContent === '?', badge(byTag(spans, 'solo')).textContent);
  check('в значке тот же текст, что и на теге',
    badge(byTag(spans, 'furry')).title === byTag(spans, 'furry').title,
    `${badge(byTag(spans, 'furry')).title} != ${byTag(spans, 'furry').title}`);
  check('на теге подсказка без префикса «Странный —»',
    !String(byTag(spans, 'furry').title).includes('Странный'), byTag(spans, 'furry').title);
  check('значок скрыт от скринридера',
    badge(byTag(spans, 'solo')).attrs['aria-hidden'] === 'true', JSON.stringify(badge(byTag(spans, 'solo')).attrs));
  // note — без цвета, но значок у него всё равно есть.
  check('note-тег не красится', !String(byTag(spans, 'solo').className).includes('tag-odd-'), byTag(spans, 'solo').className);
  check('red-тег красится', String(byTag(spans, 'furry').className).includes('tag-odd-red'), byTag(spans, 'furry').className);
}

console.log(`tag_hint_badge: ${passed} passed, ${failed} failed`);
if (failed) process.exit(1);