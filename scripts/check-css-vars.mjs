// check-css-vars.mjs — ловит CSS-ошибки, которые не видно глазом: обращение
// к переменной, которой нет в теме. Именно так всплывашка превью тега осталась
// без фона: в стиле стоял var(--surface1), а в теме есть только --surface,
// --surface2 и --surface3 — и блок просто рисовался прозрачным.
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

const missing = [...used.entries()].filter(
  ([name]) => !defined.has(name) && !dynamic.has(name)
);
if (missing.length) {
  console.error('css: используются необъявленные переменные:');
  for (const [name, files] of missing) console.error(`  ${name} (${files.join(', ')})`);
  console.error(`Подсказка: объявите переменную в :root или используйте существующую (${defined.size} объявлено).`);
  process.exit(1);
}
console.log(`css check ok (${defined.size} переменных, ${used.size} используются)`);
