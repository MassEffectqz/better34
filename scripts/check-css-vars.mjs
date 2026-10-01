// check-css-vars.mjs — ловит CSS-ошибки, которые не видно глазом: обращение
// к переменной, которой нет в теме. Именно так всплывашка превью тега осталась
// без фона: в стиле стоял var(--surface1), а в теме есть только --surface,
// --surface2 и --surface3 — и блок просто рисовался прозрачным.
//
// Плюс вторая проверка, ставшая нужной после разбиения style.css на части:
// каждый .css обязан быть подключён в index.html. Без неё «создал
// static/css/09-новое.css, поправил» выглядит как сработавшая правка, а на
// странице ничего не меняется — и все остальные проверки зелёные.
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const dir = 'static/css';
const defined = new Set();
const used = new Map(); // имя → [файлы]

// Переменные, которые ставит JS прямо в элемент (style.setProperty) или
// которые пишутся с фолбэком var(--x, 0px), — валидны и без объявления
// в :root. Иначе проверка ругалась бы на нормальный код.
const dynamic = new Set();
for (const f of readdirSync('static/js')) {
  if (!f.endsWith('.js')) continue;
  const js = readFileSync(join('static/js', f), 'utf8');
  for (const m of js.matchAll(/setProperty\(\s*['"](--[a-zA-Z0-9-]+)/g)) dynamic.add(m[1]);
}
// Переменные, заданные инлайном в разметке (style="--sw:#a78bfa"), — тоже
// легитимны: цвет образцов акцента задаётся прямо в index.html.
try {
  const html = readFileSync('static/index.html', 'utf8');
  for (const m of html.matchAll(/style="[^"]*?(--[a-zA-Z0-9-]+)\s*:/g)) dynamic.add(m[1]);
} catch { /* разметки нет — не страшно */ }

for (const f of readdirSync(dir).filter((n) => n.endsWith('.css'))) {
  const path = join(dir, f);
  const css = readFileSync(path, 'utf8');
  // Объявления: --name: в :root и в темах [data-theme="..."].
  for (const m of css.matchAll(/(--[a-zA-Z0-9-]+)\s*:/g)) defined.add(m[1]);
  // Использования: var(--name …). Имя с дефисом читаем до пробела/запятой/скобки.
  for (const m of css.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)\s*[,)]/g)) {
    // var(--x, …) — есть фолбэк, объявление не требуется.
    if (m[0].includes(',')) continue;
    if (!used.has(m[1])) used.set(m[1], []);
    const files = used.get(m[1]);
    if (!files.includes(f)) files.push(f);
  }
}

// ── Каждая часть подключена? ───────────────────────────────────────────────
// Проверяем в обе стороны: файл без <link> не применяется вовсе, а <link> на
// несуществующий файл — 404 и тихая потеря всех правил из него. Порядок
// <link> в index.html дополнительно сверяем с порядком имён файлов: от него
// зависит каскад, и перестановка молча поменяла бы, какое правило победит.
const html = (() => {
  try { return readFileSync('static/index.html', 'utf8'); } catch { return ''; }
})();
if (html) {
  const linked = [...html.matchAll(/<link[^>]+rel="stylesheet"[^>]+href="\/static\/css\/([^"?]+)(?:\?[^"]*)?"/g)]
    .map((m) => m[1])
    .filter((n) => n.endsWith('.css'));
  const onDisk = readdirSync(dir).filter((n) => n.endsWith('.css')).sort();
  const notLinked = onDisk.filter((f) => !linked.includes(f));
  const missing = linked.filter((f) => !onDisk.includes(f));
  if (notLinked.length || missing.length) {
    if (notLinked.length) {
      console.error('css: файлы не подключены в index.html (правила не применятся):');
      for (const f of notLinked) console.error(`  ${f} — добавь <link rel="stylesheet" href="/static/css/${f}?v=__VERSION__">`);
    }
    if (missing.length) {
      console.error('css: index.html ссылается на несуществующие файлы:');
      for (const f of missing) console.error(`  ${f}`);
    }
    process.exit(1);
  }
  const order = linked.join(',');
  const sorted = [...linked].sort().join(',');
  if (order !== sorted) {
    console.error(`css: порядок <link> в index.html не совпадает с порядком имён: ${order}`);
    console.error(`Подсказка: имена частей задают каскад — отсортируйте по алфавиту (${sorted}).`);
    process.exit(1);
  }
}

const missingVars = [...used.entries()].filter(
  ([name]) => !defined.has(name) && !dynamic.has(name)
);
if (missingVars.length) {
  console.error('css: используются необъявленные переменные:');
  for (const [name, files] of missingVars) console.error(`  ${name} (${files.join(', ')})`);
  console.error(`Подсказка: объявите переменную в :root или используйте существующую (${defined.size} объявлено).`);
  process.exit(1);
}

// ── Скрываемые элементы действительно скрываются? ───────────────────────────
// Общего правила .hidden в наборе нет: каждый компонент объявляет своё
// «X.hidden{display:none}». Пока так — легко забыть про один элемент, и
// `el.classList.add('hidden')` молча ничего не сделает: базовый класс с
// display:flex/block ( специфичность 0,1,0 ) перебьёт одиночный .hidden.
// Так лента выглядела пустой чёрной страницей (#friend-profile) и не
// прятались кнопки «обновить подборку», «продолжить», «объединить дубликаты».
// Проверяем, что у каждого class="… hidden" есть правило, которое его гасит.
if (html) {
  const allCss = readdirSync(dir)
    .filter((n) => n.endsWith('.css'))
    .sort()
    .map((n) => readFileSync(join(dir, n), 'utf8'))
    .join('\n');

  // Совпадение по классу или по id: .btn-icon.hidden, #btn-merge-dups.hidden.
  const hasHideRule = (name) =>
    new RegExp(`\\.[A-Za-z0-9_-]*${name.replace(/-/g, '\\-')}\\.hidden`).test(allCss);

  const suspects = new Map(); // ключ → описание
  // Класс hidden должен быть ОТДЕЛЬНЫМ словом: preset-tag-hidden — это про
  // тег «скрытые теги», а не про скрытие элемента.
  for (const m of html.matchAll(/<[^>]*\bclass="([^"]*)"[^>]*>/g)) {
    const classes = m[1].split(/\s+/).filter(Boolean);
    if (!classes.includes('hidden')) continue;
    const tag = m[0];
    const id = (tag.match(/\bid="([^"]+)"/) || [])[1];
    const own = classes.filter((c) => c !== 'hidden' && !c.endsWith('-hidden'));
    // Элемент скрыт, если правило есть хотя бы для одного его класса.
    // Проверять все, а не последний: .modal.hidden покрывает и
    // <div class="modal help-modal hidden">, а .btn-icon.hidden — и .btn-icon-sm.
    if (own.some((c) => hasHideRule(c))) continue;
    if (id && new RegExp(`#${id.replace(/-/g, '\\-')}\\.hidden`).test(allCss)) continue;
    if (!own.length) continue;
    suspects.set(id ? `#${id}` : `.${own[0]}`, own[0]);
  }
  if (suspects.size) {
    console.error('css: элементы с классом hidden, для которых нет правила скрытия:');
    for (const [key, cls] of suspects) {
      console.error(`  ${key} (.${cls}.hidden) — добавь .${cls}.hidden{display:none}`);
    }
    console.error('Подсказка: без этого classList.add(\'hidden\') ничего не скроет.');
    process.exit(1);
  }
}

console.log(`css check ok (${defined.size} переменных, ${used.size} используются, все части подключены, .hidden работает)`);
