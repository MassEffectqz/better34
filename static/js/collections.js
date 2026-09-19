// collections.js — именованные коллекции постов: вкладка в профиле
// и быстрое добавление текущего поста из вьюера.
import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t } from './i18n.js';

App.renderCollections = function () {
  const host = this.els.collectionsList;
  if (!host) return;
  const cols = (this.state.profile && this.state.profile.collections) || [];
  if (this.els.tbCollections) this.els.tbCollections.textContent = String(cols.length);
  if (!cols.length) {
    host.innerHTML = `<p class="profile-empty">${esc(t('collections.empty'))}</p>`;
    return;
  }
  host.innerHTML = '';
  cols.forEach(col => {
    const d = document.createElement('div');
    d.className = 'collection-item';
    d.innerHTML = `
      <span class="collection-ico">${icon('folder', 15)}</span>
      <span class="collection-name">${esc(col.name)}</span>
      <span class="collection-count">${col.count}</span>
      <button class="btn-icon btn-icon-sm col-open" title="${esc(t('collections.openInGrid'))}">${icon('grid', 14)}</button>
      <button class="btn-icon btn-icon-sm col-edit" title="${esc(t('collections.rename'))}">${icon('pencil', 14)}</button>
      <button class="btn-icon btn-icon-sm col-del" title="${esc(t('collections.delete'))}">${icon('trash', 14)}</button>`;
    d.addEventListener('click', (e) => {
      if (e.target.closest('button')) return;
      this.openCollectionGrid(col);
    });
    d.querySelector('.col-open').addEventListener('click', () => this.openCollectionGrid(col));
    d.querySelector('.col-edit').addEventListener('click', () => this.renameCollectionInline(d, col));
    d.querySelector('.col-del').addEventListener('click', async () => {
      const ok = await this.confirmDialog({
        title: t('collections.delete'),
        message: `${t('collections.delete')} <b>«${esc(col.name)}»</b>?`,
        okText: t('confirm.ok'),
        danger: true,
      });
      if (!ok) return;
      API.del(`/collection/${col.id}`).then(() => {
        API.invalidate('/profile');
        this.loadProfile();
        this.showToast(`Коллекция «${col.name}» удалена`);
      }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
    });
    host.appendChild(d);
  });
};

App.createCollection = async function () {
  const input = this.els.collectionName;
  if (!input) return;
  const name = input.value.trim();
  if (!name) return;
  try {
    await API.post('/collection', { name });
    input.value = '';
    API.invalidate('/profile');
    await this.loadProfile();
    this.renderCollections();
    this.showToast(name);
    input.focus();
  } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
};

App.renameCollectionInline = function (d, col) {
  const nameEl = d.querySelector('.collection-name');
  if (!nameEl) return;
  const input = document.createElement('input');
  input.className = 'preset-rename';
  input.value = col.name;
  input.maxLength = 60;
  input.spellcheck = false;
  nameEl.replaceWith(input);
  input.focus();
  input.select();
  let doneFlag = false;
  const finish = (save) => {
    if (doneFlag) return;
    doneFlag = true;
    const name = input.value.trim();
    if (save && name && name !== col.name) {
      API.patch(`/collection/${col.id}`, { name }).then(() => {
        API.invalidate('/profile');
        this.loadProfile().then(() => this.renderCollections());
        this.showToast(name);
      }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
    } else {
      this.renderCollections();
    }
  };
  input.addEventListener('keydown', (ev) => {
    ev.stopPropagation();
    if (ev.key === 'Enter') { ev.preventDefault(); finish(true); }
    else if (ev.key === 'Escape') finish(false);
  });
  input.addEventListener('blur', () => finish(false));
};

App.openCollectionGrid = function (col) {
  if (!col || !col.id) return;
  API.get(`/collection/${col.id}/posts`, { fresh: true }).then(data => {
    const ids = ((data && data.posts) || []).map(p => p.id);
    if (!ids.length) { this.showToast(`Коллекция «${col.name}» пуста`, 'error'); return; }
    this.showGridMode('collection', ids);
  }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
};

// toggleCollectMenu — показать/скрыть меню «добавить в коллекцию» для поста.
App.toggleCollectMenu = async function (postId) {
  const menu = this.els.collectMenu;
  if (!menu) return;
  if (!menu.classList.contains('hidden')) { menu.classList.add('hidden'); return; }
  menu.classList.remove('hidden');
  await this.renderCollectMenu(postId, true);
};

// renderCollectMenu перерисовывает содержимое меню коллекций для поста:
// список с галочками + инлайн-создание новой коллекции.
App.renderCollectMenu = async function (postId, keepOpen) {
  const menu = this.els.collectMenu;
  if (!menu) return;
  let cols = [];
  try {
    const d = await API.get('/collections' + (postId ? `?post_id=${encodeURIComponent(postId)}` : ''), { fresh: true });
    cols = d.collections || [];
  } catch { /* меню останется пустым */ }
  menu.innerHTML =
    `<div class="collect-menu-new">
       <input type="text" class="collect-menu-new-input" placeholder="${esc(t('collections.newPh'))}" maxlength="60" spellcheck="false">
       <button type="button" class="btn-primary btn-sm collect-menu-create-btn" title="${esc(t('btn.create'))}">${icon('plus', 13)}</button>
     </div>` +
    cols.map(c =>
      `<button type="button" class="collect-menu-item${c.has ? ' active' : ''}" data-col-id="${esc(c.id)}">
         <span class="cm-check">${c.has ? icon('check', 13) : ''}</span>
         <span class="cm-name">${esc(c.name)}</span>
         <span class="cm-count">${c.count}</span>
       </button>`
    ).join('');
  if (!cols.length) {
    menu.insertAdjacentHTML('beforeend', `<div class="collect-menu-empty">${esc(t('collections.empty'))}</div>`);
  }
  if (!keepOpen && !this.state.viewerOpen) menu.classList.add('hidden');
};
