// notify.test.js — задача 3: Web Notifications о завершении загрузок.
import { App } from '../state.js';
import '../notify.js';
import { notifySupported, requestNotifyPermission, showNotify } from '../notify.js';
import { t } from '../i18n.js';

let passed = 0, failed = 0;
function check(name, cond, detail) {
  if (cond) { passed++; console.log('  ok  - ' + name); }
  else { failed++; console.log(' FAIL - ' + name + (detail ? ' | ' + detail : '')); }
}

console.log('notify: permission-флоу, чекбокс, notifyDownloadsDone\n');

// Node 24: window нет, localStorage/navigator — getter-only глобалы.
const defG = (name, value) => Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
defG('window', globalThis);

const perms = { requestResult: 'granted', requests: 0 };
defG('Notification', class {
  static permission = 'default';
  static _last = null;
  static requestPermission = async () => {
    perms.requests++;
    Notification.permission = perms.requestResult;
    return perms.requestResult;
  };
  constructor(title, opts) {
    this.title = title; this.body = opts && opts.body; this.tag = opts && opts.tag;
    this.onclick = null; this.closed = false;
    Notification._last = this;
  }
  close() { this.closed = true; }
});

(async () => {
  check('notifySupported: API есть', notifySupported() === true);

  // ── 1. permission-флоу ──
  check('дефолт: разрешение ещё не запрошено', Notification.permission === 'default');
  check('requestNotifyPermission запрашивает и возвращает granted',
    (await requestNotifyPermission()) === 'granted' && perms.requests === 1);
  check('повторный вызов не спрашивает заново', (await requestNotifyPermission()) === 'granted' && perms.requests === 1,
    'requests=' + perms.requests);

  // ── 2. показ уведомления ──
  check('showNotify при granted показывает и ставит tag',
    showNotify('T', 'B') === true && Notification._last.tag === 'briefly-dl');
  let focused = false;
  defG('focus', () => { focused = true; });
  Notification._last.onclick();
  check('клик по уведомлению закрывает его', Notification._last.closed === true && focused);

  // ── 3. notifyDownloadsDone: гейт локальной настройки ──
  defG('localStorage', {
    _s: {},
    getItem(k) { return this._s[k] != null ? this._s[k] : null; },
    setItem(k, v) { this._s[k] = String(v); },
    removeItem(k) { delete this._s[k]; },
  });
  defG('document', { hidden: false });
  App.showToast = function () { /* тихо в тесте */ };
  App.notifyDownloadsDone(3, 0);
  check('настройка выключена → нет уведомления', Notification._last.title !== 'Загрузки завершены');

  localStorage.setItem('briefly_dl_notify', '1');
  check('включено + вкладка видима → нет уведомления (тост уже есть)',
    App.notifyDownloadsDone(3, 0) === false);
  document.hidden = true;
  check('включено + вкладка скрыта → уведомление показано',
    App.notifyDownloadsDone(3, 0) === true && Notification._last.title === 'Загрузки завершены');
  check('тело: скачано: 3', Notification._last.body.indexOf('3') >= 0, Notification._last.body);
  check('с ошибками: тело содержит счётчик',
    App.notifyDownloadsDone(3, 2) && Notification._last.body.indexOf('2') >= 0, Notification._last.body);
  check('русские строки берутся из i18n', t('notify.dlDone') === 'Загрузки завершены');

  console.log('\n' + passed + ' passed, ' + failed + ' failed');
  if (failed) throw new Error(`${failed} checks failed`);
})();