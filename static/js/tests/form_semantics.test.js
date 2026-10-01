// @ts-check
//
// Смысл форм в разметке. Chrome (и другие движки) ругается в консоль:
//   [DOM] Multiple forms should be contained in their own form elements
// на любую <form>, в которой больше одной самостоятельной группы полей.
//
// Раньше #api-keys-list был <form>, внутрь которой JS рисовал по три поля на
// каждую запись (имя / ключ / user_id) — то есть ровно та «сложная форма» из
// нескольких несвязанных действий. При этом форма никогда не отправлялась:
// onsubmit="return false" глушил всё, а сохранение идёт из App.saveSettings
// по this.state.apiKeys одним POST /settings. Контейнер переименован в <div>.
//
// Тест ловит возврат регрессии в обе стороны: и появление <form> там, где
// группа полей не одна, и исчезновение рабочего контейнера.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(fileURLToPath(new URL('../../', import.meta.url)));
const html = readFileSync(join(root, 'index.html'), 'utf8');
const settings = readFileSync(join(root, 'js', 'settings.js'), 'utf8');

test('контейнер API-ключей — не форма (иначе Chrome ругается на multiple forms)', () => {
  assert.match(html, /<div id="api-keys-list">/,
    'контейнер ключей должен быть <div id="api-keys-list">');
  assert.doesNotMatch(html, /<form[^>]*id="api-keys-list"/,
    'id="api-keys-list" на <form> вернёт предупреждение о нескольких формах');
  // onsubmit на контейнере больше не нужен: отправки не происходит.
  const tag = /<[^>]*id="api-keys-list"[^>]*>/.exec(html)?.[0] ?? '';
  assert.ok(tag, 'не нашли тег контейнера #api-keys-list');
  assert.doesNotMatch(tag, /onsubmit/,
    'onsubmit на контейнере больше не нужен: отправки не происходит');
});

test('поля ключа сами гасят автодополнение, раз атрибута на форме больше нет', () => {
  // autocomplete="off" переехал с формы на сами поля. Без этого браузер
  // начал бы предлагать сохранённые ключи и логины в поле user_id.
  for (const cls of ['api-key-name', 'api-key-value', 'api-key-uid']) {
    const field = new RegExp(
      `class="${cls}"[^>]*autocomplete="off"|<input[^>]*autocomplete="off"[^>]*class="${cls}"`,
    ).test(settings);
    assert.ok(field, `поле .${cls} должно иметь autocomplete="off"`);
  }
});

test('в разметке не осталось форм с несколькими несвязанными группами полей', () => {
  // Грубая, но достаточная проверка: каждая оставшаяся <form> обязана
  // содержать не больше одной submit-кнопки и одного блока полей.
  for (const form of html.match(/<form[\s\S]*?<\/form>/g) ?? []) {
    const submitButtons = (form.match(/<button(?![^>]*type="button")/g) ?? []).length;
    assert.ok(submitButtons <= 1,
      `форма с ${submitButtons} submit-кнопками — это несколько действий в одной форме: ${form.slice(0, 120)}`);
    assert.match(form, /onsubmit="return false;"|<button/,
      'форма без onsubmit="return false" перезагрузит страницу по Enter');
  }
});

test('сохранение ключей не завязано на отправку формы', () => {
  // Регрессия на случай, если кто-то решит вернуть <form> и повесить
  // обработчик submit: состояние собирается из полей, а не из формы.
  assert.doesNotMatch(settings, /api-keys-list[\s\S]{0,200}addEventListener\('submit'/,
    'App.renderAPIKeys не должен слушать submit — списка-формы больше нет');
  assert.match(settings, /const apiKeys = \(this\.state\.apiKeys \|\| \[\]\)/,
    'saveSettings должен собирать ключи из this.state.apiKeys');
});