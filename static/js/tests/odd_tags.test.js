// odd_tags.test.js — объяснения к тегам при наведении в просмотре поста:
//  * три уровня: yellow/красят тег, note — только текст подсказки;
//  * descOf отдаёт голый текст, без префикса «Странный —»;
//  * обычный тег объяснения не получает;
//  * старый формат (список) не ломает разбор.
import { OddTags } from '../odd_tags.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function withFetch(payload, fn) {
  const saved = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => payload });
  return Promise.resolve().then(fn).finally(() => { globalThis.fetch = saved; });
}

await withFetch(
  {
    yellow: { tomboy: 'Девушка с мальчишеской внешностью' },
    red: { furry: 'Человекоподобное существо с чертами животного' },
    note: { solo: 'В кадре один персонаж', long_hair: 'Длинные волосы' },
  },
  async () => {
    await OddTags.load();
    // Уровни различаются — по ним viewer вешает цвет.
    check('уровень yellow', OddTags.levelOf('tomboy') === 'yellow', OddTags.levelOf('tomboy'));
    check('уровень red', OddTags.levelOf('furry') === 'red', OddTags.levelOf('furry'));
    check('третий уровень note', OddTags.levelOf('solo') === 'note', OddTags.levelOf('solo'));
    // Подсказка — только текст, без «Странный —» и без названия уровня.
    check('текст для yellow', OddTags.descOf('tomboy') === 'Девушка с мальчишеской внешностью', OddTags.descOf('tomboy'));
    check('текст для red', OddTags.descOf('furry') === 'Человекоподобное существо с чертами животного');
    check('текст для note', OddTags.descOf('solo') === 'В кадре один персонаж');
    check('текст note без префикса', !String(OddTags.descOf('solo')).includes('note'));
    check('обычный тег без объяснения', OddTags.levelOf('1girl') === null && OddTags.descOf('1girl') === null);
    check('регистр игнорируется', OddTags.levelOf('FURRY') === 'red' && OddTags.descOf('FURRY') !== null);
    check('пробелы обрезаются', OddTags.levelOf('  solo  ') === 'note');
  }
);

// Конфликт: тег в двух уровнях — побеждает более строгий.
await withFetch({ yellow: { furry: 'мягкое' }, red: { furry: 'строгое' } }, async () => {
  await OddTags.load();
  check('red побеждает yellow', OddTags.levelOf('furry') === 'red', OddTags.levelOf('furry'));
  check('текст строгого уровня', OddTags.descOf('furry') === 'строгое', OddTags.descOf('furry'));
});

// note не должен перебивать цветные уровни.
await withFetch({ red: { solo: 'красный solo' }, note: { solo: 'просто solo' } }, async () => {
  await OddTags.load();
  check('red сильнее note', OddTags.levelOf('solo') === 'red', OddTags.levelOf('solo'));
});

// Старый формат со списком: уровень распознаётся, но текста нет.
await withFetch({ red: ['furry'] }, async () => {
  await OddTags.load();
  check('старый формат: уровень есть', OddTags.levelOf('furry') === 'red');
  check('старый формат: текста нет', OddTags.descOf('furry') === null, OddTags.descOf('furry'));
});

// Ответ без уровней / мусор — не падаем.
await withFetch({ yellow: null, red: null, note: null }, async () => {
  await OddTags.load();
  check('null-ответ не роняет', OddTags.descOf('furry') === null);
});

// Сервер недоступен.
{
  const saved = globalThis.fetch;
  globalThis.fetch = async () => { throw new Error('offline'); };
  await OddTags.load();
  globalThis.fetch = saved;
  check('без сервера не падает', OddTags.descOf('furry') === null && OddTags.levelOf('furry') === null);
}

console.log(`odd_tags: ${passed} passed, ${failed} failed`);
if (failed) process.exit(1);