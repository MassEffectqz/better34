// aliases.js — управление алиасами тегов (danbooru-style синонимы) в настройках.
// Список хранится на сервере (tag_aliases) и применяется при построении
// поисковых запросов: «catgirl» автоматически становится «neko» у провайдера.
import { App } from './state.js';
import { _, icon, esc } from './utils.js';
import { API } from './api.js';
import { t } from './i18n.js';

App.loadAliases = async function (force) {
  if (!force && this._aliasesCache && this._aliasesCacheTs && Date.now() - this._aliasesCacheTs < 15000) {
    this.renderAliases(this._aliasesCache);
    return;
  }
  try {
    const d = await API.get('/tag-aliases', { fresh: true });
    this._aliasesCache = d.aliases || [];
    this._aliasesCacheTs = Date.now();
    this.renderAliases(this._aliasesCache);
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
};

App.renderAliases = function (aliases) {
  const list = _('tag-aliases-list');
  if (!list) return;
  list.innerHTML = '';
  if (!aliases || !aliases.length) {
    list.innerHTML = `<p class="profile-empty">${esc(t('alias.empty'))}</p>`;
    return;
  }
  aliases.forEach(a => {
    const row = document.createElement('div');
    row.className = 'alias-item';
    row.innerHTML = `
      <span class="alias-src">${esc(a.alias)}</span>
      <span class="alias-arrow">→</span>
      <span class="alias-dst">${esc(a.target)}</span>
      <button type="button" class="btn-icon btn-icon-sm" title="Удалить">${icon('x', 13)}</button>`;
    row.querySelector('button').addEventListener('click', () => {
      API.del(`/tag-alias/${encodeURIComponent(a.alias)}`).then(() => {
        this._aliasesCache = (this._aliasesCache || []).filter(x => x.alias !== a.alias);
        this.renderAliases(this._aliasesCache);
        this.showToast(`${a.alias} → удалён`);
        API.invalidate('/tag-aliases');
      }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
    });
    list.appendChild(row);
  });
};

App.addAlias = async function () {
  const aliasInput = _('alias-input');
  const targetInput = _('alias-target');
  if (!aliasInput || !targetInput) return;
  const alias = aliasInput.value.trim().toLowerCase();
  const target = targetInput.value.trim().toLowerCase();
  if (!alias || !target) { this.showToast(t('alias.aliasPh') + ' / ' + t('alias.targetPh'), 'warning'); return; }
  if (alias === target) { this.showToast('alias = target', 'warning'); return; }
  try {
    await API.post('/tag-alias', { alias, target });
    aliasInput.value = '';
    targetInput.value = '';
    API.invalidate('/tag-aliases');
    this.loadAliases(true);
    this.showToast(`${alias} → ${target}`);
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
};