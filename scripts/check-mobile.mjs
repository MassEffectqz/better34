// check-mobile.mjs — инварианты мобильного/тач-интерфейса и «честности» API
// на ненадёжной сети. Ловит ровно те регрессии, которые не видны на десктопе:
// hover-only контролы (на тач-устройстве их просто нет), мелкие тап-таргеты,
// авто-зум iOS на полях ввода, отсутствие safe-area при viewport-fit=cover,
// «зависшие» запросы без дедлайна и toggle-эндпоинты, которые оффлайн-очередь
// переигрывает дважды (лайк/скрытие молча откатываются).
//
// Запуск: npm run check:mobile
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const root = process.cwd();
const read = (p) => readFileSync(join(root, p), 'utf8');
const problems = [];
const note = (msg) => problems.push(msg);

// CSS разбит на части (static/css/*.css). Проверки ниже регекспят по всему
// набору сразу — @media-блоки, hover-only правила и т.п. иначе «терялись» бы
// в первой попавшейся части. Склеиваем в порядке имён: числовые префиксы
// задают порядок каскада, и именно он нужен для проверок вроде mediaBlock.
const cssDir = join(root, 'static/css');
const css = readdirSync(cssDir)
  .filter((f) => f.endsWith('.css'))
  .sort()
  .map((f) => readFileSync(join(cssDir, f), 'utf8'))
  .join('\n');
const html = read('static/index.html');
const apiJs = read('static/js/api.js');
const middleware = read('middleware.go');
const profileGo = read('internal/profile.go');
const handlersProfileGo = read('internal/handlers_profile.go');
const jsSource = readdirSync(join(root, 'static/js'))
  .filter((f) => f.endsWith('.js'))
  .map((f) => read(join('static/js', f))).join('\n');
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// Блоки @media(...) целиком: считаем скобки, вложенные правила не теряем.
// Условие может встречаться несколько раз (разные правки в разных местах
// файла) — склеиваем все блоки, иначе проверка смотрит только на первый.
function mediaBlock(cond) {
  const out = [];
  let from = 0;
  for (;;) {
    const at = css.indexOf(cond, from);
    if (at < 0) break;
    const open = css.indexOf('{', at);
    let depth = 0;
    let end = -1;
    for (let i = open; i < css.length; i++) {
      if (css[i] === '{') depth++;
      else if (css[i] === '}') { depth--; if (!depth) { end = i; break; } }
    }
    if (end < 0) break;
    out.push(css.slice(open + 1, end));
    from = end;
  }
  return out.join('\n');
}

