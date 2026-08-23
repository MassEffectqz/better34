import { App } from './state.js';
App.showToast = function (msg, type) {
  const el = this.els.toast; el.textContent = msg; el.className = 'toast';
  el.style.borderColor = type === 'error' ? 'var(--error)' : type === 'success' ? 'var(--success)' : 'var(--border)';
  clearTimeout(this._toastTimer); this._toastTimer = setTimeout(() => el.classList.add('hidden'), 3000);
};

App.showToastWithUndo = function (msg, onUndo) {
  const el = this.els.toast; el.className = 'toast';
  el.style.borderColor = 'var(--border)';
  el.innerHTML = `${msg} <button style="background:var(--accent);color:#fff;border:none;border-radius:4px;padding:4px 10px;margin-left:8px;cursor:pointer;font-size:.8rem">Отмена</button>`;
  el.querySelector('button').addEventListener('click', () => { onUndo(); el.classList.add('hidden'); clearTimeout(this._toastTimer); });
  clearTimeout(this._toastTimer); this._toastTimer = setTimeout(() => el.classList.add('hidden'), 5000);
};
