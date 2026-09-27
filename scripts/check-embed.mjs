// check-embed.mjs — страховка от повторения бага «свежий клон не собирается».
//
// go:embed требует, чтобы каждый путь паттерна существовал и содержал хотя бы
// один файл. Каталоги продуктов сборки (static/js/dist) в .gitignore, поэтому
// на свежем клоне их нет — и `go build`/`go run` падал с
// «pattern static/js/dist: no matching files found». На машине автора каталог
// создавался билдом npm run build, а CI проходил случайно: check:go идёт после
// build. Починили через all:static/js/dist + .gitkeep.
//
// Проверяем класс, а не этот каталог:
//   1. все пути из //go:embed существуют;
//   2. каталог, где лежат одни точечные файлы (.gitkeep), обязан идти с all: —
//      embed по умолчанию файлы с точкой пропускает и такой паттерн не найдёт;
//   3. для каталога-продукта есть исключение в .gitignore, иначе заглушка не
//      попадёт в репозиторий и баг вернётся молча.
import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

const problems = [];
const src = readFileSync('static.go', 'utf8');
// Директив может быть несколько (в этом файле их две) — собираем все.
const directives = [...src.matchAll(/^\/\/go:embed\s+(.+)$/gm)].map((m) => m[1].trim());
if (!directives.length) {
  console.error('check-embed: не найдена директива //go:embed в static.go');
  process.exit(1);
}

const paths = directives.flatMap((d) => d.split(/\s+/));
// Каталоги, чьё содержимое целиком игнорируется git (продукт сборки).
const gitignoredDirs = ['static/js/dist'];

// Пути с * — это glob (static/js/*.js), а не буквальный путь: раскрываем
// префикс до первой звёздочки и ищем совпадения в каталоге.
const globRe = (p) => new RegExp('^' + p.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*') + '$');
const globMatches = (p) => {
  const base = p.slice(0, p.indexOf('*'));
  if (!base || !existsSync(base)) return null;
  const dir = base.slice(0, base.lastIndexOf('/') + 1);
  const re = globRe(p.slice(dir.length));
  return readdirSync(dir).filter((e) => re.test(e)).length > 0;
};

for (const p of paths) {
  const raw = p.startsWith('all:') ? p.slice(4) : p;
  if (raw.includes('*')) {
    if (globMatches(raw) === false) {
      problems.push(`EMPTY-GLOB  ${p} — ни одного файла под паттерн, //go:embed упадёт на сборке`);
    }
    continue;
  }
  if (!existsSync(raw)) {
    problems.push(`MISSING  ${p} — каталога/файла нет, //go:embed упадёт на сборке`);
    continue;
  }
  if (!statSync(raw).isDirectory()) continue;
  const entries = readdirSync(raw);
  if (entries.length === 0) {
    problems.push(`EMPTY    ${p} — пустой каталог, паттерн ничего не найдёт`);
    continue;
  }
  const onlyDotted = entries.every((e) => e.startsWith('.'));
  if (onlyDotted && !p.startsWith('all:')) {
    problems.push(`NO-ALL   ${p} — внутри только ${entries.join(', ')}, а embed без all: файлы с точкой пропускает`);
  }
}

// Заглушка для игнорируемого каталога должна быть и на диске, и в .gitignore.
const ignore = readFileSync('.gitignore', 'utf8');
const slash = (p) => relative(process.cwd(), p).split(sep).join('/');
for (const dir of gitignoredDirs) {
  const keep = slash(join(dir, '.gitkeep'));
  if (!existsSync(keep)) problems.push(`NO-KEEP   ${keep} — заглушка пропала, //go:embed перестанет матчиться на клоне`);
  if (!ignore.includes(`!${keep}`)) {
    problems.push(`NO-UNIGNORE  !${keep} — нет исключения в .gitignore, заглушка не попадёт в репозиторий`);
  }
}

if (problems.length) {
  console.error(`check-embed FAILED (${problems.length}):\n  ${problems.join('\n  ')}`);
  process.exit(1);
}
console.log(`check-embed ok (${directives.length} директив, ${paths.length} путей, заглушки на месте)`);
