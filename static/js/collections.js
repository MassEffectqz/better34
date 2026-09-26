// collections.js — именованные коллекции постов: вкладка в профиле
// и быстрое добавление текущего поста из вьюера.
import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

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
  cols.forEach((col, i) => {
    const d = document.createElement('div');
    d.className = 'collection-item';
    d.dataset.colId = col.id;
    // Мозаика обложек: до 4 превью из /api/thumb/:id (эндпоинт сам докачает
    // превью с CDN, если локальной миниатюры нет).
    const covers = (col.covers || []).slice(0, 4);
    const cover = covers.length
      ? `<span class="col-mosaic">${covers.map(id => `<img src="/api/thumb/${id}" alt="" loading="lazy" decoding="async">`).join('')}</span>`
      : `<span class="collection-ico">${icon('folder', 15)}</span>`;
    d.innerHTML = `
      ${cover}
      <span class="collection-name">${esc(col.name)}</span>
      <span class="collection-count">${col.count}</span>
      <button class="btn-icon btn-icon-sm col-zip" title="${esc(t('collections.zip'))}" aria-label="${esc(t('collections.zip'))}">${icon('download', 14)}</button>
      <button class="btn-icon btn-icon-sm col-export" title="${esc(t('collections.export'))}" aria-label="${esc(t('collections.export'))}">${icon('save', 14)}</button>
      <button class="btn-icon btn-icon-sm col-up" title="${esc(t('collections.moveUp'))}" aria-label="${esc(t('collections.moveUp'))}"${i === 0 ? ' disabled' : ''}>${icon('arrowUp', 14)}</button>
      <button class="btn-icon btn-icon-sm col-down" title="${esc(t('collections.moveDown'))}" aria-label="${esc(t('collections.moveDown'))}"${i === cols.length - 1 ? ' disabled' : ''}>${icon('arrowDown', 14)}</button>
      <button class="btn-icon btn-icon-sm col-open" title="${esc(t('collections.openInGrid'))}">${icon('grid', 14)}</button>
      <button class="btn-icon btn-icon-sm col-edit" title="${esc(t('collections.rename'))}">${icon('pencil', 14)}</button>
      <button class="btn-icon btn-icon-sm col-del" title="${esc(t('collections.delete'))}">${icon('trash', 14)}</button>`;
    // Неотдавшаяся обложка убирается, иначе в мозаике остаются дыры.
    d.querySelectorAll('.col-mosaic img').forEach(img => {
      img.addEventListener('error', () => { if (img.parentNode) img.parentNode.removeChild(img); }, { once: true });
    });
    d.addEventListener('click', (e) => {
      if (e.target.closest('button')) return;
      this.openCollectionGrid(col);
    });
    d.querySelector('.col-open').addEventListener('click', () => this.openCollectionGrid(col));
    d.querySelector('.col-zip').addEventListener('click', () => this.downloadCollectionZip(col));
    d.querySelector('.col-export').addEventListener('click', () => this.exportCollectionJson(col));
    d.querySelector('.col-up').addEventListener('click', () => this.moveCollection(col, -1));
    d.querySelector('.col-down').addEventListener('click', () => this.moveCollection(col, 1));
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
        this.showToast(tf('collections.deleted', { name: col.name }));
      }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
    });
    host.appendChild(d);
  });
};

// moveCollection — сдвиг коллекции на ±1 позицию. Порядок применяется
// оптимистично (список перерисовывается сразу), при ошибке возвращаемся.
App.moveCollection = async function (col, dir) {
  const profile = this.state.profile || {};
  const cols = profile.collections || [];
  const i = cols.findIndex(c => c.id === col.id);
  const j = i + dir;
  if (i < 0 || j < 0 || j >= cols.length) return;
  const order = cols.map(c => c.id);
  [order[i], order[j]] = [order[j], order[i]];
  const before = cols.slice();
  profile.collections = cols.slice();
  profile.collections[i] = cols[j];
  profile.collections[j] = cols[i];
  this.renderCollections();
  try {
    await API.post('/collections/reorder', { ids: order });
    API.invalidate('/profile');
  } catch (err) {
    profile.collections = before;
    this.renderCollections();
    this.showToast(err && err.message ? tf('err.withMsg', { msg: err.message }) : t('err.generic'), 'error');
  }
};

// downloadCollectionZip — архив скачанных файлов коллекции (нескачанные
// пропускаются сервером, он же сообщает их число в X-Zip-Skipped).
App.downloadCollectionZip = function (col) {
  const ids = col.ids || [];
  if (!ids.length) { this.showToast(t('collections.emptyToast', { name: col.name }), 'error'); return; }
  const a = document.createElement('a');
  a.href = `/api/download-zip?ids=${ids.join(',')}`;
  a.download = 'briefly.zip';
  document.body.appendChild(a);
  a.click();
  a.remove();
  this.showToast(tf('collections.queued', { n: ids.length }));
};

// exportCollectionJson — выгрузка id-состава коллекции в JSON (бэкап списка
// постов без файлов: переносится на другой инстанс через импорт пресетов).
App.exportCollectionJson = function (col) {
  const ids = col.ids || [];
  if (!ids.length) { this.showToast(t('collections.emptyToast', { name: col.name }), 'error'); return; }
  const data = {
    collection: { id: col.id, name: col.name, count: col.count },
    posts: ids,
    exported_at: new Date().toISOString(),
  };
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = `${(col.name || 'collection').replace(/[^\wа-яА-ЯёЁ\- ]/g, '_')}.json`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  // Blob-URL живёт до перезагрузки страницы, но освобождаем сразу после клика.
  URL.revokeObjectURL(a.href);
  this.showToast(tf('collections.exported', { name: col.name }));
};

// downloadAllCollectionsZip — один архив по всем коллекциям сразу (id
// дедуплицируются, кап 2000 — как у /api/download-zip).
App.downloadAllCollectionsZip = function () {
  const cols = (this.state.profile && this.state.profile.collections) || [];
  const ids = [];
  const seen = new Set();
  cols.forEach(col => (col.ids || []).forEach(id => {
    if (!seen.has(id)) { seen.add(id); ids.push(id); }
  }));
  if (!ids.length) { this.showToast(t('collections.noDownloaded'), 'error'); return; }
  const a = document.createElement('a');
  a.href = `/api/download-zip?ids=${ids.slice(0, 2000).join(',')}`;
  a.download = 'briefly.zip';
  document.body.appendChild(a);
  a.click();
  a.remove();
  this.showToast(tf('collections.queued', { n: ids.length }));
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
    if (!ids.length) { this.showToast(t('collections.emptyToast', { name: col.name }), 'error'); return; }
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
