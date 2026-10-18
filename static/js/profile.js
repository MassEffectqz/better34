import { App } from './state.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';
import './profile_thumbs.js';
import './profile_presets.js';

App.renderProfile = function () {
  const p = this.state.profile;
  const img = this.els.profileAvatarImg;
  if (p.avatar) {
    img.src = p.avatar;
    img.style.display = 'block';
  } else {
    // Пустой src="" заставляет браузер запросить саму страницу, а .src при
    // чтении отдаёт URL документа — атрибут просто убираем.
    img.removeAttribute('src');
    img.style.display = 'none';
  }
  this.els.profileNickname.value = p.nickname || '';
  this._syncAvatarInitials(p.nickname);
  this._lastProfileMeta = { nickname: p.nickname || '', avatar: p.avatar || '' };
  this.updateAuthUI();
  this.renderPresets(p.presets);
  this.renderPresetMenu();
  this.renderThumbs('likes', p.liked_posts);
  this.renderThumbs('hides', p.hidden_posts);
  if (typeof this.renderCollections === 'function') this.renderCollections();
  this.renderTagLists();
  this._updateTagFilterCount();
  this.updateStats(p);
};

// Инициал вместо пустого тёмного круга, когда аватар не задан.
App._syncAvatarInitials = function (nickname) {
  const el = this.els.profileAvatarInitials;
  if (!el) return;
  const hasAvatar = !!(this.els.profileAvatarImg && this.els.profileAvatarImg.getAttribute('src'));
  if (hasAvatar) {
    el.textContent = '';
    el.style.display = 'none';
    return;
  }
  const u = this.state.user || {};
  const name = (nickname || u.nickname || u.username || '?').trim();
  el.textContent = (name.charAt(0) || '?').toUpperCase();
  el.style.display = '';
};

App.updateStats = function (p) {
  const setNum = (id, n) => {
    const el = document.getElementById(id);
    if (el) el.textContent = n;
  };
  const base = this.state.profile || {};
  if (!p) p = base;
  const presetN = (p.presets || []).length;
  const likeN = (p.liked_posts || []).length;
  const hideN = (p.hidden_posts || []).length;
  const tagN = (p.fav_tags || []).length + (p.hidden_tags || []).length;
  const colN = (p.collections || []).length;
  setNum('st-presets', presetN);
  setNum('st-likes', likeN);
  setNum('st-hides', hideN);
  setNum('st-tags', tagN);
  setNum('st-collections', colN);
  // «Скачать все лайки» бессмыслен при пустом списке.
  const dlBtn = this.els.btnLikesDownload;
  if (dlBtn) dlBtn.style.display = likeN ? '' : 'none';
};

App.toggleProfile = function () {
  const opening = !this.state.profileOpen;
  if (opening && !this.state.settingsOpen) this._panelReturnFocus = document.activeElement;
  const returnFocus = this._panelReturnFocus;
  this.state.profileOpen = opening;
  if (opening && this.state.settingsOpen) {
    this.state.settingsOpen = false;
    this.setPanelOpen(this.els.settingsPanel, false);
  }
  this.setPanelOpen(this.els.profilePanel, opening, opening ? null : returnFocus);
  this._syncPanels();
  if (opening) {
    this.applyPanelWidth();
    this.loadProfile();
  }
};

App.randomPost = async function () {
  try {
    const data = await API.get('/random');
    const p = (data && data.posts && data.posts[0]);
    if (!p) { this.showToast(t('pf.noRandom'), 'error'); return; }
    this._enterContext([{ ...p, _index: 0 }], { related: false });
  } catch (err) { this.showToast(tf('err.withMsg', { msg: err.message }), 'error'); }
};