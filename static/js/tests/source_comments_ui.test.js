// source_comments_ui.test.js — комментарии источника (dapi s=comment):
//  * определение сайта по хосту медиа (у постов из ленты post.source пуст);
//  * условие показа бейджа (has_comments из выдачи / счётчик из кэша);
//  * состояния вьюера: неизвестный источник, unsupported, пусто, список;
//  * текст с чужого сайта экранируется (XSS) и обрезается.
import { App } from '../state.js';
import '../source_comments.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { addEventListener() {} });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
// esc() в utils.js: создаёт элемент, кладёт текст в textContent и читает
// innerHTML — браузер возвращает экранированную строку. Заглушка повторяет
// именно это поведение, иначе проверки XSS проходят бы вхолостую.
defG('document', {
  querySelector: () => null,
  querySelectorAll: () => [],
  createElement: () => ({
    set textContent(v) { this._t = String(v); },
    get textContent() { return this._t || ''; },
    get innerHTML() {
      return String(this._t || '')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    },
    set innerHTML(v) { this._t = String(v); },
    getAttribute: () => null,
  }),
});

// Узел для разметки вьюера: innerHTML хранится буквально (как в браузере),
// querySelector отдаёт дочерние узлы по id, чтобы работал рендер секции.
function node() {
  const kids = {};
  const self = {
    _html: '', textContent: '', className: '', hidden: false,
    disabled: false, title: '', value: '', parentElement: null,
    attributes: {},
    cls: new Set(),
    classList: {
      add(...c) { c.forEach((x) => self.cls.add(x)); },
      remove(...c) { c.forEach((x) => self.cls.delete(x)); },
      contains(c) { return self.cls.has(c); },
      toggle() {},
    },
    appendChild(c) { if (c) kids[c.id || Object.keys(kids).length] = c; },
    append(...c) { c.forEach((x) => { if (x) kids[Object.keys(kids).length] = x; }); },
    setAttribute(k, v) { this.attributes[k] = v; },
    removeAttribute(k) { delete this.attributes[k]; },
    addEventListener() {},
    querySelector(sel) {
      const id = String(sel).replace('#', '');
      if (id) {
        if (!kids[id]) { kids[id] = node(); kids[id].parentElement = this; }
        return kids[id];
      }
      return null;
    },
    querySelectorAll: () => [],
    children: kids,
  };
  Object.defineProperty(self, 'innerHTML', {
    get() { return self._html; },
    set(v) { self._html = String(v); },
  });
  return self;
}

console.log('Комментарии источника: UI\n');

// ── 1. Определение сайта ───────────────────────────────────────────────────
const a = Object.create(App);
check('сайт из post.source', a.sourceSiteOf({ source: 'Gelbooru' }) === 'gelbooru',
  a.sourceSiteOf({ source: 'Gelbooru' }));
check('сайт из хоста gelbooru', a.sourceSiteOf({ file_url: 'https://gelbooru.com/img/x/1.jpg' }) === 'gelbooru');
check('сайт из хоста hypnohub', a.sourceSiteOf({ file_url: 'https://hypnohub.net/img/x/1.jpg' }) === 'hypnohub');
check('сайт из preview_url', a.sourceSiteOf({ preview_url: 'https://i.rule34.xxx/1.jpg' }) === 'rule34',
  a.sourceSiteOf({ preview_url: 'https://i.rule34.xxx/1.jpg' }));
check('неизвестный хост → пусто', a.sourceSiteOf({ file_url: 'https://example.org/1.jpg' }) === '');
check('мусорный URL не роняет', a.sourceSiteOf({ file_url: 'не-урл' }) === '');
check('null-пост', a.sourceSiteOf(null) === '');

// ── 2. Условие бейджа ──────────────────────────────────────────────────────
check('бейдж: has_comments из выдачи', a.hasSourceComments({ has_comments: true }) === true);
check('бейдж: счётчик из кэша', a.hasSourceComments({ comment_count: 3 }) === true);
check('без бейджа: ни флага, ни счётчика', a.hasSourceComments({ has_comments: false, comment_count: 0 }) === false);
check('без бейджа: пост пустой', a.hasSourceComments({}) === false);
check('без бейджа: null', a.hasSourceComments(null) === false);

