import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

App.renderTagLists = function () {
  const p = this.state.profile || {};
  this.renderTagList('favTagsList', p.fav_tags || [], 'fav');
  this.renderTagList('hiddenTagsList', p.hidden_tags || [], 'hidden');
  this.renderTagPresetSelects();
};

App._tagPresetLists = function (pr) {
  const k = pr.kind || 'query';
  if (k === 'hidden') return { hidden: pr.tags || [], fav: [] };
  if (k === 'fav') return { hidden: [], fav: pr.tags || [] };
  return { hidden: pr.hidden_tags || [], fav: pr.fav_tags || [] };
};

App.renderTagPresetSelects = function () {
  const presets = ((this.state.profile && this.state.profile.presets) || [])
    .filter(pr => (pr.kind || 'query') !== 'query');
  const plural = (n, one, few, many) => {
    const m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return one;
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
    return many;
  };
  [['fav', this.els.favPresetSelect], ['hidden', this.els.hiddenPresetSelect]].forEach(([type, el]) => {
    if (!el) return;
    el.innerHTML = `<option value="">${esc(t('preset.standCurrent'))}</option>` + presets.map(pr => {
      const lists = this._tagPresetLists(pr);
      const n = type === 'fav' ? lists.fav.length : lists.hidden.length;
      return `<option value="${esc(pr.id)}">${esc(pr.name)} — ${n} ${plural(n, t('pf.tagOne'), t('pf.tagFew'), t('pf.tagMany'))}</option>`;
    }).join('');
    el.onchange = () => {
      const id = el.value;
      el.value = '';
      if (id) this.applyPresetById(id, type);
    };
  });
};

App.onTagFilter = function () {
  // U9: debounce 300ms — фильтрация на каждый keydown тормозит на больших списках.
  clearTimeout(this._tagFilterTimer);
  this._tagFilterTimer = setTimeout(() => {
    this._tagFilter = (this.els.tagFilter.value || '').toLowerCase().trim();
    this.renderTagLists();
    this._updateTagFilterCount();
  }, 300);
};

App._updateTagFilterCount = function () {
  const el = this.els.tagFilterCount;
  if (!el) return;
  const p = this.state.profile || {};
  const q = this._tagFilter || '';
  const fav = (p.fav_tags || []).filter(tag => !q || tag.toLowerCase().includes(q));
  const hid = (p.hidden_tags || []).filter(tag => !q || tag.toLowerCase().includes(q));
  const total = (p.fav_tags || []).length + (p.hidden_tags || []).length;
  const shown = fav.length + hid.length;
  el.textContent = total ? (q ? tf('pf.filterCount', { shown, total }) : `${total}`) : '';
};

App.renderPresets = function (presets) {
  const pl = this.els.presetsList;
  pl.innerHTML = '';
  const cur = this.state.query || '';
  const hint = this.els.presetCurrent;
  if (hint) {
    hint.innerHTML = cur
      ? `${esc(t('preset.savingCurrent'))} <b>${esc(cur)}</b>`
      : esc(t('preset.saveHint'));
  }
  presets = (presets || []).filter(pr => (pr.kind || 'query') === 'query');
  if (!presets.length) {
    pl.innerHTML = `<p class="profile-empty">${esc(t('preset.empty'))}</p>`;
    return;
  }
  presets.forEach((pr, i) => {
    const active = (pr.query || '') === cur;
    const d = document.createElement('div');
    d.className = 'preset-item' + (active ? ' active' : '');
    d.innerHTML = `
      <span class="preset-kind preset-kind-query">${icon('search', 13)}</span>
      <span class="preset-name">${esc(pr.name)}</span>
      <span class="preset-query">${esc(pr.query || t('preset.main'))}</span>
      <button class="btn-icon btn-icon-sm pf-up" title="${esc(t('preset.moveUp'))}">${icon('arrowUp', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-down" title="${esc(t('preset.moveDown'))}">${icon('arrowDown', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-edit" title="${esc(t('preset.rename'))}">${icon('pencil', 14)}</button>
      <button class="btn-icon btn-icon-sm pf-del" title="${esc(t('preset.delete'))}">${icon('x', 14)}</button>`;
    d.addEventListener('click', (e) => {
      if (e.target.closest('button')) return;
      this.applyPreset(pr);
      this.toggleProfile();
    });
    const up = d.querySelector('.pf-up');
    const down = d.querySelector('.pf-down');
    if (i === 0) up.style.visibility = 'hidden';
    if (i === presets.length - 1) down.style.visibility = 'hidden';
    up.addEventListener('click', (e) => { e.stopPropagation(); this.movePreset(pr.id, -1); });
    down.addEventListener('click', (e) => { e.stopPropagation(); this.movePreset(pr.id, 1); });
    d.querySelector('.pf-edit').addEventListener('click', (e) => { e.stopPropagation(); this.editPreset(d, pr); });
    d.querySelector('.pf-del').addEventListener('click', async (e) => {
      e.stopPropagation();
      const ok = await this.confirmDialog({
        title: t('preset.delete'),
        message: tf('preset.deleteConfirm', { name: esc(pr.name) }),
        okText: t('btn.delete'),
        danger: true,
      });
      if (!ok) return;
      API.del(`/preset/${pr.id}`).then(() => { API.invalidate('/profile'); this.loadProfile(); this.showToast(tf('preset.deleted', { name: pr.name })); }).catch(() => {});
    });
    pl.appendChild(d);
  });
};

