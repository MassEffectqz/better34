// friends_ui.test.js — вкладка «Друзья»: разметка, иконки, стили,
// безопасность рендера и РЕАЛЬНАЯ привязка кнопок.
//
// Что здесь ловится:
//  * нерабочие кнопки (уже было: bindFriendsUI звался до App.init(), когда
//    this.els ещё пуст, — обработчики не навешивались);
//  * пустой <i> вместо иконки (так было с share2 и qrCode — icon() молча
//    возвращает '' на неизвестном имени);
//  * неэкранированный адрес/имя в innerHTML.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { App } from '../state.js';
import '../friends.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
const html = readFileSync(join(root, 'index.html'), 'utf8');
const utils = readFileSync(join(root, 'js', 'utils.js'), 'utf8');

console.log('Вкладка «Друзья»\n');

// ── 1. Разметка: вкладка, панель и кнопки на месте ────────────────────────────
{
  check('вкладка «Друзья» в панели профиля', /data-panel-tab="friends"/.test(html));
  check('панель tab-friends существует', /id="tab-friends"/.test(html));
  check('aria-controls вкладки указывает на панель',
    /data-panel-tab="friends" aria-controls="tab-friends"/.test(html));
  for (const id of ['friends-list', 'friends-code-box', 'friends-code',
    'friends-add-form', 'friends-add-input', 'btn-friends-add-confirm',
    'btn-friends-add', 'btn-friends-code', 'btn-friends-sync', 'btn-friends-code-copy']) {
    check(`элемент #${id} есть в разметке`, new RegExp(`id="${id}"`).test(html));
  }
  // Счётчик нужен отдельным элементом: без него вкладка показывала бы 0.
  check('счётчик друзей есть', /id="st-friends"/.test(html));
  // QR сознательно нет: друг вставляет текстовый код, энкодер не нужен.
  check('QR-кнопки нет (код текстовый)', !/btn-friends-code-qr/.test(html));
}

// ── 2. Иконки: каждая из data-lucide в разметке есть в ICONS ───────────────
{
  const block = utils.slice(utils.indexOf('const ICONS = {'), utils.indexOf('};', utils.indexOf('const ICONS = {')));
  const have = new Set([...block.matchAll(/^\s{2}([A-Za-z0-9_]+)\s*:/gm)].map(m => m[1]));
  const used = [...html.matchAll(/data-lucide="([A-Za-z0-9_]+)"/g)].map(m => m[1]);
  const missing = [...new Set(used)].filter(n => !have.has(n));
  check('все data-lucide есть в ICONS', missing.length === 0, missing.join(','));
  // Отдельно те иконки, что добавила вкладка друзей.
  for (const n of ['users', 'userPlus', 'refresh', 'user', 'trash', 'qrCode']) {
    check(`иконка ${n} объявлена`, have.has(n));
  }
}

// ── 3. Стили вкладки ─────────────────────────────────────────────────────────
{
  const css = readFileSync(join(root, 'css', '04-panels.css'), 'utf8');
  check('.friend-item описан', /\.friend-item\{/.test(css));
  check('.friends-code не ломает вёрстку длинной строкой', /\.friends-code\{[^}]*word-break:break-all/.test(css));
  check('flex-столбец друзей не даёт ряду распираться', /\.friend-body\{[^}]*min-width:0/.test(css));
  // Без этого на тач-устройстве кнопка удаления была бы недостижима.
  check('на тач-устройствах кнопка удаления всегда видна',
    /@media\(hover:none\)\{\.friend-del\{opacity:1\}\}/.test(css));
}

// ── 4. Рендер: экранирование и безопасность аватара ──────────────────────────
// Данные приходят с чужого сервера, поэтому innerHTML здесь опасен. Проверяем
// по коду friends.js, что каждое поле проходит через esc(), и что аватар
// фильтруется по схеме (javascript: в src — вектор XSS).
{
  const friends = readFileSync(join(root, 'js', 'friends.js'), 'utf8');
  const renderBody = friends.slice(friends.indexOf('App.renderFriends'), friends.indexOf('App.showFriendCode'));
  for (const field of ['f.url', 'name', 'f.id']) {
    check(`${field} экранируется`, renderBody.includes(`esc(${field})`));
  }
  // Аватар: разрешены только http(s), / и data:image — как в social.js.
  check('аватар фильтруется по схеме', /avatarOK\s*=\s*f\.avatar\s*&&\s*\/\^\(https\?:\\\/\\\/\|\\\/\|data:image\\\/\)\//.test(renderBody));
  // Ссылок на ключ в разметке быть не должно: friendView его не отдаёт.
  check('в UI нет обращения к ключу друга', !/f\.key|f\.my_key/i.test(renderBody));
}

// ── 5. Кнопки действительно привязаны ────────────────────────────────────────
// Регрессия: bindFriendsUI звался из app.js ДО App.init(), когда this.els ещё
// пуст, — все поля были undefined, обработчики не навешивались, и вкладка
// выглядела живой, но не работала. Проверяем на DOM-проте: каждый id должен
// получить слушатель клика.
{
  /** @type {Map<string, {listeners: Map<string, Array<() => void>>}>} */
  const nodes = new Map();
  const mk = (id) => {
    const rec = {
      listeners: /** @type {Map<string, Array<() => void>>} */ (new Map()),
      text: '',
      classList: { add() {}, remove() {} },
      addEventListener(type, fn) {
        if (!rec.listeners.has(type)) rec.listeners.set(type, []);
        /** @type {any} */ (rec.listeners.get(type)).push(fn);
      },
    };
    nodes.set(id, rec);
    return rec;
  };
  ['friends-list', 'friends-code-box', 'friends-code', 'st-friends',
    'friends-add-form', 'friends-add-input',
    'btn-friends-add', 'btn-friends-code', 'btn-friends-sync', 'btn-friends-code-copy',
    'btn-friends-add-confirm',
  ].forEach(mk);

  const prevDoc = globalThis.document;
  globalThis.document = { getElementById: (id) => nodes.get(id) || null };
  try {
    const a = Object.create(App);
    // els пуст — ровно как в бою на момент вызова bindFriendsUI.
    a.els = {};
    a.bindFriendsUI();
    for (const id of ['btn-friends-add', 'btn-friends-code', 'btn-friends-sync',
      'btn-friends-code-copy', 'btn-friends-add-confirm']) {
      const rec = /** @type {any} */ (nodes.get(id));
      check(`кнопка #${id} имеет обработчик клика`, (rec.listeners.get('click') || []).length > 0);
    }
    // Повторный вызов не должен вешать обработчики дважды.
    a.bindFriendsUI();
    const rec = /** @type {any} */ (nodes.get('btn-friends-sync'));
    check('повторная привязка не дублирует обработчики',
      (rec.listeners.get('click') || []).length === 1,
      `обработчиков: ${(rec.listeners.get('click') || []).length}`);
  } finally {
    globalThis.document = prevDoc;
  }
}

// ── Итог ─────────────────────────────────────────────────────────────────────
console.log(`\n${failed ? 'FAILED' : 'ok'}: ${passed} passed, ${failed} failed`);
if (failed) process.exit(1);

