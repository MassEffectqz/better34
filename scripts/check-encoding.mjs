// Проверка кодировки: PowerShell-редактирование однажды испортило кириллицу
// в исходниках (двойное перекодирование). Скрипт ловит это до сборки.
import fs from 'fs';

const files = [
  'internal/odd_tags.go',
  'internal/odd_tags_default.json',
  'static/js/odd_tags.js',
  'static/js/viewer.js',
];
let bad = 0;
for (const f of files) {
  const b = fs.readFileSync(f);
  const s = b.toString('utf8');
  const bom = b[0] === 0xef && b[1] === 0xbb && b[2] === 0xbf;
  // U+FFFD — признак нечитаемого UTF-8; «Рџ» в начале слова — двойное кодирование.
  const hasReplacement = s.includes('�');
  // След двойного кодирования: кириллическая буква, за ней символы Latin-1
  // («Рї», «Рµ»). Проверка «Р + кириллица» давала бы ложные срабатывания на
  // обычных словах вроде «Результат».
  const hasMojibake = /[\u0400-\u04FF][\u0080-\u00BF]{2}/.test(s);
  const hasCyrillic = /[\u0430-\u044F\u0410-\u042F]/.test(s);
  if (hasReplacement || hasMojibake || bom) {
    bad++;
    console.log(`BAD  ${f}: replacement=${hasReplacement} mojibake=${hasMojibake} bom=${bom}`);
  } else {
    console.log(`ok   ${f}: cyrillic=${hasCyrillic}`);
  }
}
console.log(bad ? `FAILED: ${bad} file(s)` : 'encoding OK');
process.exit(bad ? 1 : 0);