// ── 1. viewport-fit=cover обязан быть вместе с safe-area ─────────────────
const viewport = (html.match(/<meta name="viewport" content="([^"]*)"/) || [])[1] || '';
if (!/width=device-width/.test(viewport)) {
  note('viewport: нет width=device-width — вёрстка уедет на телефоне');
}
if (/viewport-fit=cover/.test(viewport) && !/env\(safe-area-inset-/.test(css)) {
  note('viewport-fit=cover без env(safe-area-inset-*): шапка уйдёт под чёлку');
}
for (const sel of ['#header', '.viewer-head', '.panel-footer']) {
  if (!viewport.includes('viewport-fit=cover')) break;
  if (!new RegExp(esc(sel) + '\\{[^}]*env\\(safe-area-inset').test(css)) {
    note(`safe-area не задан для ${sel} (экран с вырезом / жестовой панелью)`);
  }
}

// ── 2. Контролы, видимые только по наведению ────────────────────────────
const hoverNoneBlocks = [...css.matchAll(/@media\s*\(hover:\s*none\)\s*\{/g)].map((m) => m.index);
const insideHoverNone = (needle) => hoverNoneBlocks.some((idx) => {
  const block = css.slice(idx, idx + 6000);
  return block.slice(0, block.indexOf('\n}')).includes(needle);
});
// Типичный паттерн: базовое правило прячет элемент (display:none / opacity:0),
// а правило с :hover его показывает. На тач-устройстве :hover не срабатывает —
// контрол просто недостижим (кнопка есть в DOM, но её не видно и не нажать).
const hidden = [];
for (const m of css.matchAll(/([^{}@]+)\{([^}]*)\}/g)) {
  if (!/display\s*:\s*none|opacity\s*:\s*(0|\.0)[;}]/.test(m[2])) continue;
  // Селектор может начаться с комментария и/или новой строки — отбрасываем.
  const sel = m[1].replace(/\/\*[\s\S]*?\*\//g, '').trim();
  if (!sel) continue;
  sel.split(',').map((s) => s.trim()).filter(Boolean).forEach((s) => hidden.push(s));
}
const hoverRevealed = [...css.matchAll(/:hover[^{]*?([^{}@,:]+(?:[ >][^{}@,]+)*)\s*\{/g)].map((m) => m[1].trim());
for (const sel of hidden) {
  const last = sel.split(/[ >]/).pop();
  const shownOnHover = hoverRevealed.some((h) => h === last || h.endsWith(' ' + last) || h.includes(last));
  if (!shownOnHover) continue;
  if (insideHoverNone(last)) continue;
  note(`hover-only контрол без @media(hover:none): ${sel} — на телефоне недоступен`);
}
// ── 3. Тап-таргеты: оверлеи плитки обязаны сбрасывать общее правило 44px ──
// .panel-content button{min-height:44px} ломает три кнопки в плитке ~105px шириной.
if (!/\.pf-thumb \.pf-act\{[^}]*min-height:0/.test(css) || !/\.pf-thumb \.pf-sel\{[^}]*min-height:0/.test(css)) {
  note('оверлеи плитки (.pf-act/.pf-sel) не сбрасывают min-height — три кнопки не влезут в плитку');
}
if (!/min-height:44px/.test(css)) {
  note('нет правила крупных тап-таргетов (min-height:44px) для мобильных');
}

// ── 4. Авто-зум iOS: поля ввода на мобильных должны быть >= 16px ────────
const mobile = mediaBlock('@media(max-width:600px)');
if (!/font-size:16px/.test(mobile)) {
  note('нет защиты от авто-зума iOS (font-size:16px для input в @media(max-width:600px))');
}
// В мобильном блоке каждое известное поле должно получить font-size >= 16px,
// иначе iOS Safari зумит страницу при каждом фокусе.
for (const inp of ['#search-input', '.collection-form input', '#tag-filter', '.preset-form input', '.tag-input-row input']) {
  const rules = [...mobile.matchAll(new RegExp('([^{}]*' + esc(inp) + '[^{}]*)\\{([^}]*)\\}', 'g'))];
  if (!rules.length) continue;
  const ok = rules.some(([, sel, body]) => {
    if (sel.includes(',')) return false; // перечисление разбирать не нужно: есть общий font-size
    const fs = (body.match(/font-size:([^;]+)/) || [])[1];
    return fs && parseFloat(fs) >= 16;
  }) || /input[^{}]*\{[^}]*font-size:16px/.test(mobile) || /,[^{}]*' + esc(inp) + '[^{}]*\{[^}]*font-size:16px/.test(mobile);
  if (!ok) {
    note(`поле ${inp}: в @media(max-width:600px) нет font-size>=16px — iOS зумит страницу при фокусе`);
  }
}

// ── 5. Горизонтальная прокрутка ряда чипов не должна уводить страницу ────
if (!/\.pf-tools-row:not\(:last-child\)\{[^}]*overflow-x:auto/.test(css)) {
  note('нет локальной горизонтальной прокрутки для ряда чипов профиля');
}

// ── 6. Сетевые запросы: у каждого должен быть дедлайн ───────────────────
if (!/_deadline\(/.test(apiJs) || !/timeoutMs/.test(apiJs)) {
  note('api.js без дедлайнов запросов: на мобильной сети спиннер может висеть вечно');
}
for (const [name, needle] of [['GET', 'this._deadline(controller, this.timeoutMs)'], ['мутаций', 'this.mutationTimeoutMs']]) {
  if (!apiJs.includes(needle)) note(`api.js: не найден дедлайн для ${name}`);
}
if (!/maxRetries/.test(apiJs)) {
  note('api.js без повтора GET: одиночный сетевой сбой (смена Wi-Fi↔LTE) оставляет битую плитку');
}

// ── 7. Лайк/скрытие: только явное состояние, никаких toggle-запросов ────
// Иначе оффлайн-очередь (переигровка после потери ответа) откатывает действие.
for (const call of jsSource.match(/API\.post\(`\/(?:like|hide)\/[^`]*`\)(?!\s*,)/g) || []) {
  note(`toggle-вызов без явного состояния: ${call} — переигровка очереди откатит действие`);
}
for (const m of jsSource.matchAll(/API\.post\(`\/(like|hide)\/\$\{[^}]+\}`\s*,\s*\{([^}]*)\}/g)) {
  // Принимаем и полную форму {liked:false}, и краткую {liked} — обе явные.
  const body = m[2].trim();
  const explicit = new RegExp(`^\\s*${m[1] === 'like' ? 'liked' : 'hidden'}\\s*(:|,|\\}|$)`).test(body)
    || body.includes(m[1] === 'like' ? 'liked:' : 'hidden:');
  if (!explicit) note(`POST /api/${m[1]}/: в теле нет явного состояния (${m[1] === 'like' ? 'liked' : 'hidden'}) — запрос не идемпотентен`);
}
if (!/func \(p \*Profile\) SetLiked/.test(profileGo)) {
  note('в internal/profile.go нет SetLiked — идемпотентная выставка лайка недоступна');
}
if (!/SetLiked|SetHidden/.test(handlersProfileGo)) {
  note('handlers_profile.go игнорирует явное состояние — переигровка очереди откатит лайк');
}

// ── 8. CORS: клиент использует PATCH ───────────────────────────────────
const allowMethods = (middleware.match(/Access-Control-Allow-Methods", "([^"]+)"/) || [])[1] || '';
for (const verb of ['GET', 'POST', 'PATCH', 'DELETE']) {
  if (!new RegExp(`\\b${verb}\\b`).test(allowMethods)) note(`CORS Allow-Methods без ${verb}`);
}

// ── Итог ────────────────────────────────────────────────────────────────
if (problems.length) {
  console.error('mobile check FAILED:\n' + problems.map((p) => '  ✗ ' + p).join('\n'));
  process.exit(1);
}
console.log('mobile check ok (viewport, safe-area, hover-only, тап-таргеты, зум, дедлайны API, идемпотентность лайка, CORS)');
