// state_shortcuts.js — обработка горячих клавиш клавиатуры (глобальные, вьювер, навигация по ленте).
import { App } from './state.js';

export const ShortcutMethods = {
  DIR: { UP: 'up', DOWN: 'down', LEFT: 'left', RIGHT: 'right' },

  onKeydown(e) {
    const { viewerOpen, settingsOpen, profileOpen, posts } = this.state;
    const isInput = e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.isContentEditable;
    const code = e.code;

    // Ctrl/Cmd+K — фокус поиска из любого места (в т.ч. из полей ввода).
    if ((e.ctrlKey || e.metaKey) && !e.altKey && code === 'KeyK') {
      e.preventDefault();
      if (this.els.searchInput) {
        this.els.searchInput.focus();
        this.els.searchInput.select();
      }
      return;
    }

    if (e.ctrlKey || e.metaKey || e.altKey) return;

    if (this.els.confirmModal && !this.els.confirmModal.classList.contains('hidden')) return;

    // Турнир «выбери лучшее» занимает весь экран: пока он открыт, клавиши
    // обслуживают только игру, иначе стрелки листали бы ленту под оверлеем.
    // Исключение — просмотр участника: вьювер открыт ПОВЕРХ турнира, и там клавиши
    // принадлежат посту (Esc закрывает просмотр, стрелки листают), а не игре.
    if (this.state.tournamentOpen && !(this._tournament && this._tournament.viewerOpen)) {
      if (typeof this.tournamentKey === 'function') this.tournamentKey(e);
      return;
    }

    if (e.key === '?' && !isInput && !viewerOpen) {
      e.preventDefault();
      if (typeof this.toggleHelp === 'function') this.toggleHelp();
      return;
    }

    if (e.key === 'Escape') {
      if (this.els.helpModal && !this.els.helpModal.classList.contains('hidden')) {
        if (typeof this.hideHelp === 'function') this.hideHelp();
        return;
      }
      if (viewerOpen && this.els.viewerContent && this.els.viewerContent.classList.contains('fullscreen')) {
        if (typeof this.toggleFullscreen === 'function') this.toggleFullscreen();
        return;
      }
      if (viewerOpen) {
        if (typeof this.closeViewer === 'function') this.closeViewer();
        return;
      }
      // Страница профиля друга полноэкранная: выходим из неё до профиля и
      // настроек, иначе Esc закрывал бы то, что под ней, а её оставлял открытой.
      if (this._friendProfile) {
        if (typeof this.closeFriendProfile === 'function') this.closeFriendProfile();
        return;
      }
      if (profileOpen) {
        if (typeof this.toggleProfile === 'function') this.toggleProfile();
        return;
      }
      if (settingsOpen) {
        if (typeof this.toggleSettings === 'function') this.toggleSettings();
        return;
      }
      if (this.els.statsModal && !this.els.statsModal.classList.contains('hidden')) {
        if (typeof this.hideStats === 'function') this.hideStats();
        return;
      }
      if (this.els.queueModal && !this.els.queueModal.classList.contains('hidden')) {
        if (typeof this.hideQueue === 'function') this.hideQueue();
        return;
      }
      if (this.els.suggestions && this.els.suggestions.classList.contains('active')) {
        this.els.suggestions.classList.remove('active');
        return;
      }
      if (this.els.historyDropdown && this.els.historyDropdown.classList.contains('active')) {
        if (typeof this.hideHistory === 'function') this.hideHistory();
        return;
      }
    }

    if (code === 'KeyL' && !e.ctrlKey && !e.metaKey && !viewerOpen && !settingsOpen && !isInput) {
      e.preventDefault();
      if (typeof this.toggleLocal === 'function') this.toggleLocal();
      return;
    }
    if (code === 'KeyT' && !e.ctrlKey && !e.metaKey && !viewerOpen && !settingsOpen && !isInput) {
      e.preventDefault();
      if (typeof this.toggleTheme === 'function') this.toggleTheme();
      return;
    }
    if (code === 'KeyF' && viewerOpen) {
      e.preventDefault();
      if (typeof this.toggleFullscreen === 'function') this.toggleFullscreen();
      return;
    }

    if (viewerOpen) {
      // Не перехватываем клавиши, пока пользователь печатает в поле
      // (комментарии, скорость слайдшоу): q/e/x/wasd и т.д. должны вводиться.
      // Escape и Ctrl/Cmd+K обработаны выше и работают даже из поля ввода.
      if (isInput) return;
      const img = this.els.viewerContent ? this.els.viewerContent.querySelector('img') : null;
      const isZoomed = img && this._zoomActive;
      // Для видео стрелки ←/→ перематывают (Shift+←/→ листают посты),
      // ↑/↓ меняют громкость, M — звук, Space — пауза вместо слайдшоу.
      const vid = !isZoomed && this.els.viewerContent ? this.els.viewerContent.querySelector('video') : null;
      const isVid = vid && vid.tagName === 'VIDEO';
      if ((e.key === 'ArrowUp' || code === 'KeyW') && isZoomed) { e.preventDefault(); if (typeof this.startPan === 'function') this.startPan(0, -1); }
      else if ((e.key === 'ArrowDown' || code === 'KeyS') && isZoomed) { e.preventDefault(); if (typeof this.startPan === 'function') this.startPan(0, 1); }
      else if ((e.key === 'ArrowLeft' || code === 'KeyA') && isZoomed) { e.preventDefault(); if (typeof this.startPan === 'function') this.startPan(-1, 0); }
      else if ((e.key === 'ArrowRight' || code === 'KeyD') && isZoomed) { e.preventDefault(); if (typeof this.startPan === 'function') this.startPan(1, 0); }
      else if (e.key === 'Home' && isZoomed) { e.preventDefault(); if (typeof this.zoomToEdge === 'function') this.zoomToEdge(1, 1); }
      else if (e.key === 'End' && isZoomed) { e.preventDefault(); if (typeof this.zoomToEdge === 'function') this.zoomToEdge(1, 0); }
      else if (e.key === 'PageUp' && isZoomed) { e.preventDefault(); if (typeof this.panPage === 'function') this.panPage(-1); }
      else if (e.key === 'PageDown' && isZoomed) { e.preventDefault(); if (typeof this.panPage === 'function') this.panPage(1); }
      else if (e.key === 'ArrowLeft' && isVid && !e.shiftKey) { e.preventDefault(); if (typeof this.videoSeekBy === 'function') this.videoSeekBy(-5); }
      else if (e.key === 'ArrowRight' && isVid && !e.shiftKey) { e.preventDefault(); if (typeof this.videoSeekBy === 'function') this.videoSeekBy(5); }
      else if (e.key === 'ArrowLeft' || code === 'KeyA') { e.preventDefault(); if (typeof this.navigateViewer === 'function') this.navigateViewer(-1); }
      else if (e.key === 'ArrowRight' || code === 'KeyD') { e.preventDefault(); if (typeof this.navigateViewer === 'function') this.navigateViewer(1); }
      else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && isVid) { e.preventDefault(); if (typeof this.videoChangeVolume === 'function') this.videoChangeVolume(e.key === 'ArrowUp' ? 0.05 : -0.05); }
      else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); }
      else if (code === 'KeyX') { e.preventDefault(); if (typeof this.downloadCurrent === 'function') this.downloadCurrent(); }
      else if (code === 'Space') {
        e.preventDefault();
        // Пробел при видео — пауза/плей; автолистание ленты не трогаем,
        // чтобы слайдшоу не конфликтовало с паузой.
        if (isVid) this.toggleMediaPlay();
        else if (this.state.slideshowActive) {
          if (typeof this.stopSlideshow === 'function') this.stopSlideshow();
        } else {
          if (typeof this.startSlideshow === 'function') this.startSlideshow();
        }
      }
      else if (code === 'KeyM' && isVid) { e.preventDefault(); if (typeof this.toggleVideoMute === 'function') this.toggleVideoMute(); }
      else if (code === 'KeyP' && isVid) { e.preventDefault(); if (typeof this.togglePictureInPicture === 'function') this.togglePictureInPicture(); }
      else if (code === 'KeyC' && !isVid) { e.preventDefault(); if (typeof this.copyCurrentImage === 'function') this.copyCurrentImage(); }
      else if (code === 'Equal' || code === 'KeyQ') { e.preventDefault(); if (typeof this.toggleLikeCurrent === 'function') this.toggleLikeCurrent(); }
      else if (code === 'Minus' || code === 'KeyE') { e.preventDefault(); if (typeof this.toggleHideCurrent === 'function') this.toggleHideCurrent(); }
      else if (code === 'KeyZ') { e.preventDefault(); if (typeof this.toggleViewerZoom === 'function') this.toggleViewerZoom(); }
      else if (code === 'KeyR') { e.preventDefault(); if (typeof this.toggleInvert === 'function') this.toggleInvert(); }
      // S — поделиться. Отдельная буква, а не Ctrl+C: копирование ссылки
      // в буфер привязано к буферу обмена, из которого на телефоне ещё надо
      // куда-то вставлять вручную.
      else if (code === 'KeyS') { e.preventDefault(); if (typeof this.sharePost === 'function') this.sharePost(); }
      return;
    }

    if (!viewerOpen && !settingsOpen && !isInput && posts && posts.length > 0) {
      const c = this.getColumnCount();
      if (e.key === 'ArrowDown' || code === 'KeyS') { e.preventDefault(); this.navGeom(0, 1); }
      else if (e.key === 'ArrowUp' || code === 'KeyW') { e.preventDefault(); this.navGeom(0, -1); }
      else if (e.key === 'ArrowRight' || code === 'KeyD') { e.preventDefault(); this.navGeom(1, 0); }
      else if (e.key === 'ArrowLeft' || code === 'KeyA') { e.preventDefault(); this.navGeom(-1, 0); }
      else if (e.key === 'Enter') { e.preventDefault(); if (typeof this.openViewer === 'function') this.openViewer(this.state.focusedIndex); }
      else if (e.key === 'PageDown') { e.preventDefault(); this.jumpTo(this.state.focusedIndex + c * 3, 1, posts.length); }
      else if (e.key === 'PageUp') { e.preventDefault(); this.jumpTo(this.state.focusedIndex - c * 3, -1, posts.length); }
      else if (e.key === 'Home') { e.preventDefault(); this.jumpTo(0, 1, posts.length); }
      else if (e.key === 'End') { e.preventDefault(); this.jumpTo(posts.length - 1, -1, posts.length); }
      else if (code === 'KeyX') {
        e.preventDefault();
        const p = posts[this.state.focusedIndex];
        if (p && typeof this.downloadPost === 'function') this.downloadPost(p);
      }
      else if (code === 'KeyQ' || code === 'Equal') {
        e.preventDefault();
        const idx = this.state.hoveredIndex >= 0 ? this.state.hoveredIndex : this.state.focusedIndex;
        const p = posts[idx];
        if (p && typeof this.feedToggleLike === 'function') this.feedToggleLike(p);
      }
      else if (code === 'KeyE' || code === 'Minus') {
        e.preventDefault();
        const idx = this.state.hoveredIndex >= 0 ? this.state.hoveredIndex : this.state.focusedIndex;
        const p = posts[idx];
        if (p && typeof this.feedToggleHide === 'function') this.feedToggleHide(p);
      }
    }

    if (code === 'Slash' && !viewerOpen && !settingsOpen && !isInput) {
      e.preventDefault();
      if (this.els.searchInput) this.els.searchInput.focus();
    }
  },

  onKeyup(e) {
    const code = e.code;
    const dir = this.DIR || ShortcutMethods.DIR;
    if (typeof this.stopPan === 'function') {
      if (code === 'ArrowUp' || code === 'KeyW') this.stopPan(dir.UP);
      else if (code === 'ArrowDown' || code === 'KeyS') this.stopPan(dir.DOWN);
      else if (code === 'ArrowLeft' || code === 'KeyA') this.stopPan(dir.LEFT);
      else if (code === 'ArrowRight' || code === 'KeyD') this.stopPan(dir.RIGHT);
    }
  },

  toggleMediaPlay() {
    const m = this.els && this.els.viewerContent ? this.els.viewerContent.querySelector('video') : null;
    if (!m) return;
    if (m.paused) { const p = m.play(); if (p && p.catch) p.catch(() => {}); }
    else m.pause();
  },

  // Кол-во колонок — единый источник истины с масонри-гридом (учитывает
  // ручную настройку state.gridCols, как и masonryColumnCount в feed.js).
  getColumnCount() {
    return typeof this.masonryColumnCount === 'function' ? this.masonryColumnCount() : 3;
  },

  // Геометрия карточек для навигации стрелками. Центры кэшируются на 300 мс:
  // скролл сдвигает все карточки одинаково и на относительные расстояния
  // не влияет, а пересчитывать getBoundingClientRect по всем карточкам на
  // каждое нажатие клавиши вызывало layout thrash.
  _cardGeometry() {
    const now = typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now();
    if (!this._geomCache || now - this._geomCache.ts > 300) {
      const items = [];
      if (typeof document !== 'undefined' && typeof document.querySelectorAll === 'function') {
        document.querySelectorAll('.post-card').forEach(card => {
          const r = card.getBoundingClientRect();
          if (!r.width && !r.height) return;
          items.push({ idx: parseInt(/** @type {HTMLElement} */ (card).dataset.index || '0', 10), cx: r.left + r.width / 2, cy: r.top + r.height / 2 });
        });
      }
      this._geomCache = { ts: now, items };
    }
    return this._geomCache.items;
  },

  navGeom(dx, dy) {
    const cur = typeof this.getCardByIndex === 'function' ? this.getCardByIndex(this.state.focusedIndex) : null;
    if (!cur) return;
    const cr = cur.getBoundingClientRect();
    const cx = cr.left + cr.width / 2;
    const cy = cr.top + cr.height / 2;
    let bestIdx = -1;
    let bestScore = Infinity;
    for (const it of this._cardGeometry()) {
      const ddx = it.cx - cx;
      const ddy = it.cy - cy;
      if ((dx > 0 && ddx <= 2) || (dx < 0 && ddx >= -2) || (dy > 0 && ddy <= 2) || (dy < 0 && ddy >= -2)) continue;
      const score = Math.abs(ddx) * (dx ? 1 : 2) + Math.abs(ddy) * (dy ? 1 : 2);
      if (score < bestScore) { bestScore = score; bestIdx = it.idx; }
    }
    if (bestIdx >= 0) {
      this.state.focusedIndex = bestIdx;
      this.focusCard();
      this.scrollToFocused();
    } else if (dx !== 0 && this.state.posts && this.state.posts.length > 1) {
      this.jumpTo(this.state.focusedIndex + dx, dx, this.state.posts.length);
    }
  },

  jumpTo(from, dir, postsLen) {
    let idx = from;
    while (idx >= 0 && idx < postsLen) {
      if (typeof this.getCardByIndex === 'function' && this.getCardByIndex(idx)) {
        this.state.focusedIndex = idx;
        this.focusCard();
        this.scrollToFocused();
        return;
      }
      idx += dir;
    }
  },

  focusCard() {
    if (typeof document !== 'undefined' && typeof document.querySelectorAll === 'function') {
      document.querySelectorAll('.post-card').forEach((c) => {
        c.classList.toggle('focused', parseInt(/** @type {HTMLElement} */ (c).dataset.index || '0', 10) === this.state.focusedIndex);
      });
    }
    if (this.state.focusedIndex >= 0) this.ensureCardVisible(this.state.focusedIndex);
  },

  ensureCardVisible(idx) {
    const c = typeof this.getCardByIndex === 'function' ? this.getCardByIndex(idx) : null;
    if (c && typeof c.scrollIntoView === 'function') c.scrollIntoView({ block: 'nearest' });
  },

  scrollToFocused() {
    const c = typeof this.getCardByIndex === 'function' ? this.getCardByIndex(this.state.focusedIndex) : null;
    if (c && typeof c.scrollIntoView === 'function') c.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  },
};

export function extendShortcuts(targetApp) {
  if (!targetApp.DIR) targetApp.DIR = ShortcutMethods.DIR;
  Object.assign(targetApp, ShortcutMethods);
  return targetApp;
}

export { App };

export const onKeydown = ShortcutMethods.onKeydown;
export const onKeyup = ShortcutMethods.onKeyup;
export const toggleMediaPlay = ShortcutMethods.toggleMediaPlay;
export const getColumnCount = ShortcutMethods.getColumnCount;
export const navGeom = ShortcutMethods.navGeom;
export const jumpTo = ShortcutMethods.jumpTo;
export const focusCard = ShortcutMethods.focusCard;
export const ensureCardVisible = ShortcutMethods.ensureCardVisible;
export const scrollToFocused = ShortcutMethods.scrollToFocused;
