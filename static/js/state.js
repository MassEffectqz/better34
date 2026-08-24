// state.js — корневой объект приложения. Все модули импортируют App отсюда
// и вешают свои методы на него; циклических импортов нет (state тянет только utils/api).
import { _, esc, go, icon } from './utils.js';
import { API } from './api.js';

export const App = {
  state: {
    posts: [], page: 1, loading: false, hasMore: true, query: '',
    isLocal: false, viewerOpen: false, viewerIndex: 0, recommendActive: false,
    settingsOpen: false, profileOpen: false,
    downloadQueue: new Set(), downloading: new Set(), focusedIndex: -1,
    profile: { liked_posts: [], hidden_posts: [], presets: [], fav_tags: [], hidden_tags: [] },
    selected: new Set(), slideshowActive: false, slideshowSpeed: 3000, minId: null, hoveredIndex: -1,
    sortBy: '',
    displayMode: 'search', displayIds: [], theme: 'dark',
    gridCols: null,
  },
  els: {},

  async init() {
    this.els = {
      grid: _('posts-grid'), sentinel: _('sentinel'),
      searchInput: _('search-input'), suggestions: _('suggestions'),
      searchBox: document.querySelector('.search-box'),
      viewer: _('viewer'), viewerHead: _('viewer-head'), viewerFoot: _('viewer-foot'), viewerContent: _('viewer-content'), viewerInfo: _('viewer-info'),
      viewerTags: _('viewer-tags'), viewerProgress: _('viewer-progress'),
      viewerComments: _('viewer-comments'),
      viewerDownload: _('viewer-download'), viewerFullscreen: _('viewer-fullscreen'),
      viewerMobileActions: _('viewer-mobile-actions'), viewerMobileToggle: _('viewer-mobile-toggle'),
      zoomIn: _('zoom-in'), zoomOut: _('zoom-out'), zoomFit: _('zoom-fit'), zoomLabel: _('zoom-label'),
      zoomHud: _('zoom-hud'), zThumbX: _('z-thumb-x'), zThumbY: _('z-thumb-y'),
      viewerClose: _('viewer-close'), viewerLike: _('viewer-like'), viewerHide: _('viewer-hide'),
      viewerLikeM: _('viewer-like-mobile'), viewerHideM: _('viewer-hide-mobile'), viewerFullscreenM: _('viewer-fullscreen-mobile'),
      prevBtn: _('prev-btn'), nextBtn: _('next-btn'),
      settingsPanel: _('settings-panel'), settingsClose: _('settings-close'),
      btnLocal: _('btn-local'),
      statsModal: _('stats-modal'), statsBody: _('stats-body'), statsClose: _('stats-close'),
      toast: _('toast'), statusText: _('status-text'),
      btnSaveSettings: _('btn-save-settings'),
      apiKeysList: _('api-keys-list'), btnAddApiKey: _('btn-add-api-key'),
      settingProxy: _('setting-proxy'), settingSavepath: _('setting-savepath'),
      settingProvider: _('setting-provider'),
      settingConcurrent: _('setting-concurrent'),
      settingTheme: _('setting-theme'), settingGrid: _('setting-grid'), settingAccent: _('setting-accent'),
      profilePanel: _('profile-panel'), profileClose: _('profile-close'),
      presetName: _('preset-name'), btnSavePreset: _('btn-save-preset'), presetCurrent: _('preset-current'),
      presetsList: _('presets-list'), likesList: _('likes-list'), hidesList: _('hides-list'),
      favTagsList: _('fav-tags-list'), hiddenTagsList: _('hidden-tags-list'),
      favPresetSelect: _('fav-preset-select'), hiddenPresetSelect: _('hidden-preset-select'),
      favTagInput: _('fav-tag-input'), hiddenTagInput: _('hidden-tag-input'),
      btnAddFavTag: _('btn-add-fav-tag'), btnAddHiddenTag: _('btn-add-hidden-tag'),
      likesEmpty: _('likes-empty'), hidesEmpty: _('hides-empty'),
      batchBar: _('batch-bar'), batchCount: _('batch-count'), batchDownload: _('batch-download'), batchHide: _('batch-hide'), batchClear: _('batch-clear'),
      historyDropdown: _('history-dropdown'),
      slideshowBtn: _('slideshow-btn'),

      btnHome: _('btn-home'),
      viewerLoader: _('viewer-loader'),
      profileSuggestions: _('profile-suggestions'), favSuggestions: _('fav-suggestions'),
      ssSpeedInput: _('ss-speed-input'),
      modeBar: _('mode-bar'), modeBarText: _('mode-bar-text'), modeBarRefresh: _('mode-bar-refresh'), modeBarClose: _('mode-bar-close'),
      btnLikesGrid: _('btn-likes-grid'), btnHidesGrid: _('btn-hides-grid'),
      btnLikesMore: _('btn-likes-more'), btnHidesMore: _('btn-hides-more'),
      tagFilter: _('tag-filter'), tagFilterCount: _('tag-filter-count'),
      btnClearFavTags: _('btn-clear-fav-tags'), btnClearHiddenTags: _('btn-clear-hidden-tags'),
      profileAvatar: _('profile-avatar'), profileAvatarImg: _('profile-avatar-img'),
      profileAvatarFile: _('profile-avatar-file'), profileNickname: _('profile-nickname'),
      btnDBClean: _('btn-clean-db'),
      dlProgress: _('dl-progress'), dlProgressBar: _('dl-progress-bar'), dlProgressText: _('dl-progress-text'), dlPause: _('dl-pause'), dlResume: _('dl-resume'),
      queueModal: _('queue-modal'), queueBody: _('queue-body'), queueClose: _('queue-close'),
      btnExportPresets: _('btn-export-presets'), btnImportPresets: _('btn-import-presets'), presetImportFile: _('preset-import-file'),
      btnHeaderMenu: _('btn-header-menu'), headerMenu: _('header-menu'), searchClear: _('search-clear'),
      providerBadge: _('provider-badge'), providerMenu: _('provider-menu'), providerMenuList: _('provider-menu-list'),
      ratingToggle: _('rating-toggle'), feedProgress: _('feed-progress'), queryMeta: _('query-meta'),
      searchChips: _('search-chips'), scrollProgress: _('scroll-progress'),
      viewerRelated: _('viewer-related'), btnLikesDownload: _('btn-likes-download'),
      panelBackdrop: _('panel-backdrop'),
      filterChips: _('filter-chips'),
      confirmModal: _('confirm-modal'), confirmTitle: _('confirm-title'), confirmMessage: _('confirm-message'),
      confirmOk: _('confirm-ok'), confirmCancel: _('confirm-cancel'), confirmClose: _('confirm-close'), confirmBackdrop: _('confirm-backdrop'),
      helpModal: _('help-modal'), helpBody: _('help-body'), helpClose: _('help-close'), helpBackdrop: _('help-backdrop'),
      btnFindDups: _('btn-find-dups'), btnCleanDups: _('btn-clean-dups'), dupsInfo: _('dups-info'),
    };
    this.loadTheme();
    this.loadGridSetting();
    try {
      const savedScroll = parseInt(sessionStorage.getItem('briefly_scroll_top') || '', 10);
      if (Number.isFinite(savedScroll) && savedScroll > 0) this._pendingScrollTop = savedScroll;
    } catch {}
    this.bindAuthEvents();
    await this.initAuth();
    this.bindEvents();
    this.initIntersectionObserver();
    this.initPullToRefresh();
    this.startDlPoll();
    try { if (localStorage.getItem('briefly_viewer_panel') === '1') this.els.viewerMobileActions.classList.add('collapsed'); } catch (_) {}
    this.loadAccent();
    this.loadSettings();
    await this.loadProfile();
    this.loadProfileMeta();
    this._loadTagCounts();
    const path = location.pathname + location.search;
    let m = path.match(/^\/search\/(.+?)(?:\/post\/(\d+))?$/);
    let restorePostId = null;
    if (m) {
      this.state.query = decodeURIComponent(m[1]);
      this.els.searchInput.value = this.state.query;
      restorePostId = m[2] ? parseInt(m[2]) : null;
    } else {
      m = path.match(/^\/post\/(\d+)$/);
      if (m) restorePostId = parseInt(m[1]);
    }
    this.loadPosts(true, restorePostId);
  },

  onPopState(ev) {
    const path = location.pathname + location.search;
    this._lastURL = path;
    if (path === '/' || path === '') {
      if (this.state.viewerOpen) { this.closeViewer(); return; }
      if (this.state.query) {
        this.state.query = '';
        this.els.searchInput.value = '';
        this.loadPosts(true);
        return;
      }
      return;
    }
    let m = path.match(/^\/search\/(.+?)(?:\/post\/(\d+))?$/);
    let q = '', postId = null, matched = false;
    if (m) {
      q = decodeURIComponent(m[1]);
      if (m[2]) postId = parseInt(m[2]);
      matched = true;
    } else {
      m = path.match(/^\/post\/(\d+)$/);
      if (m) { postId = parseInt(m[1]); matched = true; }
    }
    if (matched) {
      if (q !== this.state.query || this.state.posts.length === 0) {
        this.state.query = q;
        this.els.searchInput.value = q;
        this.loadPosts(true).then(() => {
          if (postId != null) {
            const idx = this.state.posts.findIndex(p => p.id === postId);
            if (idx >= 0) this.openViewer(idx);
          }
        });
      } else if (postId != null) {
        const idx = this.state.posts.findIndex(p => p.id === postId);
        if (idx >= 0) this.openViewer(idx);
        else if (this.state.viewerOpen) this.closeViewer();
      } else if (this.state.viewerOpen) {
        this.closeViewer();
      }
    }
  },

  bindEvents() {
    const e = this.els;
    e.searchInput.addEventListener('input', () => this.onSearchInput());
    e.searchInput.addEventListener('keydown', (ev) => this.onSearchKeydown(ev));
    e.searchInput.addEventListener('focus', () => this.showHistory());
    e.searchInput.addEventListener('blur', () => setTimeout(() => this.hideHistory(), 200));
    document.addEventListener('pointerdown', (ev) => {
      if (e.searchBox && !e.searchBox.contains(ev.target)) {
        this._suggestSeq++;
        this.renderSuggestions([]);
        this.hideHistory();
      }
    });
    e.viewerClose.addEventListener('click', () => this.closeViewer());
    e.prevBtn.addEventListener('click', () => this.navigateViewer(-1));
    e.nextBtn.addEventListener('click', () => this.navigateViewer(1));
    e.viewerDownload.addEventListener('click', () => this.downloadCurrent());
    e.viewerMobileToggle.addEventListener('click', () => this.toggleViewerMobile());
    e.viewerFullscreen.addEventListener('click', () => this.toggleFullscreen());
    e.viewerLike.addEventListener('click', () => this.toggleLikeCurrent());
    e.viewerHide.addEventListener('click', () => this.toggleHideCurrent());
    if (e.viewerLikeM) e.viewerLikeM.addEventListener('click', () => this.toggleLikeCurrent());
    if (e.viewerHideM) e.viewerHideM.addEventListener('click', () => this.toggleHideCurrent());
    if (e.viewerFullscreenM) e.viewerFullscreenM.addEventListener('click', () => this.toggleFullscreen());
    e.viewer.addEventListener('click', (ev) => { if (ev.target === e.viewer) this.closeViewer(); });
    e.btnLocal.addEventListener('click', () => this.toggleLocal());
    e.profileClose.addEventListener('click', () => this.toggleProfile());
    e.settingsClose.addEventListener('click', () => this.toggleSettings());
    e.panelBackdrop.addEventListener('click', () => { if (this.state.profileOpen) this.toggleProfile(); else if (this.state.settingsOpen) this.toggleSettings(); });
    e.statsClose.addEventListener('click', () => this.hideStats());
    e.queueClose.addEventListener('click', () => this.hideQueue());
    e.queueModal.addEventListener('click', (ev) => { if (ev.target === e.queueModal || ev.target.classList.contains('modal-backdrop')) this.hideQueue(); });
    e.dlProgress.addEventListener('click', (ev) => { if (ev.target === e.dlPause || ev.target === e.dlResume) return; go(this.showQueue()); });
    e.searchClear.addEventListener('click', () => { this.state.query = ''; this.els.searchInput.value = ''; this.els.searchInput.focus(); e.searchClear.classList.remove('visible'); this.search(''); });
    e.btnHeaderMenu.addEventListener('click', (ev) => { ev.stopPropagation(); e.headerMenu.classList.toggle('hidden'); });
    e.headerMenu.addEventListener('click', (ev) => {
      const item = ev.target.closest('.header-menu-item');
      if (!item) return;
      e.headerMenu.classList.add('hidden');
      const action = item.dataset.action;
      if (action === 'profile') this.toggleProfile();
      else if (action === 'theme') this.toggleTheme();
      else if (action === 'settings') this.toggleSettings();
      else if (action === 'queue') go(this.showQueue());
      else if (action === 'stats') go(this.showStats());
      else if (action === 'logout') go(this.logout());
      else if (action === 'local') this.toggleLocal();
      else if (action === 'random') go(this.randomPost());
      else if (action === 'recommend') this.recommendFeed();
      else if (action === 'grid') this.cycleGridDensity();
      else if (action === 'help') this.toggleHelp();
      else if (action === 'presets') this.openPresetsSubmenu();
      else if (action === 'presets-back') this.closePresetsSubmenu();
      else if (action === 'preset') {
        const kind = item.dataset.kind || 'query';
        if (kind === 'query') {
          const q = item.dataset.query || '';
          this.els.searchInput.value = q;
          this.state.query = q;
          this.search(q);
        } else {
          this.applyPresetById(item.dataset.presetId);
        }
      }
    });
    e.viewerRelated.addEventListener('click', (ev) => {
      const it = ev.target.closest('.rel-item');
      if (!it) return;
      const p = this._relPosts && this._relPosts[parseInt(it.dataset.idx, 10)];
      if (p) this.openRelatedChain(p, this._relPosts);
    });
    e.btnLikesDownload.addEventListener('click', () => go(this.downloadAllLikes()));
    document.addEventListener('click', (ev) => {
      if (!e.headerMenu.classList.contains('hidden') && !e.btnHeaderMenu.contains(ev.target) && !e.headerMenu.contains(ev.target)) { e.headerMenu.classList.add('hidden'); }
      if (e.providerMenu && !e.providerMenu.classList.contains('hidden') && !e.providerBadge.contains(ev.target) && !e.providerMenu.contains(ev.target)) { e.providerMenu.classList.add('hidden'); }
    });
    if (e.providerBadge) {
      e.providerBadge.addEventListener('click', (ev) => { ev.stopPropagation(); e.providerMenu.classList.toggle('hidden'); });
      e.providerMenu.addEventListener('click', async (ev) => {
        const it = ev.target.closest('.provider-menu-item');
        if (!it || it.classList.contains('active')) { e.providerMenu.classList.add('hidden'); return; }
        e.providerMenu.classList.add('hidden');
        await this.switchProvider(it.dataset.value);
      });
    }
    if (e.ratingToggle) {
      const saved = (() => { try { return localStorage.getItem('briefly-rating') || ''; } catch { return ''; } })();
      this.state.ratingFilter = saved;
      e.ratingToggle.querySelectorAll('.rt-btn').forEach(b => b.classList.toggle('active', b.dataset.rating === saved));
      e.ratingToggle.addEventListener('click', (ev) => {
        const b = ev.target.closest('.rt-btn');
        if (!b || b.dataset.rating === this.state.ratingFilter) return;
        this.state.ratingFilter = b.dataset.rating;
        try { localStorage.setItem('briefly-rating', b.dataset.rating); } catch {}
        e.ratingToggle.querySelectorAll('.rt-btn').forEach(x => x.classList.toggle('active', x === b));
        API.invalidate('/');
        this.loadPosts(true, null, true);
      });
    }
    // Автоскрытие хедера + полоса глубины скролла ленты.
    const mainEl = document.getElementById('main');
    if (mainEl) mainEl.addEventListener('scroll', () => this.onMainScroll(), { passive: true });
    e.dlPause.addEventListener('click', () => go(this.pauseDownloads()));
    e.dlResume.addEventListener('click', () => go(this.resumeDownloads()));
    e.btnSaveSettings.addEventListener('click', () => go(this.saveSettings()));
    e.settingTheme.addEventListener('change', () => this.setTheme(e.settingTheme.value));
    e.settingGrid.addEventListener('change', () => this.setGridSetting(e.settingGrid.value));
    e.settingAccent.addEventListener('click', (ev) => {
      const sw = ev.target.closest('.accent-swatch');
      if (sw) this.setAccent(sw.dataset.accent);
    });
    e.btnAddApiKey.addEventListener('click', () => this.renderAPIKeys([...this.state.apiKeys, { name: '', api_key: '', user_id: '' }]));
    e.btnDBClean.addEventListener('click', () => go(this.cleanDB()));
    e.btnExportPresets.addEventListener('click', () => go(this.exportPresets()));
    e.btnImportPresets.addEventListener('click', () => e.presetImportFile.click());
    e.presetImportFile.addEventListener('change', (ev) => go(this.importPresets(ev)));
    e.btnAddFavTag.addEventListener('click', () => this.addTag('fav'));
    e.favTagInput.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); this.addTag('fav'); } });
    e.btnAddHiddenTag.addEventListener('click', () => this.addTag('hidden'));
    e.hiddenTagInput.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); this.addTag('hidden'); } });
    e.btnSavePreset.addEventListener('click', () => this.savePreset());
    e.presetName.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); this.savePreset(); } });
    const btnSaveHiddenPreset = _('btn-save-hidden-preset');
    const btnSaveFavPreset = _('btn-save-fav-preset');
    if (btnSaveHiddenPreset) btnSaveHiddenPreset.addEventListener('click', () => this.saveTagPreset('hidden'));
    if (btnSaveFavPreset) btnSaveFavPreset.addEventListener('click', () => this.saveTagPreset('fav'));
    e.batchDownload.addEventListener('click', () => this.batchDownload());
    e.batchHide.addEventListener('click', () => this.batchHide());
    e.batchClear.addEventListener('click', () => this.clearSelection());
    e.helpClose.addEventListener('click', () => this.hideHelp());
    e.helpBackdrop.addEventListener('click', () => this.hideHelp());
    e.btnFindDups.addEventListener('click', () => go(this.findDuplicates()));
    e.btnCleanDups.addEventListener('click', () => go(this.cleanDuplicates()));
    if (e.slideshowBtn) e.slideshowBtn.addEventListener('click', () => this.toggleSlideshow());
    if (e.btnHome) e.btnHome.addEventListener('click', () => this.goHome());
    e.ssSpeedInput.addEventListener('change', () => {
      const v = Math.max(1, Math.min(30, parseInt(e.ssSpeedInput.value) || 3));
      e.ssSpeedInput.value = v;
      this.state.slideshowSpeed = v * 1000;
    });
    e.favTagInput.addEventListener('input', () => this.suggestProfileTag('fav'));
    e.hiddenTagInput.addEventListener('input', () => this.suggestProfileTag('hidden'));
    document.querySelectorAll('#profile-panel .profile-tab').forEach((/** @type {HTMLElement} */ tab) => {
      tab.addEventListener('click', () => {
        document.querySelectorAll('#profile-panel .profile-tab').forEach((/** @type {HTMLElement} */ t) => t.classList.remove('active'));
        document.querySelectorAll('#profile-panel .profile-tab-content').forEach((/** @type {HTMLElement} */ c) => c.classList.remove('active'));
        tab.classList.add('active');
        _(`tab-${tab.dataset.tab}`).classList.add('active');
        if (tab.dataset.tab === 'likes' || tab.dataset.tab === 'hides') {
          this.renderThumbs(tab.dataset.tab, this.state.profile[tab.dataset.tab === 'likes' ? 'liked_posts' : 'hidden_posts']);
        }
      });
    });
    document.querySelectorAll('#settings-panel .profile-tab').forEach((/** @type {HTMLElement} */ tab) => {
      tab.addEventListener('click', () => {
        document.querySelectorAll('#settings-panel .profile-tab').forEach(t => t.classList.remove('active'));
        document.querySelectorAll('#settings-panel .profile-tab-content').forEach(c => c.classList.remove('active'));
        tab.classList.add('active');
        _(`stab-${tab.dataset.tab}`).classList.add('active');
      });
    });
    e.modeBarClose.addEventListener('click', () => { if (this.state.recommendActive) this.exitRecommend(); else this.clearMode(); });
    e.modeBarRefresh.addEventListener('click', () => this.refreshRecommend());
    e.btnLikesGrid.addEventListener('click', () => this.showGridMode('likes'));
    e.btnHidesGrid.addEventListener('click', () => this.showGridMode('hides'));
    e.btnLikesMore.addEventListener('click', () => this.loadMoreThumbs('likes'));
    e.btnHidesMore.addEventListener('click', () => this.loadMoreThumbs('hides'));
    e.tagFilter.addEventListener('input', () => this.onTagFilter());
    e.btnClearFavTags.addEventListener('click', () => this.clearTagList('fav'));
    e.btnClearHiddenTags.addEventListener('click', () => this.clearTagList('hidden'));
    this.bindProfileStats();
    e.profileAvatar.addEventListener('click', () => e.profileAvatarFile.click());
    e.profileAvatarFile.addEventListener('change', (ev) => this.onAvatarChange(ev));
    e.profileNickname.addEventListener('blur', () => this.saveProfileMeta());
    e.profileNickname.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); this.saveProfileMeta(); e.profileNickname.blur(); } });
    document.addEventListener('keydown', (ev) => this.onKeydown(ev));
    document.addEventListener('keyup', (ev) => this.onKeyup(ev));
    window.addEventListener('popstate', (ev) => this.onPopState(ev));
    const savedW = parseFloat(localStorage.getItem('briefly_panel_width') || '');
    this._panelWidth = isFinite(savedW) ? savedW : null;
    this.applyPanelWidth();
    this.bindPanelResize();
    this.bindPanelSwipe();
    let pwT;
    window.addEventListener('resize', () => {
      clearTimeout(pwT);
      pwT = setTimeout(() => this.applyPanelWidth(), 150);
    });
  },

  bindAuthEvents() {
    _('auth-submit').addEventListener('click', () => go(this.authSubmit()));
    _('auth-toggle').addEventListener('click', () => this.authToggleMode());
    _('auth-username').addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); _('auth-password').focus(); } });
    _('auth-password').addEventListener('keydown', (ev) => {
      if (ev.key !== 'Enter') return;
      ev.preventDefault();
      if (this._authMode === 'register') _('auth-password2').focus();
      else go(this.authSubmit());
    });
    _('auth-password2').addEventListener('keydown', (ev) => { if (ev.key === 'Enter') { ev.preventDefault(); go(this.authSubmit()); } });
    const EYE_ON = icon('eye');
    const EYE_OFF = icon('eyeOff');
    document.querySelectorAll('.auth-pw-toggle').forEach((btn) => {
      btn.addEventListener('click', () => {
        const inp = /** @type {HTMLInputElement} */ (document.getElementById(/** @type {HTMLElement} */ (btn).dataset.target));
        const show = inp.type === 'password';
        inp.type = show ? 'text' : 'password';
        btn.innerHTML = show ? EYE_OFF : EYE_ON;
        inp.focus();
      });
    });
  },

  loadTheme() {
    let theme = 'dark';
    try { theme = localStorage.getItem('briefly_theme') || 'dark'; } catch {}
    this.state.theme = theme;
    document.documentElement.setAttribute('data-theme', theme);
  },

  loadGridSetting() {
    let v = null;
    try { v = localStorage.getItem('briefly_grid_cols'); } catch {}
    const n = parseInt(v || '', 10);
    this.state.gridCols = (Number.isInteger(n) && n >= 2 && n <= 6) ? n : null;
    this.renderGridMenu();
  },

  setGridSetting(val) {
    const n = parseInt(val || '', 10);
    this.state.gridCols = (val === 'auto' || !Number.isInteger(n)) ? null : (n >= 2 ? Math.min(n, 6) : null);
    try { localStorage.setItem('briefly_grid_cols', this.state.gridCols === null ? 'auto' : String(this.state.gridCols)); } catch {}
    this.renderGridMenu();
    this.rebuildMasonry();
  },

  renderGridMenu() {
    const active = this.state.gridCols === null ? 'auto' : String(this.state.gridCols);
    document.querySelectorAll('.grid-opt').forEach((/** @type {HTMLElement} */ b) => {
      b.classList.toggle('active', b.dataset.cols === active);
    });
  },

  // Полоса глубины скролла + скрытие хедера при прокрутке вниз.
  onMainScroll() {
    const main = document.getElementById('main');
    if (!main) return;
    const y = main.scrollTop;
    const max = main.scrollHeight - main.clientHeight;
    if (this.els.scrollProgress) {
      this.els.scrollProgress.style.width = max > 4 ? ((y / max) * 100).toFixed(2) + '%' : '0%';
    }
    const hd = document.getElementById('header');
    if (!hd) return;
    const e = this.els;
    const dropdownsClosed = !e.suggestions.classList.contains('active')
      && !e.historyDropdown.classList.contains('active')
      && (!e.providerMenu || e.providerMenu.classList.contains('hidden'))
      && e.headerMenu.classList.contains('hidden');
    const inputFocused = document.activeElement === e.searchInput;
    const last = this._lastST ?? 0;
    if (y <= 60 || y < last - 4 || inputFocused || !dropdownsClosed) {
      hd.classList.remove('header-hidden');
    } else if (y > last + 8) {
      if (!hd.style.getPropertyValue('--hh')) hd.style.setProperty('--hh', '-' + hd.offsetHeight + 'px');
      hd.classList.add('header-hidden');
    }
    this._lastST = y;
  },

  ACCENTS: {
    purple: ['#a78bfa', '#7c5cbf'],
    blue: ['#60a5fa', '#3b82f6'],
    cyan: ['#22d3ee', '#0891b2'],
    green: ['#34d399', '#059669'],
    pink: ['#f472b6', '#db2777'],
    orange: ['#fb923c', '#ea580c'],
    red: ['#f87171', '#dc2626'],
  },

  loadAccent() {
    let name = 'purple';
    try { name = localStorage.getItem('briefly_accent') || 'purple'; } catch {}
    if (!this.ACCENTS[name]) name = 'purple';
    this.state.accent = name;
    this.applyAccent(name);
  },

  applyAccent(name) {
    const [a, d] = this.ACCENTS[name];
    document.documentElement.style.setProperty('--accent', a);
    document.documentElement.style.setProperty('--accent-dim', d);
  },

  setAccent(name) {
    if (!this.ACCENTS[name]) return;
    this.state.accent = name;
    this.applyAccent(name);
    try { localStorage.setItem('briefly_accent', name); } catch {}
    this.renderAccent();
  },

  renderAccent() {
    document.querySelectorAll('.accent-swatch').forEach((/** @type {HTMLElement} */ b) => {
      b.classList.toggle('active', b.dataset.accent === this.state.accent);
    });
  },

  setTheme(theme) {
    if (theme !== 'dark' && theme !== 'light') return;
    this.state.theme = theme;
    document.documentElement.setAttribute('data-theme', theme);
    try { localStorage.setItem('briefly_theme', theme); } catch {}
  },

  toggleTheme() {
    this.setTheme(this.state.theme === 'dark' ? 'light' : 'dark');
  },

  pushState(query, postId) {
    let url = query ? `/search/${encodeURIComponent(query)}` : '/';
    if (postId != null) url = url === '/' ? `/post/${postId}` : `${url}/post/${postId}`;
    if (url === this._lastURL) return;
    this._lastURL = url;
    history.pushState({ query, postId }, '', url);
  },

  async loadProfile() {
    if (this._profilePromise) return this._profilePromise;
    this._profilePromise = (async () => {
      try {
        this.state.profile = await API.get('/profile');
        this._profileFresh = true;
        this.renderProfile();
        this.renderFilterChips();
      } catch (err) {
        console.error('Failed to load profile:', err);
      }
    })();
    try {
      return await this._profilePromise;
    } finally {
      this._profilePromise = null;
    }
  },

  loadProfileMeta() {
    const p = this.state.profile || /** @type {any} */ ({});
    let avatar = p.avatar || '';
    let nickname = p.nickname || '';
    if (!avatar && !nickname) {
      try {
        const m = JSON.parse(localStorage.getItem('briefly_profile_meta') || '{}');
        avatar = m.avatar || '';
        nickname = m.nickname || '';
      } catch {}
    }
    return { avatar, nickname };
  },

  saveProfileMeta() {
    const data = {
      avatar: this.els.profileAvatarImg.src || '',
      nickname: this.els.profileNickname.value.trim() || '',
    };
    if (data.avatar && data.avatar.length > 3 * 1024 * 1024) {
      this.showToast('Аватар слишком большой — выберите меньшее изображение', 'error');
      return;
    }
    API.post('/profile/meta', data).then(() => {
      if (this.state.user) {
        this.state.user.nickname = data.nickname;
        this.state.user.avatar = data.avatar;
      }
      API.invalidate('/profile');
      this.showToast('Профиль обновлён');
    }).catch(err => this.showToast(`Ошибка: ${err.message}`, 'error'));
    try { localStorage.setItem('briefly_profile_meta', JSON.stringify(data)); } catch {}
  },

  onAvatarChange(ev) {
    const file = ev.target.files[0];
    if (!file) return;
    if (!file.type.startsWith('image/')) { this.showToast('Только изображения', 'error'); return; }
    const reader = new FileReader();
    reader.onload = (e) => {
      const src = /** @type {string} */ (e.target.result);
      const img = new Image();
      img.onload = () => {
        const MAX = 512;
        let { width, height } = img;
        if (Math.max(width, height) > MAX) {
          const k = MAX / Math.max(width, height);
          width = Math.round(width * k);
          height = Math.round(height * k);
        }
        const canvas = document.createElement('canvas');
        canvas.width = width; canvas.height = height;
        canvas.getContext('2d').drawImage(img, 0, 0, width, height);
        const dataUrl = canvas.toDataURL('image/jpeg', 0.85);
        this.els.profileAvatarImg.src = dataUrl;
        this.els.profileAvatarImg.style.display = 'block';
        this.saveProfileMeta();
        this.showToast('Аватар обновлён');
      };
      img.onerror = () => this.showToast('Не удалось прочитать изображение', 'error');
      img.src = src;
    };
    reader.readAsDataURL(file);
    ev.target.value = '';
  },

  initIntersectionObserver() {
    this._lastUserInput = 0;
    const markUser = () => { this._lastUserInput = Date.now(); };
    ['wheel', 'touchmove', 'mousedown', 'pointerdown', 'keydown'].forEach(t => window.addEventListener(t, markUser, { passive: true }));
    this.observer = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting && !this.state.loading && !this.state.viewerOpen && this.state.hasMore) this.loadMore();
    }, { root: _('main'), rootMargin: '2000px 0px' });
    this.observer.observe(this.els.sentinel);
    const mainEl = _('main');
    if (mainEl) {
      mainEl.addEventListener('scroll', () => {
        clearTimeout(this._scrollLoadTimer);
        this._scrollLoadTimer = setTimeout(() => this.maybeLoadMore(), 120);
        clearTimeout(this._scrollSaveTimer);
        this._scrollSaveTimer = setTimeout(() => {
          try {
            if (this.state.posts.length) sessionStorage.setItem('briefly_scroll_top', String(mainEl.scrollTop));
          } catch {}
        }, 250);
      }, { passive: true });
    }
    window.addEventListener('resize', () => {
      clearTimeout(this._rsTimer);
      this._rsTimer = setTimeout(() => this.rebuildMasonry(), 200);
    });
  },

  hasRecentInput() {
    return Date.now() - this._lastUserInput < 4000;
  },

  panelSide() {
    const grid = document.querySelector('.posts-grid');
    if (!grid) return 0;
    const cr = grid.getBoundingClientRect().right;
    return Math.max(0, window.innerWidth - cr);
  },

  defaultPanelWidth() {
    const side = this.panelSide();
    const v = Math.round(side * 0.6);
    return Math.min(Math.max(v, 320), Math.max(320, Math.round(side * 0.92)));
  },

  clampPanelWidth(w) {
    const side = this.panelSide();
    const lo = 280, hi = Math.max(280, Math.round(side * 0.92));
    return Math.min(Math.max(w, lo), hi);
  },

  applyPanelWidth() {
    const w = this.clampPanelWidth(this._panelWidth || this.defaultPanelWidth());
    this.els.profilePanel.style.width = w + 'px';
    this.els.settingsPanel.style.width = w + 'px';
  },

  _syncPanels() {
    if (!this.els.panelBackdrop) return;
    const anyOpen = this.state.profileOpen || this.state.settingsOpen;
    this.els.panelBackdrop.classList.toggle('active', anyOpen);
    document.getElementById('app').classList.toggle('panel-open', anyOpen);
  },

  savePanelWidth(w) {
    this._panelWidth = w;
    try { localStorage.setItem('briefly_panel_width', String(w)); } catch {}
  },

  bindPanelResize() {
    ['profilePanel', 'settingsPanel'].forEach(k => {
      const panel = this.els[k];
      const handle = panel.querySelector('.panel-resize');
      if (!handle) return;
      handle.addEventListener('pointerdown', (e) => {
        if (window.innerWidth <= 640) return;
        e.preventDefault();
        const startX = e.clientX;
        const startW = panel.getBoundingClientRect().width;
        panel.classList.add('resizing');
        const move = (ev) => {
          panel.style.width = this.clampPanelWidth(startW + (startX - ev.clientX)) + 'px';
        };
        const up = () => {
          panel.classList.remove('resizing');
          this.savePanelWidth(parseFloat(panel.style.width) || startW);
          this.applyPanelWidth();
          document.removeEventListener('pointermove', move);
          document.removeEventListener('pointerup', up);
        };
        document.addEventListener('pointermove', move);
        document.addEventListener('pointerup', up);
      });
    });
  },

  bindPanelSwipe() {
    const bind = (key, close) => {
      const panel = this.els[key];
      if (!panel) return;
      let sx = 0, sy = 0, swiping = false;
      panel.addEventListener('touchstart', (e) => {
        if (e.touches.length !== 1) return;
        sx = e.touches[0].clientX; sy = e.touches[0].clientY; swiping = false;
      }, { passive: true });
      panel.addEventListener('touchmove', (e) => {
        if (e.touches.length !== 1) return;
        const dx = e.touches[0].clientX - sx;
        const dy = e.touches[0].clientY - sy;
        if (!swiping && Math.abs(dx) > 24 && Math.abs(dx) > Math.abs(dy) * 1.4) {
          swiping = true;
          e.preventDefault();
        }
      }, { passive: false });
      panel.addEventListener('touchend', (e) => {
        if (!swiping) return;
        swiping = false;
        const dx = e.changedTouches.length ? e.changedTouches[0].clientX - sx : 0;
        if (dx < -80) close();
      }, { passive: true });
    };
    bind('profilePanel', () => { if (this.state.profileOpen) this.toggleProfile(); });
    bind('settingsPanel', () => { if (this.state.settingsOpen) this.toggleSettings(); });
  },

  startDlPoll() {
    let lastDone = 0;
    let hideTimer = null;
    const hideProgress = () => {
      this.els.dlProgress.classList.add('hidden');
    };
    const updateFromStatus = (d) => {
      const total = (d.queued || 0) + (d.active || 0) + (d.done || 0);
      if ((d.queued || 0) + (d.active || 0) > 0) {
        this.els.dlProgress.classList.remove('hidden');
        clearTimeout(hideTimer);
        const pct = total > 0 ? (d.done || 0) / total * 100 : 0;
        this.els.dlProgressBar.style.width = `${Math.min(pct, 100)}%`;
        this.els.dlProgressText.textContent = (d.active || 0) > 0 ? `${d.done || 0}/${total}` : `готово: ${d.done || 0}`;
        if ((d.done || 0) > lastDone && this.state.viewerOpen) {
          const p = this.state.posts[this.state.viewerIndex];
          if (p && (d.done_ids || []).includes(p.id)) this.els.viewerProgress.textContent = 'скачано';
        }
      } else if (total > 0) {
        this.els.dlProgress.classList.remove('hidden');
        this.els.dlProgressBar.style.width = '100%';
        this.els.dlProgressText.textContent = `готово: ${d.done || 0}`;
        if ((d.done || 0) > lastDone && this.state.viewerOpen) {
          const p = this.state.posts[this.state.viewerIndex];
          if (p && (d.done_ids || []).includes(p.id)) this.els.viewerProgress.textContent = 'скачано';
        }
        clearTimeout(hideTimer);
        hideTimer = setTimeout(hideProgress, 3000);
      }
      lastDone = d ? (d.done || 0) : 0;
    };
    const applyResult = (r) => {
      if (r.duplicate_of) {
        this.state.downloadQueue.delete(r.post_id);
        this.state.downloading.delete(r.post_id);
        this.showToast(`Пост ${r.post_id} — дубликат #${r.duplicate_of}, файл уже скачан`);
        return;
      }
      if (r.success) {
        const p = this.state.posts.find(x => x.id === r.post_id);
        if (p) {
          p.downloaded = true;
          const card = this.getCardByIndex(this.state.posts.indexOf(p));
          if (card) card.classList.add('downloaded');
        }
      } else {
        this.state.downloadQueue.delete(r.post_id);
        this.state.downloading.delete(r.post_id);
        this.showToast(`Ошибка скачивания поста ${r.post_id}: ${r.error || 'неизвестная'}`, 'error');
      }
    };
    const m = document.querySelector('meta[name="briefly-token"]');
    const tok = m ? (m.getAttribute('content') || '') : '';
    let url = '/api/events';
    if (tok) url += `?token=${encodeURIComponent(tok)}`;
    const es = new EventSource(url);
    es.addEventListener('event', (ev) => {
      let d;
      try { d = JSON.parse(ev.data); } catch { return; }
      if (d.type === 'status') updateFromStatus(d);
      else if (d.type === 'result') applyResult(d);
      else if (d.type === 'comment' && this._onCommentEvent) this._onCommentEvent(d.post_id);
    });
    es.onerror = () => {  };
    this._sse = es;
  },

  async pauseDownloads() {
    await API.post('/download/pause');
    this.els.dlPause.classList.add('hidden');
    this.els.dlResume.classList.remove('hidden');
    this.showToast('Загрузки приостановлены');
  },

  async resumeDownloads() {
    await API.post('/download/resume');
    this.els.dlResume.classList.add('hidden');
    this.els.dlPause.classList.remove('hidden');
    this.showToast('Загрузки возобновлены');
  },

  async showQueue() {
    if (this.state.viewerOpen) this.closeViewer();
    try {
      const d = await API.get('/download/queue');
      const active = d.active || [];
      const queue = d.queue || [];
      let html = '';
      if (active.length > 0) {
        html += `<div class="queue-active-info">Активны: ${active.join(', ')}</div>`;
      }
      if (queue.length === 0) {
        html += '<div class="queue-empty">Очередь пуста</div>';
      } else {
        html += queue.map(j =>
          `<div class="queue-item">
            <span class="queue-item-id">#${j.post_id}</span>
            <span class="queue-item-ext">${j.file_type || ''}</span>
            <div class="queue-item-actions">
              <button onclick="App.queueMoveUp(${j.post_id})" title="Вверх">${icon('chevronUp', 13)}</button>
              <button onclick="App.queueMoveDown(${j.post_id})" title="Вниз">${icon('chevronDown', 13)}</button>
              <button onclick="App.queueCancel(${j.post_id})" title="Отменить" style="color:var(--error)">${icon('x', 13)}</button>
            </div>
          </div>`
        ).join('');
      }
      this.els.queueBody.innerHTML = html;
      this.els.queueModal.classList.remove('hidden');
    } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  },

  toggleViewerMobile() {
    const el = this.els.viewerMobileActions;
    if (!el) return;
    el.classList.toggle('collapsed');
    try { localStorage.setItem('briefly_viewer_panel', el.classList.contains('collapsed') ? '1' : '0'); } catch (_) {}
  },

  hideQueue() { this.els.queueModal.classList.add('hidden'); },

  async queueCancel(id) {
    await API.post(`/download/cancel/${id}`);
    this.showToast(`Загрузка #${id} отменена`);
    this.showQueue();
  },

  async queueMoveUp(id) {
    await API.post(`/download/move-up/${id}`);
    this.showQueue();
  },

  async queueMoveDown(id) {
    await API.post(`/download/move-down/${id}`);
    this.showQueue();
  },

  cycleGridDensity() {
    const order = ['auto', '2', '3', '4', '5', '6'];
    const cur = this.state.gridCols === null ? 'auto' : String(this.state.gridCols);
    const next = order[(order.indexOf(cur) + 1) % order.length];
    this.setGridSetting(next);
    this.showToast(`Сетка: ${next === 'auto' ? 'авто' : next + ' в ряд'}`);
  },

  async recommendFeed() {
    if (this.state.recommendActive) { this.exitRecommend(); return; }
    API.invalidate('/profile');
    if (!this.state.profileOpen && !this._profileFresh) {
      try { await this.loadProfile(); } catch (_) {}
    }
    const liked = (this.state.profile && this.state.profile.liked_posts) || [];
    if (liked.length < 5) {
      this.showToast(`Для рекомендаций нужно минимум 5 лайков — осталось ${5 - liked.length}`, 'error');
      return;
    }
    this._recViewed = this._recViewed || this._recViewedLoad();
    if (this.state.viewerOpen) this.closeViewer();
    this.state.recommendActive = true;
    this.renderModeBar();
    this.loadPosts(true, null, true);
    this.showToast('Собираю рекомендации по вашим лайкам...');
  },

  exitRecommend() {
    if (!this.state.recommendActive) return;
    this.state.recommendActive = false;
    this.renderModeBar();
    this.loadPosts(true, null, true);
    this.showToast('Вернулся к вашему запросу');
  },

  refreshRecommend(silent = false) {
    if (!this.state.recommendActive) return;
    this.loadPosts(true, null, true);
    if (!silent) this.showToast('Подборка обновлена');
  },

  _scheduleRecommendRefresh() {
    clearTimeout(this._recRefreshTimer);
    this._recRefreshTimer = setTimeout(() => {
      this._recRefreshTimer = null;
      if (this.state.recommendActive && !this.state.loading) this.refreshRecommend(true);
    }, 1200);
  },

  _recViewedKey: 'briefly_rec_viewed',

  _recViewedLoad() {
    try {
      const arr = JSON.parse(localStorage.getItem(this._recViewedKey) || '[]');
      return Array.isArray(arr) ? arr.filter(Number.isFinite).slice(0, 500) : [];
    } catch { return []; }
  },

  _recViewedSave() {
    try {
      localStorage.setItem(this._recViewedKey, JSON.stringify(this._recViewed.slice(-500)));
    } catch {}
  },

  _recMarkViewed(postId) {
    if (!postId) return;
    if (!this._recViewed) this._recViewed = this._recViewedLoad();
    if (!this._recViewed.includes(postId)) {
      this._recViewed.push(postId);
      this._recViewedSave();
    }
  },

  async _recSendDislike(postId) {
    if (!this.state.recommendActive) return;
    const post = this.state.posts.find(p => p.id === postId);
    if (!post || !post.tags) return;
    try {
      await API.post('/recommend/dislike', { tags: post.tags.split(' ').slice(0, 40) });
      this._scheduleRecommendRefresh();
    } catch (_) {}
  },

  async openPresetsSubmenu() {
    const menu = this.els.headerMenu;
    if (!this._headerMenuHTML) this._headerMenuHTML = menu.innerHTML;
    if (!this.state.profileOpen && !this._profileFresh) {
      try { await this.loadProfile(); } catch (_) {}
    }
    const presets = ((this.state.profile && this.state.profile.presets) || [])
      .filter(p => (p.kind || 'query') === 'query');
    let html = `<button class="header-menu-item" data-action="presets-back">${icon('chevronLeft', 16)} Назад</button>`;
    if (!presets.length) {
      html += `<div class="header-menu-empty">Пресетов поиска пока нет — сохраните в профиле</div>`;
    } else {
      presets.forEach(p => {
        html += `<button class="header-menu-item" data-action="preset" data-query="${esc(p.query || '')}">${esc(p.name || 'Без названия')}</button>`;
      });
    }
    menu.innerHTML = html;
  },

  closePresetsSubmenu() {
    this.els.headerMenu.innerHTML = this._headerMenuHTML || '';
  },
};

// Доступ из консоли браузера; в Node (тесты) window не существует.
if (typeof window !== 'undefined') (/** @type {any} */ (window)).App = App;
