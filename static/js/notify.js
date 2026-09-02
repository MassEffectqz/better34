// notify.js — задача 3: уведомления браузера о завершении загрузок.
// Серверный Web Push (VAPID) не нужен: статус загрузок уже приходит по SSE
// (/api/events), уведомление показывается в момент «партия завершена», когда
// вкладка скрыта (иначе пользователь и так видит прогресс и тосты).
// Ограничение клиентского подхода: при полностью закрытом браузере уведомления
// не приходят — для этого понадобился бы серверный Push с подписками.
import { App } from './state.js';
import { t } from './i18n.js';

// Доступен ли Notification API в этом браузере.
export function notifySupported() {
  return typeof window !== 'undefined' && 'Notification' in window;
}

// Запросить разрешение. Вызывается только из обработчика клика (переключение
// галочки в настройках) — не при старте страницы.
export async function requestNotifyPermission() {
  if (!notifySupported()) return 'denied';
  try {
    if (Notification.permission !== 'default') return Notification.permission;
    return await Notification.requestPermission();
  } catch { return 'denied'; }
}

// Показать уведомление. tag схлопывает дубликаты; клик возвращает фокус.
// Возвращает true, если уведомление реально показано.
export function showNotify(title, body, tag) {
  if (!notifySupported() || Notification.permission !== 'granted') return false;
  try {
    const n = new Notification(title, { body, tag: tag || 'briefly-dl' });
    n.onclick = () => {
      try { window.focus(); n.close(); } catch { /* noop */ }
    };
    return true;
  } catch { return false; }
}

// Привязка чекбокса «уведомлять о завершении загрузок» (вызывается из state.bindEvents).
App.bindDlNotifySetting = function (checkbox) {
  if (!checkbox) return;
  const granted = notifySupported() && Notification.permission === 'granted';
  checkbox.checked = granted && localStorage.getItem('briefly_dl_notify') === '1';
  if (!notifySupported()) checkbox.disabled = true;
  checkbox.addEventListener('change', async () => {
    if (!checkbox.checked) {
      localStorage.setItem('briefly_dl_notify', '0');
      this.showToast('Уведомления о загрузках выключены');
      return;
    }
    const perm = await requestNotifyPermission();
    if (perm !== 'granted') {
      checkbox.checked = false;
      this.showToast('Разрешение на уведомления не выдано', 'error');
      return;
    }
    localStorage.setItem('briefly_dl_notify', '1');
    this.showToast('Уведомления о загрузках включены', 'success');
  });
};

// Момент завершения партии загрузок (вызывается из startDlPoll).
App.notifyDownloadsDone = function (done, failed) {
  if (localStorage.getItem('briefly_dl_notify') !== '1') return false;
  if (!notifySupported() || Notification.permission !== 'granted') return false;
  if (typeof document !== 'undefined' && !document.hidden) return false; // вкладка видна — тостов достаточно
  const body = `${t('viewer.downloaded')}: ${done}` +
    (failed ? ` · ${t('notify.errors')}: ${failed}` : '');
  return showNotify(t('notify.dlDone'), body);
};