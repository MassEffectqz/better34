// Регистрация service worker вынесена из index.html: CSP запрещает
// inline-скрипты (script-src 'self').
if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/sw.js?v=7').catch(function () {});
  });
}
