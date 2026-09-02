// a11y.js — задача 4: доступность (focus trap, aria-live, role="dialog").
// a11y.js — листовый модуль (как auth.js): навешивает методы на App, чтобы
// не создавать цикл импортов с state.js. state.js импортирует его в bindUI.
import { App } from './state.js';

// ── aria-live для toast (скринридеры читают статусы без фокусной смены) ──
App.announce = function (text) {
  let el = this.els.toast && this.els.toast.liveEl;
  if (!el) {
    const t = this.els.toast;
    if (!t) return;
    el = t.querySelector('.a11y-live');
    if (!el) {
      el = document.createElement('span');
      el.className = 'a11y-live';
      el.setAttribute('aria-live', 'polite');
      el.setAttribute('aria-atomic', 'true');
      el.style.position = 'absolute';
      el.style.left = '-9999px';
      t.appendChild(el);
    }
    this.els.toast.liveEl = el;
  }
  el.textContent = text || '';
};

// ── focus trap: Tab ↔ Shift+Tab кольцом по элементам root ─────────────────
//  firstEl — куда возвращать фокус при закрытии (иначе ставим на root).
//  Хукнуто как метод App, чтобы тестировать отдельно.
App.trapFocus = function (root, firstEl) {
  if (!root) return () => {};
  firstEl = firstEl || root;
  const handle = (e) => {
    if (e.key !== 'Tab') return;
    const focusables = root.querySelectorAll(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    const nodes = Array.from(focusables).filter(n => n.offsetParent !== null || n === document.activeElement);
    if (!nodes.length) { if (e.shiftKey) e.preventDefault(); (firstEl || root).focus?.(); return; }
    const first = nodes[0];
    const last = nodes[nodes.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault(); last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault(); first.focus();
    }
  };
  root.addEventListener('keydown', handle);
  return () => root.removeEventListener('keydown', handle);
};

// ── aria-скрытие/показ фона при открытой модальной подсистеме ─────────────
//  Используется для main / #posts-grid, чтобы скринридеры не читали фон.
App.setAriaHidden = function (el, hidden) {
  if (!el) return;
  if (hidden) {
    el.setAttribute('aria-hidden', 'true');
    el.classList.add('a11y-hidden');
  } else {
    el.setAttribute('aria-hidden', 'false');
    el.classList.remove('a11y-hidden');
  }
};