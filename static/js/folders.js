import { App } from './state.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';
import { esc, icon } from './utils.js';

/**
 * Отрисовка полосы быстрых умных папок.
 * @this {any}
 */
App.renderSmartFoldersBar = function () {
  const bar = this.els.smartFoldersBar;
  if (!bar) return;

  const presets = (this.state.profile && this.state.profile.presets) || [];
  const folders = presets.filter(p => p && (p.kind || 'query') === 'query' && String(p.query || '').trim());

  if (!folders.length) {
    bar.classList.add('hidden');
    bar.innerHTML = '';
    return;
  }

  bar.classList.remove('hidden');
  const curQ = (this.state.query || '').trim();

  let html = `<div class="smart-folders-header" title="${esc(t('smartFolder.title'))}">` +
    `<span class="smart-folders-icon">${icon('folder', 13)}</span>` +
    `<span class="smart-folders-label">${esc(t('smartFolder.title'))}</span>` +
    `</div>` +
    `<div class="smart-folders-list">`;
  for (const f of folders) {
    const fQ = String(f.query || '').trim();
    const isActive = curQ === fQ;
    html += `<button type="button" class="folder-chip${isActive ? ' active' : ''}" data-id="${esc(f.id)}" data-query="${esc(fQ)}" title="${esc(fQ)}">` +
      `<span class="folder-chip-icon">${icon('folder', 13)}</span>` +
      `<span class="folder-chip-name">${esc(f.name)}</span>` +
      `<span class="folder-chip-del" data-action="delete" title="${esc(t('btn.delete'))}">${icon('x', 11)}</span>` +
    '</button>';
  }
  html += '</div>';
  bar.innerHTML = html;
};

/**
 * Сохранить текущий поисковый запрос как умную папку.
 * @this {any}
 */
App.saveCurrentSearchAsFolder = async function () {
  const q = (this.state.query || (this.els.searchInput && this.els.searchInput.value) || '').trim();
  if (!q) {
    this.showToast(t('preset.needSearch'), 'info');
    return;
  }
  const defaultName = (q.length > 25 ? q.slice(0, 24) + '…' : q) || t('smartFolder.new');
  const name = typeof this.promptDialog === 'function'
    ? await this.promptDialog({ title: t('smartFolder.namePrompt'), defaultValue: defaultName })
    : (window.prompt(t('smartFolder.namePrompt'), defaultName) || '').trim();

  if (!name) return;

  try {
    await API.post('/preset', { name, query: q, kind: 'query' });
    API.invalidate('/profile');
    await this.loadProfile();
    this.showToast(tf('smartFolder.saved', { name }), 'success');
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
};

/**
 * Инициализация слушателей для умных папок.
 * @this {any}
 */
App.initSmartFolders = function () {
  const bar = this.els.smartFoldersBar;
  if (bar) {
    bar.addEventListener('click', async (ev) => {
      const target = /** @type {HTMLElement} */ (ev.target);
      const chip = /** @type {HTMLElement | null} */ (target.closest('.folder-chip'));
      if (!chip) return;

      const id = chip.dataset.id;
      const q = chip.dataset.query;

      if (target.closest('[data-action="delete"]')) {
        ev.stopPropagation();
        const pr = ((this.state.profile && this.state.profile.presets) || []).find(p => p.id === id);
        const name = pr ? pr.name : '';
        const ok = await this.confirmDialog({
          title: tf('smartFolder.delete', { name }),
          message: q || '',
          okText: t('btn.delete'),
          danger: true,
        });
        if (!ok) return;
        try {
          await API.del(`/preset/${id}`);
          API.invalidate('/profile');
          await this.loadProfile();
        } catch (err) {
          this.showToast(`Ошибка: ${err.message}`, 'error');
        }
        return;
      }

      if (q != null) {
        this.setSearchValue(q);
        this.search(q);
      }
    });
  }

  const btnSave = this.els.btnSaveFolder;
  if (btnSave) {
    btnSave.addEventListener('click', () => this.saveCurrentSearchAsFolder());
  }

  const colorPopover = document.getElementById('color-popover');
  if (colorPopover) {
    colorPopover.addEventListener('click', (ev) => {
      const btn = /** @type {HTMLElement | null} */ ((/** @type {HTMLElement} */ (ev.target)).closest('.color-btn'));
      if (!btn) return;
      const color = btn.dataset.color || '';
      this.setColorFilter(color);
    });
  }
};
