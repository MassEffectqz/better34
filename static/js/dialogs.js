import { App } from './state.js';
import { esc } from './utils.js';
import { t } from './i18n.js';
App.confirmDialog = function (opts) {
  return new Promise((resolve) => {
    const el = this.els.confirmModal;
    if (!el) return resolve(false);
    this.els.confirmTitle.textContent = opts.title || 'Подтверждение';
    this.els.confirmMessage.innerHTML = opts.message || '';
    this.els.confirmOk.textContent = opts.okText || 'Подтвердить';
    this.els.confirmOk.classList.toggle('btn-danger', !!opts.danger);
    el.classList.remove('hidden');
    const done = (val) => {
      el.classList.add('hidden');
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
  this.els.helpModal.classList.remove('hidden');
};

App.hideHelp = function () {
  if (this.els.helpModal) this.els.helpModal.classList.add('hidden');
};

App.toggleHelp = function () {
  if (this.els.helpModal && this.els.helpModal.classList.contains('hidden')) this.showHelp();
  else this.hideHelp();
};