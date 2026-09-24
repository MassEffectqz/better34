import { App } from './state.js';
import { esc } from './utils.js';
import { t } from './i18n.js';

// PB-5: confirmDialog принимал произвольный HTML (innerHTML). Сообщения
// приходят из кода и обычно экранированы, но чтобы диалог оставался безопасным
// при любом будущем вызове, пропускаем их через санитайзер: разрешён только
// узкий whitelist лёгких тегов, всё остальное превращается в текст.
const SAFE_TAGS = new Set(['B', 'BR', 'I', 'EM', 'STRONG', 'CODE', 'SPAN', 'SMALL', 'MARK']);
const safeRich = (s) => {
  const haystack = String(s || '');
  if (haystack === '') return '';
  const doc = new DOMParser().parseFromString(haystack, 'text/html');
  const clean = (node) => {
    for (const child of [...node.children]) {
      if (SAFE_TAGS.has(child.tagName)) {
        // Разрешённому тегу обрезаем атрибуты (никаких onclick/href и т.п.).
        for (const k of [...child.attributes]) child.removeAttribute(k.name);
        clean(child);
      } else {
        const span = document.createElement('span');
        span.textContent = child.textContent;
        node.replaceChild(span, child);
      }
    }
  };
  clean(doc.body);
  return doc.body.innerHTML;
};

App.confirmDialog = function (opts) {
  return new Promise((resolve) => {
    const prev = document.activeElement;
    const el = this.els.confirmModal;
    if (!el) return resolve(false);
    el.setAttribute('role', 'dialog');
    el.setAttribute('aria-modal', 'true');
    el.setAttribute('aria-labelledby', 'confirm-title');
    this.els.confirmTitle.id = 'confirm-title';
    this.els.confirmTitle.textContent = opts.title || 'Подтверждение';
    this.els.confirmMessage.innerHTML = safeRich(opts.message || '');
    this.els.confirmOk.textContent = opts.okText || 'Подтвердить';
    this.els.confirmOk.classList.toggle('btn-danger', !!opts.danger);
    el.classList.remove('hidden');
    if (typeof this.setAriaHidden === 'function') this.setAriaHidden(document.getElementById('main'), true);
    const releaseTrap = typeof this.trapFocus === 'function'
      ? this.trapFocus(el, this.els.confirmOk) : () => {};
    const done = (val) => {
      releaseTrap();
      el.classList.add('hidden');
      el.removeAttribute('aria-hidden');
      el.removeAttribute('role');
      el.removeAttribute('aria-modal');
      el.removeAttribute('aria-labelledby');
      const main = document.getElementById('main');
      if (main && typeof this.setAriaHidden === 'function') this.setAriaHidden(main, false);
      if (prev && prev.focus) prev.focus();
      this.els.confirmOk.removeEventListener('click', onOk);
      this.els.confirmCancel.removeEventListener('click', onCancel);
      this.els.confirmClose.removeEventListener('click', onCancel);
      this.els.confirmBackdrop.removeEventListener('click', onBackdrop);
      document.removeEventListener('keydown', onKey);
      resolve(val);
    };
    const onOk = () => done(true);
    const onCancel = () => done(false);
    const onBackdrop = () => done(false);
    const onKey = (e) => {
      if (e.key === 'Escape') done(false);
      else if (e.key === 'Enter') done(true);
    };
    this.els.confirmOk.addEventListener('click', onOk);
    this.els.confirmCancel.addEventListener('click', onCancel);
    this.els.confirmClose.addEventListener('click', onCancel);
    this.els.confirmBackdrop.addEventListener('click', onBackdrop);
    document.addEventListener('keydown', onKey);
    this.els.confirmOk.focus();
  });
};

App.showHelp = function () {
  const groups = [
    { title: t('help.feed'), rows: [
      [['W', 'A', 'S', 'D', '←', '↑', '↓', '→'], 'Навигация по сетке'],
      [['Enter'], 'Открыть пост'],
      [['PageUp', 'PageDown'], 'Прокрутка по страницам'],
      [['Home', 'End'], 'Начало / конец ленты'],
      [['X'], 'Скачать пост'],
      [['Q'], 'Лайк'],
      [['E'], 'Скрыть'],
      [['/', 'Ctrl K'], 'Фокус поиска'],
      [['L'], 'Локальные посты'],
      [['Ctrl', 'ЛКМ'], 'Открыть пост в новой вкладке'],
    ] },
    { title: t('help.view'), rows: [
      [['←', '→'], 'Предыдущий / следующий пост'],
      [['←', '→'], 'Перемотка видео ±5 с'],
      [['Shift', '←', '→'], 'Листать посты при видео'],
      [['↑', '↓'], 'Громкость видео'],
      [['M'], 'Звук вкл/выкл'],
      [['Пробел'], 'Слайдшоу / пауза видео'],
      [['F'], 'Полный экран'],
      [['Z'], 'Зум'],
      [['R'], 'Инверсия'],
      [['X'], 'Скачать'],
      [['Q', 'E'], 'Лайк / Скрыть'],
      [['Ctrl', 'ЛКМ'], 'Открыть файл в новой вкладке'],
      [['ESC'], 'Закрыть'],
    ] },
    { title: t('help.zoom'), rows: [
      [['+', '−'], 'Приблизить / отдалить'],
      [['W', 'A', 'S', 'D', '←', '↑', '↓', '→'], 'Панорама'],
      [['PageUp', 'PageDown'], 'Листать по страницам'],
      [['Home', 'End'], 'Край изображения'],
    ] },
    { title: t('help.ui'), rows: [
      [['ESC'], 'Закрыть панели и меню'],
      [['?'], 'Эта справка'],
    ] },
  ];
  const body = this.els.helpBody;
  if (!body) return;
  body.innerHTML = groups.map(g =>
    `<div class="help-group"><h3>${esc(g.title)}</h3>` +
    g.rows.map(r =>
      `<div class="help-row"><span>${esc(r[1])}</span><span class="help-keys">${r[0].map(k => `<span class="kbd">${esc(k)}</span>`).join('')}</span></div>`
    ).join('') +
    `</div>`
  ).join('');
  this.els.helpModal.setAttribute('role', 'dialog');
  this.els.helpModal.setAttribute('aria-modal', 'true');
  this.els.helpModal.classList.remove('hidden');
  if (typeof this.trapFocus === 'function') {
    this._releaseHelpTrap = this.trapFocus(this.els.helpModal, this.els.helpModal);
  }
};

App.hideHelp = function () {
  if (this._releaseHelpTrap) { this._releaseHelpTrap(); this._releaseHelpTrap = null; }
  if (this.els.helpModal) {
    this.els.helpModal.removeAttribute('role');
    this.els.helpModal.removeAttribute('aria-modal');
    this.els.helpModal.classList.add('hidden');
  }
};

App.toggleHelp = function () {
  if (this.els.helpModal && this.els.helpModal.classList.contains('hidden')) this.showHelp();
  else this.hideHelp();
};