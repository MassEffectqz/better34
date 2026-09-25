// suggest_precision.test.js — точность автодополнения в строке поиска:
//  * активное слово считается ПО КАРЕТКЕ, а не по последнему в строке;
//  * выбор подсказки в середине запроса не стирает последующие теги;
//  * служебный префикс (-, ~) не уходит в запрос автодополнения и сохраняется;
//  * NL-запрос (?фраза) не предлагает теги;
//  * mergeSuggestions: точное совпадение первым, мусорный апстрим отсекается.
import { App } from '../state.js';
import { API } from '../api.js';
import '../search.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

function makeClassList(initial) {
  const s = new Set(initial || []);
  return {
    contains(c) { return s.has(c); },
    add(c) { s.add(c); },
    remove(c) { s.delete(c); },
    toggle(c, force) {
      const on = force != null ? !!force : !s.has(c);
      if (on) s.add(c); else s.delete(c);
      return on;
    },
  };
}

function makeEl() {
  return {
    style: {}, classList: makeClassList(), children: [], dataset: {},
    innerHTML: '', textContent: '', value: '',
    appendChild(c) { this.children.push(c); },
    addEventListener() {}, removeEventListener() {},
    setAttribute() {}, removeAttribute() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
  };
}

globalThis.window = {};
globalThis.document = {
  activeElement: null,
  querySelector() { return null; },
  querySelectorAll() { return []; },
  createElement: () => makeEl(),
  addEventListener() {},
};
globalThis.localStorage = {
  _s: {},
  getItem(k) { return this._s[k] != null ? this._s[k] : null; },
  setItem(k, v) { this._s[k] = String(v); },
  removeItem(k) { delete this._s[k]; },
};

// input со «настоящей» кареткой: selectionStart выставляется тестом.
function makeInput(value, caret) {
  const el = makeEl();
  el.value = value;
  el.selectionStart = caret == null ? value.length : caret;
  el.setSelectionRange = (a, b) => { el.selectionStart = a; el.selectionEnd = b; };
  return el;
}

function makeApp() {
  const a = Object.create(App);
  a.state = { query: '', recommendActive: false, profile: { fav_tags: [], hidden_tags: [] } };
  a.els = {
    searchInput: makeInput(''),
    searchClear: makeEl(),
    suggestions: makeEl(),
    searchChips: makeEl(),
    queryMeta: makeEl(),
    historyDropdown: makeEl(),
  };
  a.rendered = [];
  a.renderSuggestions = function (tags) { a.rendered.push((tags || []).map(t => t.value || t)); };
  a.updateQueryMeta = () => {};
  a.renderQueryChips = () => {};
  a.hideHistory = () => {};
  a.showHistory = () => {};
  a.renderModeBar = () => {};
  a.search = () => {};
  a._lastSearchQuery = '';
  return a;
}

console.log('suggest precision tests\n');

// ── 1. Активное слово по каретке ───────────────────────────────────────────
{
  const a = makeApp();
  // Запрос «breast girl», каретка после «breast» (позиция 6).
  a.els.searchInput = makeInput('breast girl', 6);
  const w = a.suggestWord(a.els.searchInput);
  check('каретка в середине: слово под кареткой, не последнее',
    w.word === 'breast' && w.start === 0 && w.end === 6, JSON.stringify(w));

  const b = makeApp();
  b.els.searchInput = makeInput('breast girl', 11);
  const w2 = b.suggestWord(b.els.searchInput);
  check('каретка в конце: последнее слово', w2.word === 'girl' && w2.start === 7 && w2.end === 11,
    JSON.stringify(w2));

  // Разделитель «|» — граница слова (ленты-чередования).
  const c = makeApp();
  c.els.searchInput = makeInput('breast | girl', 10);
  const w3 = c.suggestWord(c.els.searchInput);
  check('| обрезает слово', w3.word === 'girl' && w3.start === 9, JSON.stringify(w3));

  // Служебный префикс отделяется от имени тега.
  const d = makeApp();
  d.els.searchInput = makeInput('cat -brea', 9);
  const w4 = d.suggestWord(d.els.searchInput);
  check('префикс «-» отделён от тега', w4.prefix === '-' && w4.word === 'brea' && w4.start === 4,
    JSON.stringify(w4));

  // Нет selectionStart (старый stub) — падаем на длину строки, не падаем.
  const e = makeApp();
  e.els.searchInput = makeInput('breast');
  e.els.searchInput.selectionStart = undefined;
  const w5 = e.suggestWord(e.els.searchInput);
  check('без selectionStart работает конец строки', w5.word === 'breast', JSON.stringify(w5));
}