// ── 3. Неизвестный источник ────────────────────────────────────────────────
const host = node();
a.els = { viewerSourceComments: host };
a.state = { viewerOpen: true };
a.renderSourceComments({ id: 5, file_url: 'https://example.org/5.jpg' });
// Сообщение «источник неизвестен» живёт в списке секции, а не в её шапке.
const noSrcList = host.querySelector('#vc-src-list');
check('неизвестный источник: понятный текст',
  /неизвестен источник|Unknown post source/i.test(noSrcList.innerHTML), noSrcList.innerHTML.slice(0, 140));
check('неизвестный источник: кнопка загрузки скрыта',
  host.querySelector('#vc-src-load').classList.contains('hidden'),
  [...host.querySelector('#vc-src-load').cls].join(','));

// ── 4. Загрузка: URL, unsupported, пусто, список, XSS, обрезка ────────────
function withList(response) {
  const box = node();
  const l = node();
  l.parentElement = box;
  const calls = [];
  a.state = { viewerOpen: true };
  a._srcCommentsPostId = 5;
  a.API = { get: (u) => { calls.push(u); return Promise.resolve(response); } };
  return { box, l, calls, done: a.loadSourceComments.call(a, 5, 'hypnohub', { list: l }) };
}

(async () => {
  const r1 = withList({ unsupported: true, site: 'gelbooru', comments: [] });
  await r1.done;
  check('unsupported: понятный текст, без списка',
    /не отдаёт комментарии|does not expose comments/i.test(r1.l.innerHTML), r1.l.innerHTML.slice(0, 140));
  check('URL содержит site и source_id',
    /site=hypnohub/.test(r1.calls[0]) && /source_id=5/.test(r1.calls[0]), r1.calls[0]);
  check('без refresh по умолчанию', !/refresh=1/.test(r1.calls[0]), r1.calls[0]);

  const r2 = withList({ site: 'hypnohub', count: 0, comments: [] });
  await r2.done;
  check('пусто: сообщение «нет комментариев»',
    /комментариев нет|No comments/i.test(r2.l.innerHTML), r2.l.innerHTML.slice(0, 140));

  const r3 = withList({
    site: 'hypnohub',
    count: 2,
    comments: [
      { id: 1, author: '<img src=x onerror=alert(1)>', body: 'ok', created_at: '2026-09-27 00:08' },
      { id: 2, author: 'bob', body: 'y'.repeat(5000), created_at: '2026-09-27 00:09' },
    ],
  });
  await r3.done;
  check('список отрисован', (r3.l.innerHTML.match(/vc-item/g) || []).length === 2, r3.l.innerHTML.slice(0, 160));
  check('XSS: тег не попал в DOM', !r3.l.innerHTML.includes('<img'), r3.l.innerHTML.slice(0, 200));
  check('XSS: автор экранирован', r3.l.innerHTML.includes('&lt;img'), r3.l.innerHTML.slice(0, 200));
  check('длинный текст обрезан', r3.l.innerHTML.includes('…') && !r3.l.innerHTML.includes('y'.repeat(2001)));
  check('дата показана', r3.l.innerHTML.includes('2026-09-27 00:08'));

  // refresh=1 уходит только по явной кнопке.
  const r4 = withList({ site: 'hypnohub', count: 0, comments: [] });
  const l4 = r4.l;
  a.state = { viewerOpen: true };
  a._srcCommentsPostId = 5;
  a.API = { get: (u) => { r4.calls.push(u); return Promise.resolve({ site: 'hypnohub', comments: [] }); } };
  await a.loadSourceComments.call(a, 5, 'hypnohub', { list: l4, refresh: true });
  const last = r4.calls[r4.calls.length - 1];
  check('refresh=1 по кнопке «обновить»', /refresh=1/.test(last), last);

  // Переключили пост — поздний ответ не должен перерисовывать панель.
  const r5 = withList({ site: 'hypnohub', count: 1, comments: [{ id: 1, author: 'a', body: 'b' }] });
  a._srcCommentsPostId = 6; // юзер ушёл на другой пост
  await r5.done;
  check('ответ для устаревшего поста игнорируется', !/vc-item/.test(r5.l.innerHTML), r5.l.innerHTML.slice(0, 120));

  console.log(`\nИтог: ${passed} ok, ${failed} fail`);
  if (failed) process.exit(1);
})();