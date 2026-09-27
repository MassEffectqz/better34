// need-frontend-build.mjs — печатает «yes», если esbuild-бандл отсутствует или
// устарел относительно исходников. Пустой вывод = пересборка не нужна.
//
// Живёт отдельным скриптом, а не однострочником в run.cmd: логика mtime в батче
// ломается на экранировании `|` и `>` внутри for /f, а здесь её можно и
// прочитать, и проверить. Пути в сообщении печатаются слэшами — чтобы
// отличить предупреждение от мусора консоли.
import { readdirSync, statSync, existsSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

const BUNDLE = join('static', 'js', 'dist', 'app.js');
const SRC = join('static', 'js');
// Каталоги, где лежат не наши исходники.
const SKIP = ['dist', 'tests', 'node_modules'];

const newestSource = (dir) => {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (SKIP.includes(entry.name)) continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      newest = Math.max(newest, newestSource(full));
    } else if (entry.name.endsWith('.js')) {
      newest = Math.max(newest, statSync(full).mtimeMs);
    }
  }
  return newest;
};

const bundle = existsSync(BUNDLE) ? statSync(BUNDLE) : null;
const src = newestSource(SRC);
// Бандл старше любого исходника → esbuild обязан пересобрать. Сравниваем и
// равенство: запуск с --print в ту же секунду счёт не должен ломать.
if (!bundle || src >= bundle.mtimeMs) console.log('yes');
if (!bundle) {
  console.error(`[need-build] no bundle at ${relative(process.cwd(), BUNDLE).split(sep).join('/')}`);
}
