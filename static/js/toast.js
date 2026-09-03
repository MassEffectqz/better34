import { App } from './state.js';
App.showToast = function (msg, type) {
  const el = this.els.toast; el.textContent = msg; el.className = 'toast';
  el.style.borderColor = type === 'error' ? 'var(--error)' : type === 'success' ? 'var(--success)' : 'var(--border)';
  clearTimeout(this._toastTimer); this._toastTimer = setTimeout(() => el.classList.add('hidden'), 3000);
};

App.showToastWithUndo = function (msg, onUndo) {
  const el = this.els.toast; el.className = 'toast';
  el.style.borderColor = 'var(--border)';
  // textContent + DOM API: msg приходит из ответов сервера (ошибки и т.п.) —
  // innerHTML здесь был вектором XSS.
  el.textContent = msg;
  const btn = document.createElement('button');
  btn.style.cssText = 'background:var(--accent);color:#fff;border:none;border-radius:4px;padding:4px 10px;margin-left:8px;cursor:pointer;font-size:.8rem';
  btn.textContent = 'Отмена';
  btn.addEventListener('click', () => { onUndo(); el.classList.add('hidden'); clearTimeout(this._toastTimer); });
  el.appendChild(btn);
  clearTimeout(this._toastTimer); this._toastTimer = setTimeout(() => el.classList.add('hidden'), 5000);
};
