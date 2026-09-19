import { App } from './state.js';

const MAX_VISIBLE = 3;
const TOAST_HEIGHT = 60;
const BASE_BOTTOM = 60;
const DURATION = 3000;
const UNDO_DURATION = 5000;

const activeToasts = [];

function positionToasts() {
  activeToasts.forEach((t, i) => {
    t.style.bottom = `${BASE_BOTTOM + i * TOAST_HEIGHT}px`;
  });
}

function removeToast(el, timer) {
  clearTimeout(timer);
  el.classList.add('hidden');
  setTimeout(() => {
    const idx = activeToasts.indexOf(el);
    if (idx !== -1) activeToasts.splice(idx, 1);
    el.remove();
    positionToasts();
  }, 250);
}

function createToast(msg, opts = {}) {
  if (activeToasts.length >= MAX_VISIBLE) {
    removeToast(activeToasts[0], activeToasts[0]._timer);
  }

  const el = document.createElement('div');
  el.className = 'toast';
  el.style.transition = 'opacity .25s, bottom .25s';
  el.style.display = 'flex';
  el.style.alignItems = 'center';
  el.textContent = msg;

  if (opts.borderColor) el.style.borderColor = opts.borderColor;

  if (opts.button) {
    const btn = document.createElement('button');
    btn.style.cssText = 'background:var(--accent);color:#fff;border:none;border-radius:4px;padding:4px 10px;margin-left:8px;cursor:pointer;font-size:.8rem';
    btn.textContent = opts.button.text;
    btn.addEventListener('click', () => {
      opts.button.onClick();
      removeToast(el, timer);
    });
    el.appendChild(btn);
  }

  const closeBtn = document.createElement('button');
  closeBtn.textContent = '\u00d7';
  closeBtn.style.cssText = 'background:none;border:none;color:var(--text-secondary);font-size:1.2rem;cursor:pointer;margin-left:auto;padding:0 4px;line-height:1';
  closeBtn.setAttribute('aria-label', 'Dismiss');
  closeBtn.addEventListener('click', () => removeToast(el, timer));
  el.appendChild(closeBtn);

  document.body.appendChild(el);
  activeToasts.push(el);
  positionToasts();

  const duration = opts.duration || DURATION;
  const timer = setTimeout(() => removeToast(el, timer), duration);
  el._timer = timer;
}

App.showToast = function (msg, type) {
  const borderColor = type === 'error' ? 'var(--error)' : type === 'success' ? 'var(--success)' : 'var(--border)';
  createToast(msg, { borderColor, duration: DURATION });
};

App.showToastWithUndo = function (msg, onUndo) {
  createToast(msg, {
    borderColor: 'var(--border)',
    duration: UNDO_DURATION,
    button: { text: 'Отмена', onClick: onUndo },
  });
};
