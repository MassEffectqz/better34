// Регистрация service worker вынесена из index.html: CSP запрещает
// inline-скрипты (script-src 'self').
//
// Обновление SW (задача «тост с перезагрузкой»): когда новый воркер уже
// установился, а страницей всё ещё управляет старый — показываем тост
// «Доступно обновление» с кнопкой «Перезагрузить». Авто-reload не делаем:
// не прерываем чтение/листание. Проверки: каждые 60 минут и при возврате
// на вкладку (visibilitychange) — sw.js сам skipWaiting(), поэтому после
// перезагрузки пользователь гарантированно получает новую сборку.
// Скрипт классический (не module) — тост строим через DOM без import'ов;
// для вёрстки переиспользуем CSS-класс .toast из toast.js.
(function () {
  if (!('serviceWorker' in navigator)) return;
  let updateToastShown = false;

  function showUpdateToast() {
    if (updateToastShown) return;
    updateToastShown = true;
    const el = document.createElement('div');
    el.className = 'toast';
    el.style.transition = 'opacity .25s';
    el.style.display = 'flex';
    el.style.alignItems = 'center';
    el.textContent = 'Доступно обновление приложения';
    const btn = document.createElement('button');
    btn.style.cssText = 'background:var(--accent);color:#fff;border:none;border-radius:4px;padding:4px 10px;margin-left:8px;cursor:pointer;font-size:.8rem';
    btn.textContent = 'Перезагрузить';
    btn.addEventListener('click', () => { location.reload(); });
    el.appendChild(btn);
    const closeBtn = document.createElement('button');
    closeBtn.textContent = '\u00d7';
    closeBtn.style.cssText = 'background:none;border:none;color:var(--text-secondary);font-size:1.2rem;cursor:pointer;margin-left:auto;padding:0 4px;line-height:1';
    closeBtn.setAttribute('aria-label', 'Dismiss');
    closeBtn.addEventListener('click', () => { el.remove(); });
    el.appendChild(closeBtn);
    document.body.appendChild(el);
    // Самоустраняемся, чтобы не висеть вечно, если проигнорировано.
    setTimeout(() => { el.remove(); }, 15000);
  }

  function watchInstalling(reg) {
    const sw = reg.installing;
    if (!sw) return;
    sw.addEventListener('statechange', () => {
      // 'installed' при живом controller = это обновление, а не первая установка.
      if (sw.state === 'installed' && navigator.serviceWorker.controller) showUpdateToast();
    });
  }

  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js?v=7').then(function (reg) {
      reg.addEventListener('updatefound', function () { watchInstalling(reg); });
      watchInstalling(reg); // обновление могло начаться до нашего listener'а
      setInterval(function () { reg.update().catch(function () {}); }, 60 * 60 * 1000);
      document.addEventListener('visibilitychange', function () {
        if (!document.hidden) reg.update().catch(function () {});
      });
    }).catch(function () {});
  });
})();