// ── 2. Подстановка не стирает хвост запроса ────────────────────────────────
{
  const a = makeApp();
  a.els.searchInput = makeInput('breast girl dog', 10); // каретка после «girl»
  a.onSearchInput();
  a.applySuggestion('girls');
  check('подстановка в середине сохраняет последующие теги',
    a.els.searchInput.value === 'breast girls dog', JSON.stringify(a.els.searchInput.value));
}
{
  const a = makeApp();
  a.els.searchInput = makeInput('cat -brea', 9);
  a.onSearchInput();
  a.applySuggestion('breasts');
  check('минус-тег сохраняет префикс при подстановке',
    a.els.searchInput.value === 'cat -breasts ', JSON.stringify(a.els.searchInput.value));
}
{
  const a = makeApp();
  a.els.searchInput = makeInput('breast', 6);
  a.onSearchInput();
  a.applySuggestion('breasts');
  check('подстановка в конце добавляет пробел',
    a.els.searchInput.value === 'breasts ', JSON.stringify(a.els.searchInput.value));
}

// ── 3. Запрос автодополнения без служебных префиксов + NL ──────────────────
(async () => {
  {
    const calls = [];
    API.get = function (url) { calls.push(url); return Promise.resolve({ tags: [] }); };
    const a = makeApp();
    a.els.searchInput = makeInput('cat -brea', 9);
    a.onSearchInput();
    await new Promise(r => setTimeout(r, 260)); // debounce 200мс
    const sugg = calls.filter(u => u.startsWith('/suggest?'));
    // Запрос уходит без минуса; важно отсутствие «q=-brea».
    check('в /suggest уходит тег без минуса',
      sugg.includes('/suggest?q=brea') && !sugg.some(u => u.includes('q=-')),
      JSON.stringify(sugg));
  }
  {
    const nlCalls = [];
    API.get = function (url) { nlCalls.push(url); return Promise.resolve({ tags: [] }); };
    const b = makeApp();
    b.els.searchInput = makeInput('?котики в шляпах', 17);
    b.onSearchInput();
    await new Promise(r => setTimeout(r, 260));
    check('NL-запрос не дёргает автодополнение',
      nlCalls.filter(u => u.startsWith('/suggest')).length === 0, JSON.stringify(nlCalls));
  }

  // ── 4. mergeSuggestions: релевантность и защита от мусора ─────────────────
  {
    const a = makeApp();
    const local = [{ value: 'breasts_large', count: 2 }];
    const remote = [
      { value: 'unrelated_tag', count: 800 },  // префикса нет вовсе — мусор
      { value: 'breasts', count: 5000 },      // точное совпадение
      { value: 'breasts_large', count: 900 }, // дубль локального, счётчик с сайта
    ];
    const merged = a.mergeSuggestions(local, remote, 'breast');
    const vals = merged.map(t => t.value);
    check('тег без вхождения префикса отсечён', !vals.includes('unrelated_tag'), JSON.stringify(vals));
    check('точное совпадение идёт первым', vals[0] === 'breasts', JSON.stringify(vals));
    check('дубль слит, счётчик с сайта',
      merged.length === 2 && merged[1].count === 900, JSON.stringify(merged));
  }
  {
    const a = makeApp();
    const merged = a.mergeSuggestions([], [{ value: 'anything' }], '');
    check('пустой префикс не фильтрует', merged.length === 1, JSON.stringify(merged));
  }
  {
    const a = makeApp();
    const merged = a.mergeSuggestions([{ value: 'Cat' }], [{ value: 'cat' }], 'cat');
    check('дедуп регистронезависимый', merged.length === 1, JSON.stringify(merged));
  }

  // ── 5. Сортировка по популярности ───────────────────────────────────────
  {
    const a = makeApp();
    // Апстрим прислал вразнобой — порядок должен стать по убыванию счётчика.
    const remote = [
      { value: 'cat_ears', count: 50 },
      { value: 'cat_girl', count: 90000 },
      { value: 'catboy', count: 8000 },
    ];
    const vals = a.mergeSuggestions([], remote, 'cat').map(t => t.value);
    check('подсказки отсортированы по популярности',
      JSON.stringify(vals) === JSON.stringify(['cat_girl', 'catboy', 'cat_ears']),
      JSON.stringify(vals));
  }
  {
    const a = makeApp();
    // Точное совождение остаётся первым, даже если оно самое редкое.
    const remote = [
      { value: 'cat_ears', count: 50 },
      { value: 'cat_girl', count: 90000 },
      { value: 'cat', count: 7 },
    ];
    const vals = a.mergeSuggestions([], remote, 'cat').map(t => t.value);
    check('точное совпадение первым, остальные — по популярности',
      JSON.stringify(vals) === JSON.stringify(['cat', 'cat_girl', 'cat_ears']),
      JSON.stringify(vals));
  }
  {
    const a = makeApp();
    // Локальный счётчик (маленький) не должен «затопить» удалённый (большой):
    // incomparable шкалы, сортируем по удалённому.
    const local = [{ value: 'cat_local_only', count: 3 }];
    const remote = [{ value: 'cat_remote', count: 5000 }];
    const merged = a.mergeSuggestions(local, remote, 'cat');
    check('удалённая популярность приоритетнее локальной',
      merged[0].value === 'cat_remote', JSON.stringify(merged));
  }
  {
    const a = makeApp();
    // У тега, известного обоим источникам, показывается счётчик с сайта.
    const local = [{ value: 'cat_girl', count: 2 }];
    const remote = [{ value: 'cat_girl', count: 4242 }];
    const merged = a.mergeSuggestions(local, remote, 'cat');
    check('показывается счётчик с сайта, а не локальный',
      merged.length === 1 && merged[0].count === 4242, JSON.stringify(merged));
  }
  {
    const a = makeApp();
    // Стабильность: равные счётчики → короче, затем алфавит.
    const remote = [
      { value: 'cat_cccc', count: 10 },
      { value: 'cat_bb', count: 10 },
      { value: 'cat_a', count: 10 },
    ];
    const vals = a.mergeSuggestions([], remote, 'cat').map(t => t.value);
    check('при равных счётчиках — короче, затем алфавит',
      JSON.stringify(vals) === JSON.stringify(['cat_a', 'cat_bb', 'cat_cccc']),
      JSON.stringify(vals));
  }

  // ── 6. Релевантность важнее популярности ────────────────────────────────
  {
    const a = makeApp();
    // solo_breasts начинается с «_breast» — совпадение на границе слова.
    // Раньше строгий HasPrefix его выбрасывал, и по «breast» подсказки были
    // почти пустыми. Теперь он в списке, но ниже префиксных.
    const remote = [
      { value: 'breasts', count: 10 },
      { value: 'solo_breasts', count: 10 },
      { value: 'hugebreasts', count: 10 }, // склейка: вхождение в середине
    ];
    const vals = a.mergeSuggestions([], remote, 'breast').map(t => t.value);
    check('граница слова выше вхождения в середину слова',
      JSON.stringify(vals) === JSON.stringify(['breasts', 'solo_breasts', 'hugebreasts']),
      JSON.stringify(vals));
  }
  {
    const a = makeApp();
    // Популярный, но нерелевантный по позиции тег не должен обгонять точный:
    // solo_breasts (10) идёт ниже breasts (10) — префикс важнее границы.
    const remote = [
      { value: 'solo_breasts', count: 999999 },
      { value: 'breasts', count: 1 },
    ];
    const vals = a.mergeSuggestions([], remote, 'breast').map(t => t.value);
    check('префиксный тег выше границы слова независимо от счётчика',
      vals[0] === 'breasts', JSON.stringify(vals));
  }
  {
    const a = makeApp();
    // Граница слова не теряется при слиянии с локальными: локальный тег без
    // удалённого счётчика не должен вытеснить solo_breasts вниз.
    const local = [{ value: 'breasts_small', count: 1 }];
    const remote = [{ value: 'solo_breasts', count: 500 }];
    const vals = a.mergeSuggestions(local, remote, 'breast').map(t => t.value);
    check('локальный префиксный тег выше удалённой границы слова',
      JSON.stringify(vals) === JSON.stringify(['breasts_small', 'solo_breasts']),
      JSON.stringify(vals));
  }
  {
    const a = makeApp();
    // Без счётчиков порядок не должен падать (NaN-компарация).
    const remote = [{ value: 'cat_b' }, { value: 'cat_a' }];
    const vals = a.mergeSuggestions([], remote, 'cat').map(t => t.value);
    check('теги без счётчиков сортируются корректно',
      JSON.stringify(vals) === JSON.stringify(['cat_a', 'cat_b']), JSON.stringify(vals));
  }

  {
    // Список не должен быть безграничным: на короткий префикс сайт отдаёт
    // десятки тегов, и нужный уезжал за пределы выпадающего окна.
    const a = makeApp();
    const many = Array.from({ length: 92 }, (_, i) => ({ value: 'uma_t' + i, count: 1000 - i }));
    const merged = a.mergeSuggestions([], many, 'uma');
    check('слияние возвращает полный набор (обрезание — при отрисовке)',
      merged.length === 92, 'len=' + merged.length);
  }

  // ── 8. Локальный счётчик не сравнивается с удалённым ────────────────────
  {
    // Реальный случай: gelbooru режет выдачу до 100 записей и не отдаёт
    // umamusume на «uma». Он приходит из локальной базы со счётчиком 4
    // (4 скачанных поста) и вставал рядом с umarutsufuri=364 (364 поста
    // на всём сайте). Шкалы несопоставимы — теги с сайта должны быть выше.
    const a = makeApp();
    const local = [{ value: 'umamusume', count: 4 }];
    const remote = [
      { value: 'umarutsufuri', count: 364 },
      { value: 'umamipesto', count: 55 },
    ];
    const merged = a.mergeSuggestions(local, remote, 'uma');
    const vals = merged.map(t => t.value);
    check('локальный-only тег ниже тегов, известных сайту',
      vals.indexOf('umamusume') > vals.indexOf('umarutsufuri'), JSON.stringify(vals));
    check('локальный счётчик помечен флагом local',
      merged[vals.indexOf('umamusume')].local === true, JSON.stringify(merged));
    check('удалённый счётчик не помечен как local',
      merged[vals.indexOf('umarutsufuri')].local === false, JSON.stringify(merged));
  }
  {
    // Обратный случай: тег есть и там, и там — показываем счётчик с сайта.
    const a = makeApp();
    const merged = a.mergeSuggestions([{ value: 'umamipesto', count: 2 }], [{ value: 'umamipesto', count: 55 }], 'umam');
    check('для общего тега счётчик с сайта и без флага local',
      merged.length === 1 && merged[0].count === 55 && merged[0].local === false,
      JSON.stringify(merged));
  }
  {
    // Приоритет «сайт выше локального» не должен ломать сортировку внутри
    // одной группы: два удалённых тега по-прежнему по убыванию счётчика.
    const a = makeApp();
    const remote = [{ value: 'uma_a', count: 10 }, { value: 'uma_b', count: 900 }];
    const vals = a.mergeSuggestions([{ value: 'uma_local', count: 9999 }], remote, 'uma')
      .map(t => t.value);
    check('внутри группы удалённых — по убыванию счётчика',
      JSON.stringify(vals) === JSON.stringify(['uma_b', 'uma_a', 'uma_local']), JSON.stringify(vals));
  }

  // ── 7. Медленный /suggest не теряет удалённые подсказки ─────────────────
  {
    // Регрессия: таймер ставил settled=true, из-за чего удалённый ответ
    // отбрасывался навсегда. Популярнейший тег не показывался, пока
    // пользователь не вводил его целиком.
    const a = makeApp();
    // Локальная база: umamusume среди скачанных тегов НЕТ.
    const local = [{ value: 'umamusume_pretty_derby', count: 3 },
                   { value: 'umamuse_pretty_derby', count: 2 }];
    // Апстрим отдаёт umamusume ПЕРВЫМ (живой ответ r34).
    const remote = [{ value: 'umamusume', count: 41955 },
                    { value: 'umamusume_pretty_derby', count: 9000 }];
    API.get = function (url) {
      if (url.startsWith('/suggest-local')) return Promise.resolve({ tags: local });
      return new Promise(res => setTimeout(() => res({ tags: remote }), 200)); // дольше 120мс
    };
    a.fetchSuggestions('umamu');
    await new Promise(r => setTimeout(r, 500));
    const last = a.rendered[a.rendered.length - 1] || [];
    check('медленный /Suggest не выбрасывает удалённые подсказки (umamusume)',
      last.includes('umamusume'), JSON.stringify(a.rendered));
    check(' umamusume в финальном списке первый',
      last[0] === 'umamusume', JSON.stringify(last));
  }
  {
    // Быстрый /suggest: одна отрисовка, без двойного переупорядочивания.
    const a = makeApp();
    const local = [{ value: 'breasts', count: 2 }, { value: 'solo_breasts', count: 1 }];
    const remote = [
      { value: 'breasts', count: 90000 },
      { value: 'breasts_large', count: 800 },
      { value: 'solo_breasts', count: 40000 },
    ];
    API.get = function (url) {
      if (url.startsWith('/suggest-local')) return Promise.resolve({ tags: local });
      return new Promise(res => setTimeout(() => res({ tags: remote }), 10)); // быстрее 120мс
    };
    a.fetchSuggestions('breast');
    await new Promise(r => setTimeout(r, 300));
    check('при быстром ответе список рендерится один раз',
      a.rendered.length === 1, 'renders=' + JSON.stringify(a.rendered));
  }
  {
    // Совсем недоступный /suggest: показываем локальное, не блокируя ввод.
    const a = makeApp();
    const local = [{ value: 'breasts', count: 3 }];
    API.get = function (url) {
      if (url.startsWith('/suggest-local')) return Promise.resolve({ tags: local });
      return new Promise(() => { }); // навсегда «висит»
    };
    a.fetchSuggestions('breast');
    await new Promise(r => setTimeout(r, 200)); // > SUGGEST_RENDER_DELAY_MS
    check('при недоступном /suggest показываются локальные подсказки',
      a.rendered.length >= 1 && a.rendered[0].includes('breasts'),
      JSON.stringify(a.rendered));
  }

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();


