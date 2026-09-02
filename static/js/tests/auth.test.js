// auth.test.js — ветвления initAuth (authed / register / login), метр пароля,
// совпадение паролей, валидация authSubmit.
import '../auth.js';
import { App } from '../state.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

// ── DOM-стаб ────────────────────────────────────────────────────────────────
// .auth-chips нет → loadAuthChips выходит сразу.
const els = {};
function el(id) {
  if (!els[id]) els[id] = {
    id,
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    style: {}, dataset: {}, value: '', disabled: true,
    addEventListener() {}, blur() {}, focus() {}, offsetWidth: 0,
  };
  return els[id];
}
const authMe = { json: null };
globalThis.document = {
  getElementById: el,
  querySelector() { return null; },
  querySelectorAll() { return []; },
  addEventListener() {},
  activeElement: null,
};
globalThis.window = { setTimeout };
globalThis.fetch = async () => {
  if (authMe.json === null) throw new Error('network down');
  return {
    ok: true, status: 200,
    headers: { get: () => 'application/json' },
    json: async () => authMe.json,
  };
};

console.log('auth: initAuth-ветвления, метр пароля, совпадение паролей, authSubmit\n');

(async () => {
  // ── 1. /auth/me недоступен → регистрация (первый запуск) ──
  await App.initAuth();
  check('нет сети: режим register', App._authMode === 'register', 'mode=' + App._authMode);
  check('нет сети: _usersExist=false', App._usersExist === false);
  check('authReady резолвится после showAuth (register)',
    await Promise.race([App.authReady.then(() => true),
      new Promise(r => setTimeout(() => r(false), 100))]) === true);

  // ── 2. /auth/me: юзеры есть, не залогинены → логин ──
  authMe.json = { authed: false, users_exist: true };
  await App.initAuth();
  check('юзеры есть, не залогинены: режим login', App._authMode === 'login', 'mode=' + App._authMode);
  check('_usersExist=true', App._usersExist === true);

  // ── 3. /auth/me: уже залогинены → hideAuth, state.user ──
  authMe.json = { authed: true, user: { id: 1, username: 'u' } };
  await App.initAuth();
  check('залогинен: state.user проставлен',
    !!(App.state.user && App.state.user.username === 'u'), JSON.stringify(App.state.user));
  check('залогинен: поля формы дизейблены (hideAuth)',
    el('auth-username').disabled === true && el('auth-password').disabled === true);
  check('залогинен: authReady резолвится сразу',
    await Promise.race([App.authReady.then(() => true),
      new Promise(r => setTimeout(() => r(false), 100))]) === true);

  // ── 4. Метр пароля ──
  el('auth-password').value = 'abc';
  App.updateAuthMeter();
  check('метр: короткий пароль → level 1', el('auth-meter').dataset.level === '1',
    'level=' + el('auth-meter').dataset.level);
  el('auth-password').value = 'abcdefgh';
  App.updateAuthMeter();
  check('метр: 8 символов → level 2', el('auth-meter').dataset.level === '2');
  el('auth-password').value = 'Abcdefghij';
  App.updateAuthMeter();
  check('метр: 10 симв., 2 класса символов → level 4', el('auth-meter').dataset.level === '4');
  el('auth-password').value = 'abcdefghij';
  App.updateAuthMeter();
  check('метр: 10 симв., 1 класс → level 3', el('auth-meter').dataset.level === '3');
  el('auth-password').value = '';
  App.updateAuthMeter();
  check('метр: пусто → level 0', el('auth-meter').dataset.level === '0');

  // ── 5. Совпадение паролей ──
  el('auth-password').value = 'secret1';
  el('auth-password2').value = 'secret1';
  App.updateAuthMatch();
  check('совпадение: hint ok', el('auth-hint').className === 'auth-hint ok', el('auth-hint').className);
  el('auth-password2').value = 'secret2';
  App.updateAuthMatch();
  check('не совпадают: hint bad', el('auth-hint').className === 'auth-hint bad');
  el('auth-password2').value = '';
  App.updateAuthMatch();
  check('пустое подтверждение: hint сброшен', el('auth-hint').className === 'auth-hint');

  // ── 6. authSubmit: валидация без сети ──
  App._authMode = 'login';
  el('auth-username').value = '';
  el('auth-password').value = '';
  await App.authSubmit();
  check('authSubmit: пустая форма — ошибка валидации', el('auth-error').textContent !== '');

  App._authMode = 'register';
  el('auth-username').value = 'U';
  el('auth-password').value = 'short';
  el('auth-password2').value = 'short';
  await App.authSubmit();
  check('authSubmit: короткий пароль отклонён',
    el('auth-error').textContent.indexOf('6') >= 0, el('auth-error').textContent);

  // Логин с сетью: 200 → state.user, форма скрыта, поля дизейбл.
  App._authMode = 'login';
  globalThis.fetch = async (url) => {
    if (url === '/api/auth/login') {
      return {
        ok: true, status: 200,
        headers: { get: () => 'application/json' },
        json: async () => ({ ok: true, user: { id: 2, username: 'u' } }),
      };
    }
    throw new Error('network down');
  };
  App.afterLogin = async () => {};
  el('auth-username').value = 'U';
  el('auth-password').value = 'secret1';
  await App.authSubmit();
  check('authSubmit: успешный логин проставляет user',
    !!(App.state.user && App.state.user.id === 2), JSON.stringify(App.state.user));
  check('authSubmit: ошибка очищена', el('auth-error').textContent === '');
  check('authSubmit: поля задизейблены после логина', el('auth-password').disabled === true);

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();
