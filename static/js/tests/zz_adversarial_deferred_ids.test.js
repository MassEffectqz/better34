// zz_adversarial_deferred_ids.test.js — обход `deferred` при выборке по id.
//
// РЕГРЕССИЯ (2026-09): сервер спрашивает источник не больше пачки id за ответ,
// а остальные отдаёт в `deferred` — сайты, не понимающие списки id
// (gelbooru, safebooru), отвечают на каждый пост отдельным запросом, и пачка
// в сотню id не укладывается в дедлайн телефона. Если бы клиент брал только
// первый ответ, вкладка «Лайки» показывала бы первые сорок постов и молча
// выкинула остальные — пользователь считал бы, что «посты пропали».
import { App } from '../state.js';
import { API } from '../api.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

globalThis.window = {};
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.document = { querySelector: () => null, addEventListener() {} };

console.log('Обход deferred при выборке по id\n');

(async () => {
  // Сервер отдаёт по 2 поста за ответ, остальное — в deferred.
  {
    const seen = [];
    API.get = async (url) => {
      const ids = (url.split('ids=')[1] || '').split(',').map(Number);
      seen.push(ids);
      return {
        posts: ids.slice(0, 2).map(id => ({ id, tags: 'a' })),
        unresolved: [],
        deferred: ids.slice(2),
      };
    };
    const r = await App._fetchPostsByIds([1, 2, 3, 4, 5, 6, 7]);
    check('обошёл все id, а не только первые два', r.posts.length === 7,
      'постов: ' + r.posts.length);
    check('id не потеряны и не задвоены',
      JSON.stringify(r.posts.map(p => p.id)) === '[1,2,3,4,5,6,7]',
      JSON.stringify(r.posts.map(p => p.id)));
    // Каждый следующий запрос — только остаток: первый несёт весь список,
    // дальше запросы строго убывают (иначе мы бы повторно спрашивали id,
    // за которые уже получили ответ).
    check('следующий запрос берёт только остаток, а не весь список',
      seen.length === 4
      && seen[0].length === 7 && seen[1].length === 5 && seen[2].length === 3 && seen[3].length === 1,
      'запросов: ' + seen.length + ' ' + JSON.stringify(seen));
  }

  // deferred и unresolved не должны смешиваться: unresolved — «источник не
  // ответил» (плитка с кнопкой повтора), deferred — «ещё не спросили».
  // unresolved возвращается один раз: сервер такие id больше не отдаёт в
  // deferred, поэтому клиент их и не переспрашивает.
  {
    let first = true;
    API.get = async (url) => {
      const ids = (url.split('ids=')[1] || '').split(',').map(Number);
      const unres = first ? [ids[1]] : [];
      first = false;
      return {
        posts: ids.filter(id => id !== unres[0]).slice(0, 1).map(id => ({ id })),
        unresolved: unres,
        deferred: ids.filter(id => id !== unres[0]).slice(1),
      };
    };
    const r = await App._fetchPostsByIds([10, 11, 12, 13, 14]);
    check('unresolved собран и не потерян', JSON.stringify(r.unresolved) === '[11]',
      JSON.stringify(r.unresolved));
    check('все посты в итоге получены', r.posts.length === 4, 'постов: ' + r.posts.length);
  }

  // Упорный сервер, возвращающий те же deferred: без предела заходов клиент
  // крутил бы запросы вхолостую — тот самый «вечный спиннер».
  {
    let calls = 0;
    API.get = async () => {
      calls++;
      return { posts: [], unresolved: [], deferred: [900, 901] };
    };
    const r = await App._fetchPostsByIds([900, 901], { maxPasses: 4 });
    check('бесконечный deferred не крутит запросы вечно', calls <= 4, 'запросов: ' + calls);
    check('без постов вернулся пустой результат, а не исключение', r.posts.length === 0);
  }

  // Сеть упала на первом запросе и постов ещё нет — ошибка доходит до
  // вызывающего (вьювер скажет «пост недоступен»), а не глотается молча.
  {
    API.get = async () => { throw new Error('offline'); };
    let err = null;
    try { await App._fetchPostsByIds([42]); } catch (e) { err = e; }
    check('ошибка сети без постов доходит до вызывающего', !!err, String(err));
  }

  // Сеть упала, но часть постов уже пришла: отдаём их, не заставляя вьювер
  // открывать пустую ленту.
  {
    let n = 0;
    API.get = async () => {
      if (n++ === 0) return { posts: [{ id: 1 }], unresolved: [], deferred: [2] };
      throw new Error('offline');
    };
    let r = null, err = null;
    try { r = await App._fetchPostsByIds([1, 2]); } catch (e) { err = e; }
    check('частичный результат не выбрасывается', !err && !!r && r.posts.length === 1,
      'err=' + err + ' posts=' + (r && r.posts.length));
  }

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();