App.editPreset = function (d, pr) {
  const nameEl = d.querySelector('.preset-name');
  const input = document.createElement('input');
  input.className = 'preset-rename';
  input.value = pr.name;
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
    if (save && name && name !== pr.name) {
      API.patch(`/preset/${pr.id}`, { name }).then(() => {
        API.invalidate('/profile');
        this.loadProfile();
        this.showToast(t('preset.renamed'));
      }).catch(err => {
        this.showToast(tf('err.withMsg', { msg: err.message }), 'error');
        nameEl.textContent = pr.name;
        input.replaceWith(nameEl);
      });
    } else {
      nameEl.textContent = pr.name;
      input.replaceWith(nameEl);
    }
  };
  input.addEventListener('keydown', (ev) => {
    ev.stopPropagation();
    if (ev.key === 'Enter') { ev.preventDefault(); finish(true); }
    else if (ev.key === 'Escape') { finish(false); }
  });
  input.addEventListener('blur', () => finish(true));
};

App.movePreset = function (id, dir) {
  API.post(`/preset/${id}/move`, { dir }).then(() => {
    API.invalidate('/profile');
    this.loadProfile();
  }).catch(() => {});
};

App._matchTag = function (tag) {
  const q = this._tagFilter || '';
  const idx = q ? tag.toLowerCase().indexOf(q.toLowerCase()) : -1;
  if (idx < 0) return esc(tag);
  return esc(tag.slice(0, idx)) + '<mark>' + esc(tag.slice(idx, idx + q.length)) + '</mark>' + esc(tag.slice(idx + q.length));
};

App.renderTagList = function (elId, tags, type) {
  const el = this.els[elId];
  if (!el) return;
  el.innerHTML = '';
  const q = this._tagFilter || '';
  if (q) tags = (tags || []).filter(tag => tag.toLowerCase().includes(q));
  if (!tags || !tags.length) {
    el.innerHTML = `<p class="profile-empty">${esc(t('tags.none'))}</p>`;
    return;
  }
  const isFav = type === 'fav';
  const activeCls = isFav ? 'fav' : 'hidden';
  tags.forEach(tag => {
    const d = document.createElement('div');
    d.className = 'profile-tag-item ' + activeCls;
    d.title = tag;
    let count = '';
    if (this._tagCounts && this._tagCounts[tag]) count = `<span class="tg-count">${this._tagCounts[tag]}</span>`;
    const ico = isFav ? icon('bookmark', 11) : icon('eye', 11);
    d.innerHTML = `${ico}<span>${this._matchTag(tag)}</span>${count}`;
    const btn = document.createElement('button');
    btn.className = isFav ? 'tg-unfav' : 'tg-unhide';
    btn.innerHTML = icon('x', 11);
    btn.title = isFav ? t('tags.unfav') : t('tags.unhide');
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      API.post(`/${isFav ? 'fav-tag' : 'hidden-tag'}`, { tag }).then(() => {
        API.invalidate('/profile');
        if (!isFav) { this.invalidateFeedCache(); this.loadPosts(true); }
        this.loadProfile();
      }).catch(() => {});
    });
    d.appendChild(btn);
    if (isFav) {
      d.title = tf('tags.search', { tag });
      d.addEventListener('click', (ev) => {
        if (ev.target.closest('button')) return;
        this.setSearchValue(tag);
        this.search(tag);
        this.toggleProfile();
      });
    } else {
      d.title = tf('tags.searchAvoid', { tag });
      d.addEventListener('click', (ev) => {
        if (ev.target.closest('button')) return;
        const cur = this.state.query;
        const newQ = cur ? `${cur} -${tag}` : `-${tag}`;
        this.setSearchValue(newQ);
        this.search(newQ);
        this.toggleProfile();
      });
    }
    el.appendChild(d);
  });
};

