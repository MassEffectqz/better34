// viewer_share.test.js — кнопка «Поделиться» во вьювере:
//  * делится АБСОЛЮТНЫМ URL текущего поста (относительный в мессенджере не откроется);
//  * сперва нативный Web Share API, при его отсутствии/отказе — копирование;
//  * закрытый пользователем системный лист (AbortError) не считается ошибкой;
//  * текст названия: запрос, иначе первые теги, иначе #id.
import { App } from '../state.js';
import '../viewer.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', { location: { origin: 'https://briefly.example', href: 'https://briefly.example/post/7' }, addEventListener() {} });
defG('localStorage', { getItem: () => null, setItem() {}, removeItem() {} });
defG('navigator', {});
defG('document', {
  createElement: () => ({ style: {}, setAttribute() {}, select() {}, remove() {} }),
  body: { appendChild() {} },
  execCommand: () => true,
  querySelector: () => null,
  querySelectorAll: () => [],
  addEventListener() {},
});
defG('AbortController', class { constructor() { this.signal = {}; } abort() {} });

function makeApp(post, query) {
  const a = Object.create(App);
  a.state = { posts: post ? [post] : [], viewerIndex: 0, query: query || '' };
  a.toasts = [];
  a.showToast = (m, k) => a.toasts.push([m, k]);
  return a;
}

console.log('Вьювер: кнопка «Поделиться»\n');

(async () => {
  // Абсолютный URL: обсуждать пост имеет смысл с другого устройства, где
  // относительный /post/7 просто не откроется.
  {
    const a = makeApp({ id: 7 });
    const u = a.shareUrl();
    check('URL абсолютный, с текущим постом', u === 'https://briefly.example/post/7', u);
  }
  {
    const a = makeApp({ id: 9 }, 'blue_hair');
    const u = a.shareUrl();
    check('URL учитывает поиск', u === 'https://briefly.example/search/blue_hair/post/9', u);
  }

  // Текст: сначала запрос, потом теги, потом #id.
  {
    const a = makeApp({ id: 5 }, 'blue_hair');
    check('текст — запрос, подчёркивания заменены на пробелы',
      a.shareText() === 'blue hair', a.shareText());
    const b = makeApp({ id: 5, tags: 'solo smile 1girl long_hair' });
    check('без запроса — первые теги поста',
      b.shareText() === 'solo, smile, 1girl', b.shareText());
    const c = makeApp({ id: 5 });
    check('без запроса и тегов — #id', c.shareText() === '#5', c.shareText());
  }

  // Нативный share: вызывается и НЕ сопровождается копированием.
  {
    const a = makeApp({ id: 7 });
    let got = null;
    globalThis.navigator = { share: async (d) => { got = d; } };
    await a.sharePost();
    check('Web Share API вызван с названием и ссылкой',
      got && got.url === 'https://briefly.example/post/7' && got.title === '#7', JSON.stringify(got));
    check('при успешном share тоста нет — нечего подтверждать',
      a.toasts.length === 0, JSON.stringify(a.toasts));
  }

  // Пользователь закрыл системный лист крестиком: это не ошибка.
  {
    const a = makeApp({ id: 7 });
    globalThis.navigator = { share: async () => { const e = new Error('x'); e.name = 'AbortError'; throw e; } };
    let copied = 0;
    a._copyToClipboard = async () => { copied++; return true; };
    await a.sharePost();
    check('закрытый пользователем лист — молчание, а не «ошибка»',
      copied === 0 && a.toasts.length === 0, 'copied=' + copied + ' ' + JSON.stringify(a.toasts));
  }

  // Нет Web Share API (десктоп) — копируем и говорим об этом.
  {
    const a = makeApp({ id: 7 });
    globalThis.navigator = {};
    let copied = '';
    a._copyToClipboard = async (txt) => { copied = txt; return true; };
    await a.sharePost();
    check('без Web Share API ссылка копируется', copied === 'https://briefly.example/post/7', copied);
    check('показан тост «ссылка скопирована»',
      a.toasts.length === 1 && /скопирована/i.test(a.toasts[0][0]), JSON.stringify(a.toasts));
  }

  // Share есть, но упал с другой ошибкой — тоже откатываемся на копирование.
  {
    const a = makeApp({ id: 7 });
    globalThis.navigator = { share: async () => { throw new Error('boom'); } };
    let copied = '';
    a._copyToClipboard = async (txt) => { copied = txt; return true; };
    await a.sharePost();
    check('падение share (не AbortError) откатывается на копирование',
      copied === 'https://briefly.example/post/7', copied);
  }

  // Буфер запрещён — честная ошибка, а не тишина.
  {
    const a = makeApp({ id: 7 });
    globalThis.navigator = {};
    a._copyToClipboard = async () => false;
    await a.sharePost();
    check('если буфер недоступен — сообщение об ошибке',
      a.toasts.length === 1 && a.toasts[0][1] === 'error', JSON.stringify(a.toasts));
  }

  // Встроенный копировальщик: нет clipboard API → execCommand.
  {
    const a = makeApp({ id: 7 });
    globalThis.navigator = {};
    let used = false;
    globalThis.document.execCommand = (cmd) => { used = cmd === 'copy'; return used; };
    const ok = await a._copyToClipboard('https://x/1');
    check('fallback на execCommand, когда нет clipboard API',
      ok === true && used === true, 'ok=' + ok + ' execCommand=' + used);
  }

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();
