import { readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

function run(command, args) {
  const result = spawnSync(command, args, { stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function goFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) return goFiles(full);
    return entry.name.endsWith('.go') ? [full] : [];
  });
}

const root = resolve('.');
const files = [...goFiles(root), ...goFiles(join(root, 'internal'))]
  .filter((file, index, all) => all.indexOf(file) === index);
const formatted = spawnSync('gofmt', ['-l', ...files], { encoding: 'utf8' });
if (formatted.status !== 0) process.exit(formatted.status ?? 1);
if (formatted.stdout.trim()) {
  console.error('gofmt required:\n' + formatted.stdout.trim());
  process.exit(1);
}

run('go', ['build', '.', './internal/...']);
run('go', ['vet', '.', './internal/...']);

// staticcheck ловит то, чего не видит go vet: sqlrowserr, устаревшие API,
// подозрительные конструкции. Устанавливается отдельно
// (go install honnef.co/go/tools/cmd/staticcheck@2025.1.1), в CI — обязателен,
// локально гейт его пропускает с явной пометкой, чтобы npm run check не
// требовал сетевую установку.
//
// U1000 (unused) отключён НАМЕРЕННО: он не учитывает использование из
// _test.go и требовал удалять хелперы, на которых держатся тесты (проверено:
// setQueueFile, keyManager.sync/healthyCount, sortedOddKeys). Такие помечены
// комментарием со ссылкой на этот файл.
const hasStaticcheck = spawnSync('staticcheck', ['-version'], { stdio: 'ignore' }).error == null
  && spawnSync('staticcheck', ['-version'], { stdio: 'ignore' }).status === 0;
if (hasStaticcheck) {
  console.log('staticcheck: SA/S/ST/QF (-U1000)…');
  run('staticcheck', ['-checks=inherit,-U1000', '.', './internal/...']);
} else {
  console.log('staticcheck: не установлен — пропускаю (в CI обязателен)');
}

run('go', ['test', '.', './internal/...']);
