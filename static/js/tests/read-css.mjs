// read-css.mjs — единая точка чтения стилей для тестов.
//
// CSS разбит на части static/css/*.css (бывший единый style.css). Тестам,
// которые проверяют правила регексом по всему набору, нужен весь CSS
// склеенным. Порядок — по именам файлов: числовые префиксы (00-, 01-, …)
// задают порядок каскада, и именно он важен для проверок вроде «в @media
// есть font-size >= 16px». Правило, найденное в первой подходящей части,
// должно быть тем же самым, что и в каскаде страницы.
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const CSS_DIR = join(fileURLToPath(new URL('../../css/', import.meta.url)));

/** @returns {string[]} имена частей в порядке подключения */
export function cssParts() {
  return readdirSync(CSS_DIR).filter((f) => f.endsWith('.css')).sort();
}

/** @returns {string} все части, склеенные в порядке каскада */
export function readAllCss() {
  return cssParts()
    .map((f) => readFileSync(join(CSS_DIR, f), 'utf8'))
    .join('\n');
}
