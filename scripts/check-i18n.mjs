// Проверка словарей i18n: одинаковый набор ключей в RU/EN и что все
// t('…')/tf('…')/data-i18n="…" в статике существуют хотя бы в одном словаре.
// Запуск: node scripts/check-i18n.mjs (или npm run check:i18n). В CI — exit 1.
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const src = readFileSync(join(root, 'static', 'js', 'i18n.js'), 'utf8');

// Ключи каждого словаря — по строкам вида 'a.b': '...' внутри const RU/EN { ... };
function dictKeys(name) {
  const m = new RegExp(`const ${name} = \\{([\\s\\S]*?)\\n\\};`).exec(src);
  if (!m) throw new Error(`dict ${name} not found in i18n.js`);
  const all = [...m[1].matchAll(/^\s*'([^']+)'\s*:/gm)].map(x => x[1]);
  const seen = new Set(), dups = new Set();
  for (const k of all) { if (seen.has(k)) dups.add(k); else seen.add(k); }
  return { keys: seen, dups: [...dups] };
}
const ru = dictKeys('RU'), en = dictKeys('EN');
const problems = [];
for (const [name, d] of [['RU', ru], ['EN', en]]) {
  for (const k of d.dups) problems.push(`DUPLICATE KEY in ${name}  ${k}`);
}

// Использование ключей: t('k') / tf('k', …) в JS и data-i18n="k" в HTML.
const jsDir = join(root, 'static', 'js');
const files = [
  ...readdirSync(jsDir).filter(f => f.endsWith('.js')).map(f => join(jsDir, f)),
  join(root, 'static', 'index.html'),
];
const used = new Map(); // key -> Set(files)
for (const f of files) {
  const code = readFileSync(f, 'utf8');
  const rel = f.slice(root.length + 1).replace(/\\/g, '/');
  for (const m of code.matchAll(/(?:^|[^\w.$])t(?:f)?\(\s*'([\w.]+)'/g)) add(m[1], rel);
  for (const m of code.matchAll(/data-i18n(?:-title|-ph|-aria)?="([\w.]+)"/g)) add(m[1], rel);
}
function add(k, where) {
  if (!used.has(k)) used.set(k, new Set());
  used.get(k).add(where);
}

for (const [k, where] of [...used].sort()) {
  if (!ru.keys.has(k) && !en.keys.has(k)) problems.push(`NO SUCH KEY  ${k}  (${[...where].join(', ')})`);
  else if (!ru.keys.has(k)) problems.push(`MISSING IN RU  ${k}`);
  else if (!en.keys.has(k)) problems.push(`MISSING IN EN  ${k}`);
}
for (const k of ru.keys) if (!en.keys.has(k)) problems.push(`ONLY IN RU  ${k}`);
for (const k of en.keys) if (!ru.keys.has(k)) problems.push(`ONLY IN EN  ${k}`);

// Interpolation placeholders must match between dictionaries, otherwise the EN
// build silently drops a value.
function ph(name) {
  const m = new RegExp(`const ${name} = \\{([\\s\\S]*?)\\n\\};`).exec(src);
  const out = new Map();
  for (const x of m[1].matchAll(/^\s*'([^']+)'\s*:\s*'([^']*)'/gm)) {
    out.set(x[1], new Set([...x[2].matchAll(/\{(\w+)\}/g)].map(y => y[1])));
  }
  return out;
}
const ruPh = ph('RU'), enPh = ph('EN');
for (const [k, set] of ruPh) {
  const other = enPh.get(k);
  if (!other) continue;
  const a = [...set].sort().join(','), b = [...other].sort().join(',');
  if (a !== b) problems.push(`PLACEHOLDER MISMATCH  ${k}: ru{${a}} en{${b}}`);
}

const unused = [...ru.keys].filter(k => !used.has(k));
console.log(`i18n: RU=${ru.keys.size} EN=${en.keys.size} used=${used.size} unused=${unused.length}`);
if (unused.length) console.log('not referenced (ok if dynamic): ' + unused.join(', '));
if (problems.length) {
  console.error('i18n check FAILED:');
  for (const p of problems) console.error('  ' + p);
  process.exit(1);
}
console.log('i18n check ok');
