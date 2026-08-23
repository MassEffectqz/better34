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
import './settings.js';
import './social.js';
import './auth.js';
import { renderLucideIcons } from './utils.js';

document.addEventListener('DOMContentLoaded', () => {
  renderLucideIcons();
  App.init();
});
