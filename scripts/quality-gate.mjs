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
run('go', ['test', '.', './internal/...']);
