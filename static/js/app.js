// app.js — единственная точка входа браузера: собирает модули и стартует.
// Порядок импортов задаёт порядок регистрации методов на App (load-order больше не глобальный).
import { App } from './state.js';
import './toast.js';
import './dialogs.js';
import './search.js';
import './viewer.js';
import './video.js';
import './feed.js';
import './keyboard.js';
import './profile.js';
import './collections.js';
import './settings.js';
import './aliases.js';
import './social.js';
import './auth.js';
import './notify.js';
import './a11y.js';
import { renderLucideIcons } from './utils.js';
import { applyI18n, t, getLang } from './i18n.js';

document.addEventListener('DOMContentLoaded', () => {
  applyI18n();
  document.documentElement.lang = getLang();
  const st = document.getElementById('status-text');
  if (st) st.textContent = t('status.ready');
  // Смена языка из настроек: селектор уже синхронизирован в state.js.
  window.addEventListener('briefly-lang', () => {
    const sel = document.getElementById('setting-lang');
    if (sel) sel.value = getLang();
  });
  renderLucideIcons();
  App.init();
});
