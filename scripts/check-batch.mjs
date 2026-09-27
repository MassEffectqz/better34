// check-batch.mjs — ловушки парсинга cmd.exe в .cmd/.bat.
//
// Проверка не «просто есть ли скобки», а две конкретные ошибки, на которые
// проект уже наступал дважды:
//
// 1. Скобки в тексте echo ВНУТРИ блока ( ... ). cmd не отличает их от скобок
//    группировки команд: «echo build (npm run build)...» разбирается как
//    вложенная группа, а остаток строки cmd считает командой и падает с
//    «Непредвиденное появление: ...». Вне блока строка безопасна, поэтому баг
//    не воспроизводится с --print и всплывает только на реальном запуске.
//    В rem-строках скобки безопасны всегда.
//
// 2. Метасимволы | > < & внутри backtick-команды у for /f. Внутри backtick нужен
//    ^|, но caret доходит до вложенной команды литералом — так PowerShell
//    получал «^|» и падал. Лечится переносом логики в отдельный скрипт (как
//    scripts/need-frontend-build.mjs).
//
// Проверка построчная и эвристическая: она не разбирает весь язык cmd, а
// отсекает ровно эти два класса. Ложные срабатывания опаснее пропусков, поэтому
// сомнительные строки (кавычки, экранирование) не трогаем.
import { readdirSync, readFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

const problems = [];
const files = [];
const walk = (dir) => {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (['node_modules', '.git', 'data', 'tmp', 'static', 'dist'].includes(e.name)) continue;
    const full = join(dir, e.name);
    if (e.isDirectory()) walk(full);
    else if (/\.(cmd|bat)$/i.test(e.name)) files.push(full);
  }
};
walk('.');

for (const f of files) {
  const rel = relative(process.cwd(), f).split(sep).join('/');
  const lines = readFileSync(f, 'utf8').split(/\r?\n/);
  let depth = 0; // вложенность блока ( ... )

  lines.forEach((raw, i) => {
    const n = i + 1;
    const line = raw.trim();
    if (!line || line.startsWith('::')) return;

    // Правило 2: for /f с backtick-командой и неэкранированными метасимволами.
    if (/^for\s+\/f\b/i.test(line)) {
      const bt = line.match(/`([^`]*)`/);
      if (bt && /[|<>&]/.test(bt[1])) {
        problems.push(`${rel}:${n}  for /f: | < & внутри backticks — cmd передаст ^| литералом. Выносите логику в отдельный скрипт.`);
      }
    }

    if (/^rem\b/i.test(line) || line.startsWith(':')) return; // скобки в rem безопасны

    const opens = (line.match(/\(/g) || []).length;
    const closes = (line.match(/\)/g) || []).length;
    // Правило 1: echo со скобками внутри блока.
    if (depth > 0 && /^echo\b/i.test(line) && (opens || closes)) {
      const safe = /\^[()]/.test(line) || /"[^"]*[()][^"]*"/.test(line);
      if (!safe) {
        problems.push(`${rel}:${n}  echo со скобками внутри блока ( ... ): cmd примет скобки за группу команд, а остаток строки — за команду («Непредвиденное появление»). Уберите скобки или экранируйте ^( ^).`);
      }
    }
    depth += opens - closes;
    if (depth < 0) depth = 0;
  });
}

if (problems.length) {
  console.error(`check-batch FAILED (${problems.length}):\n  ${problems.join('\n  ')}`);
  process.exit(1);
}
console.log(`check-batch ok (${files.length} файлов, ловушки парсинга cmd не найдены)`);
