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
  // tp('k', n) — числовые формы. Ключи 'k1'/'k2'/'k5' собираются внутри tp()
  // конкатенацией, поэтому статический разбор их не видит. Регистрируем явно:
  // так опечатка в имени формы всё равно даст NO SUCH KEY, а не «неиспользуемый
  // ключ» в белом списке.
  for (const m of code.matchAll(/(?:^|[^\w.$])tp\(\s*'([\w.]+)'/g)) {
    // Базового ключа в словаре нет — есть только три формы.
    add(m[1] + '1', rel);
    add(m[1] + '2', rel);
    add(m[1] + '5', rel);
  }
  // T('k', fallback) — локальный алиас t() в классических скриптах
  // (sw-register.js подключается без module и берёт словарь из window).
  for (const m of code.matchAll(/(?:^|[^\w.$])T\(\s*'([\w.]+)'/g)) add(m[1], rel);
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

// Строгий режим (по умолчанию): неиспользуемый ключ — ошибка. Ключи err.*
// собираются динамически в API._err ('err.' + code из ответа сервера) — их
// статический разбор не видит, поэтому они в белом списке. Аналогично
// friends.err* берутся из friendStatusErr[...] и попадают в t() переменной
// (friendErrText) — тот же случай динамического поиска.
// Отключить строгость: node scripts/check-i18n.mjs --allow-unused
const ALLOW_UNUSED = process.argv.includes('--allow-unused');
const DYNAMIC_PREFIXES = ['err.', 'friends.err'];
const unused = [...ru.keys].filter((k) => !used.has(k) && !DYNAMIC_PREFIXES.some((p) => k.startsWith(p)));
console.log(`i18n: RU=${ru.keys.size} EN=${en.keys.size} used=${used.size} unused=${unused.length}`);
if (unused.length) {
  if (ALLOW_UNUSED) {
    console.log('not referenced (ok if dynamic): ' + unused.join(', '));
  } else {
    console.error(`UNUSED KEYS (${unused.length}) — удалите или используйте:\n  ${unused.join('\n  ')}`);
    process.exit(1);
  }
}
// ── Пользовательские строки: русский литерал в UI — ошибка ───────────────
// Комментарии и логи в порядке (они не в этих позициях), а вот строки,
// которые видит пользователь, обязаны идти через t()/tf() или data-i18n-*.
const CYR = /[А-Яа-яЁё]/;
const UI_POS = [
  /showToast(?:WithUndo)?\(\s*'([^']*)'/g, /title:\s*'([^']*)'/g, /aria-label:\s*'([^']*)'/g,
  /placeholder:\s*'([^']*)'/g, /(?:textContent|innerHTML)\s*=\s*'([^']*)'/g,
  /(?:title|aria-label|placeholder)="([^"]*)"/g,
];
const literals = [];
const htmlSrc = readFileSync(join(root, 'static', 'index.html'), 'utf8');
for (const f of files) {
  if (!f.endsWith('.js')) continue;
  const srcText = readFileSync(f, 'utf8');
  srcText.split('\n').forEach((line, i) => {
    if (line.trimStart().startsWith('//') || line.trimStart().startsWith('*')) return;
    for (const re of UI_POS) {
      re.lastIndex = 0;
      let m;
      while ((m = re.exec(line)) !== null) {
        if (CYR.test(m[1])) literals.push(`${f.replace(root, '.')}:${i + 1}  ${m[1].slice(0, 60)}`);
      }
    }
  });
}
// Разметка: атрибут с русским текстом обязан иметь пару data-i18n-* на строке.
htmlSrc.split('\n').forEach((line, i) => {
  if (!CYR.test(line)) return;
  for (const attr of ['title', 'aria-label', 'placeholder']) {
    const m = new RegExp(`${attr}="([^"]*[А-Яа-яЁё][^"]*)"`).exec(line);
    if (!m) continue;
    const dataAttr = attr === 'aria-label' ? 'data-i18n-aria' : attr === 'placeholder' ? 'data-i18n-ph' : 'data-i18n-title';
    if (!line.includes(dataAttr)) literals.push(`index.html:${i + 1}  ${attr}="${m[1].slice(0, 60)}" (нет ${dataAttr})`);
  }
});
if (literals.length) {
  problems.push(`HARDCODED RU STRINGS (${literals.length}):\n    ${literals.join('\n    ')}`);
}

// ── Итог ────────────────────────────────────────────────────────────────
if (problems.length) {
  console.error('i18n check FAILED:');
  for (const p of problems) console.error('  ' + p);
  process.exit(1);
}
console.log('i18n check ok');