App.clearTagList = async function (type) {
  const key = type === 'fav' ? 'fav_tags' : 'hidden_tags';
  const label = type === 'fav' ? t('tags.favLabel') : t('tags.hiddenLabel');
  const tags = (this.state.profile && (this.state.profile[key] || [])) || [];
  if (!tags.length) {
    this.showToast(t('tags.nothingToClear'), 'error');
    return;
  }
  const ok = await this.confirmDialog({
    title: t('tags.clearTitle'),
    message: tf('tags.clearConfirm', { label, n: tags.length }),
    okText: t('btn.delete'),
    danger: true,
  });
  if (!ok) return;
  try {
    await API.post(type === 'fav' ? '/fav-tags/clear' : '/hidden-tags/clear');
    API.invalidate('/profile');
    if (type === 'hidden') this.invalidateFeedCache();
    await this.loadProfile();
    if (type === 'hidden') this.loadPosts(true);
    this.showToast(tf('tags.cleared', { n: tags.length, label }));
  } catch (err) {
    this.showToast(tf('err.withMsg', { msg: err.message }), 'error');
  }
};

App.savePreset = function () {
  const name = this.els.presetName.value.trim();
  if (!name) return;
  const query = this.state.query || '';
  if (!query) { this.showToast(t('preset.needSearch'), 'error'); return; }
  API.post('/preset', { name, query }).then(() => {
    this.els.presetName.value = '';
    API.invalidate('/profile');
    this.loadProfile();
    this.showToast(tf('preset.saved', { name }));
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.saveTagPreset = function (kind) {
  const p = this.state.profile || {};
  const fav = p.fav_tags || [];
  const hidden = p.hidden_tags || [];
  const list = kind === 'fav' ? fav : hidden;
  if (!list.length) {
    this.showToast(kind === 'fav' ? t('tags.noFav') : t('tags.noHidden'), 'error');
    return;
  }
  const label = kind === 'fav' ? t('tags.favHead') : t('tags.hiddenHead');
  const name = `${label} (${list.length})`;
  API.post('/preset', { name, kind: 'tags', hidden_tags: hidden, fav_tags: fav }).then(() => {
    API.invalidate('/profile');
    this.loadProfile();
    this.showToast(tf('preset.saved', { name }));
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.applyPreset = function (pr) {
  if ((pr.kind || 'query') !== 'query') return;
  this.setSearchValue(pr.query || '');
  this.search(pr.query || '');
};

App.applyPresetById = function (id, type) {
  if (type !== 'fav' && type !== 'hidden') return;
  API.post(`/preset/${id}/apply`, { type }).then(r => {
    API.invalidate('/profile');
    if (type === 'hidden') { this.invalidateFeedCache(); }
    this.loadProfile();
    if (type === 'hidden') this.loadPosts(true);
    const label = type === 'fav' ? 'tags.appliedFav' : 'tags.appliedHidden';
    this.showToast(r.applied > 0 ? tf(label, { n: r.applied }) : t('tags.setCleared'));
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.addTag = function (type) {
  const input = type === 'fav' ? this.els.favTagInput : this.els.hiddenTagInput;
  const tag = input.value.trim().toLowerCase().replace(/\s+/g, '_');
  if (!tag) return;
  const endpoint = type === 'fav' ? '/fav-tag' : '/hidden-tag';
  API.post(endpoint, { tag }).then(r => {
    input.value = '';
    API.invalidate('/profile');
    if (type === 'hidden') this.invalidateFeedCache();
    this.loadProfile();
    this.showToast(r.liked || r.hidden ? tf('tags.added', { tag }) : tf('tags.removed', { tag }));
    this.loadPosts(true);
  }).catch(err => this.showToast(tf('err.withMsg', { msg: err.message }), 'error'));
};

App.exportPresets = function () {
  const a = document.createElement('a');
  a.href = '/api/presets/export';
  a.download = 'briefly-presets.json';
  a.click();
  this.showToast(t('preset.exported'));
};

App.importPresets = async function (ev) {
  const file = ev.target.files[0];
  if (!file) return;
  try {
    const text = await file.text();
    const data = JSON.parse(text);
    if (!data.presets || !Array.isArray(data.presets)) { throw new Error(t('err.badFormat')); }
    const r = await API.post('/presets/import', { presets: data.presets });
    this.loadProfile();
    this.showToast(tf('preset.imported', { n: r.imported }));
  } catch (err) { this.showToast(tf('err.withMsg', { msg: err.message }), 'error'); }
  ev.target.value = '';
};

App.renderPresetMenu = function () {
  const list = this.els.presetMenuList;
  if (!list) return;
  const presets = ((this.state.profile && this.state.profile.presets) || [])
    .filter(pr => (pr.kind || 'query') === 'query');
  if (!presets.length) {
    list.innerHTML = `<div class="preset-menu-empty">${esc(t('preset.none'))}</div>`;
    return;
  }
  const cur = this.state.query || '';
  list.innerHTML = presets.map(pr =>
    `<button class="preset-menu-item${(pr.query || '') === cur ? ' active' : ''}" data-query="${esc(pr.query || '')}"><span class="pm-name">${esc(pr.name)}</span><span class="pm-query">${esc(pr.query || t('preset.main'))}</span></button>`
  ).join('');
};
