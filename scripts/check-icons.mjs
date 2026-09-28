// check-icons.mjs — ловит иконки, которых нет в ICONS (static/js/utils.js).
//
// Иконки тут не подгружаются из CDN: набор SVG-путей лежит вручную в ICONS,
// а разметка и JS ссылаются на имя строкой. icon() для неизвестного имени
// возвращает '' (utils.js), и renderLucideIcons() на такой вызов молча
// ничего не рисует — кнопка остаётся с пустым <i> внутри. Выглядит это как
// «иконка пропала», и ни один существующий тест это не ловил: так в разметке
// уже висели несуществующие share2 и qrCode, а в feed.js — messageSquare.
//
// Ловит только критичную сторону: имя иконки используется, а записи в ICONS
// нет — тогда кнопка остаётся с пустым <i> внутри («иконка пропала»).
// Обратную сторону (запись в ICONS, на которую никто не ссылается) НЕ ловим:
// ICONS — это маленькая библиотека, в которой полезно держать запас иконок,
// и such warning быстро превратился бы в шум, который все перестанут читать.
//
// Запуск: node scripts/check-icons.mjs (или npm run check:icons). В CI — exit 1.
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const utils = readFileSync(join(root, 'static', 'js', 'utils.js'), 'utf8');

const start = utils.indexOf('const ICONS = {');
if (start < 0) throw new Error('ICONS not found in utils.js');
const block = utils.slice(start, utils.indexOf('};', start));
const have = new Set([...block.matchAll(/^\s{2}([A-Za-z0-9_]+)\s*:/gm)].map((m) => m[1]));

const jsDir = join(root, 'static', 'js');
const files = [
  ...readdirSync(jsDir).filter((f) => f.endsWith('.js')).map((f) => join(jsDir, f)),
  join(root, 'static', 'index.html'),
];
/** имя → Set(файлы) */
const used = new Map();
for (const f of files) {
  const code = readFileSync(f, 'utf8');
  const rel = f.slice(root.length + 1).replace(/\\/g, '/');
  for (const m of code.matchAll(/data-lucide="([A-Za-z0-9_]+)"/g)) add(m[1], rel);
  for (const m of code.matchAll(/\bicon\(\s*'([A-Za-z0-9_]+)'/g)) add(m[1], rel);
}
function add(k, where) {
  if (!used.has(k)) used.set(k, new Set());
  used.get(k).add(where);
}

const problems = [];
for (const [name, where] of [...used].sort()) {
  if (!have.has(name)) {
    problems.push(`NO SUCH ICON  ${name}  (${[...where].join(', ')}) — добавь его в ICONS (static/js/utils.js)`);
  }
}

if (problems.length) {
  console.error(`check-icons FAILED (${problems.length}):\n  ${problems.join('\n  ')}`);
  process.exit(1);
}
console.log(`check-icons ok (${have.size} иконок в ICONS, ${used.size} имён используются)`);
