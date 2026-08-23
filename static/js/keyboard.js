App.onKeydown = function (e) {
  const { viewerOpen, settingsOpen, profileOpen, posts } = this.state;
  const isInput = e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA';
  const code = e.code;

  // Ctrl/Cmd+K — фокус поиска из любого места (в т.ч. из полей ввода).
  if ((e.ctrlKey || e.metaKey) && !e.altKey && code === 'KeyK') {
    e.preventDefault();
    this.els.searchInput.focus();
    this.els.searchInput.select();
    return;
  }

  if (e.ctrlKey || e.metaKey || e.altKey) return;

  if (this.els.confirmModal && !this.els.confirmModal.classList.contains('hidden')) return;

  if (e.key === '?' && !isInput && !viewerOpen) { e.preventDefault(); this.toggleHelp(); return; }

  if (e.key === 'Escape') {
    if (this.els.helpModal && !this.els.helpModal.classList.contains('hidden')) { this.hideHelp(); return; }
    if (viewerOpen && this.els.viewerContent.classList.contains('fullscreen')) { this.toggleFullscreen(); return; }
    if (viewerOpen) { this.closeViewer(); return; }
    if (profileOpen) { this.toggleProfile(); return; }
    if (settingsOpen) { this.toggleSettings(); return; }
    if (this.els.statsModal && !this.els.statsModal.classList.contains('hidden')) { this.hideStats(); return; }
    if (this.els.queueModal && !this.els.queueModal.classList.contains('hidden')) { this.hideQueue(); return; }
    if (this.els.suggestions && this.els.suggestions.classList.contains('active')) { this.els.suggestions.classList.remove('active'); return; }
    if (this.els.historyDropdown && this.els.historyDropdown.classList.contains('active')) { this.hideHistory(); return; }
  }

  if (code === 'KeyL' && !e.ctrlKey && !e.metaKey && !viewerOpen && !settingsOpen && !isInput) { e.preventDefault(); this.toggleLocal(); return; }
  if (code === 'KeyF' && viewerOpen) { e.preventDefault(); this.toggleFullscreen(); return; }

  if (viewerOpen) {
    const img = this.els.viewerContent.querySelector('img');
    const isZoomed = img && this._zoomActive;
    // Для видео стрелки ←/→ перематывают (Shift+←/→ листают посты),
    // ↑/↓ меняют громкость, M — звук, Space — пауза вместо слайдшоу.
    const vid = !isZoomed ? this.els.viewerContent.querySelector('video') : null;
    const isVid = vid && vid.tagName === 'VIDEO';
    if ((e.key === 'ArrowUp' || code === 'KeyW') && isZoomed) { e.preventDefault(); this.startPan(0, -1); }
    else if ((e.key === 'ArrowDown' || code === 'KeyS') && isZoomed) { e.preventDefault(); this.startPan(0, 1); }
    else if ((e.key === 'ArrowLeft' || code === 'KeyA') && isZoomed) { e.preventDefault(); this.startPan(-1, 0); }
    else if ((e.key === 'ArrowRight' || code === 'KeyD') && isZoomed) { e.preventDefault(); this.startPan(1, 0); }
    else if (e.key === 'Home' && isZoomed) { e.preventDefault(); this.zoomToEdge(1, 1); }
    else if (e.key === 'End' && isZoomed) { e.preventDefault(); this.zoomToEdge(1, 0); }
    else if (e.key === 'PageUp' && isZoomed) { e.preventDefault(); this.panPage(-1); }
    else if (e.key === 'PageDown' && isZoomed) { e.preventDefault(); this.panPage(1); }
    else if (e.key === 'ArrowLeft' && isVid && !e.shiftKey) { e.preventDefault(); this.videoSeekBy(-5); }
    else if (e.key === 'ArrowRight' && isVid && !e.shiftKey) { e.preventDefault(); this.videoSeekBy(5); }
    else if (e.key === 'ArrowLeft' || code === 'KeyA') { e.preventDefault(); this.navigateViewer(-1); }
    else if (e.key === 'ArrowRight' || code === 'KeyD') { e.preventDefault(); this.navigateViewer(1); }
    else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && isVid) { e.preventDefault(); this.videoChangeVolume(e.key === 'ArrowUp' ? 0.05 : -0.05); }
    else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); }
    else if (code === 'KeyX') { e.preventDefault(); this.downloadCurrent(); }
    else if (code === 'Space') {
      e.preventDefault();
      // Пробел при видео — пауза/плей; автолистание ленты не трогаем,
      // чтобы слайдшоу не конфликтовало с паузой.
      if (isVid) this.toggleMediaPlay();
      else if (this.state.slideshowActive) this.stopSlideshow();
      else this.startSlideshow();
    }
    else if (code === 'KeyM' && isVid) { e.preventDefault(); this.toggleVideoMute(); }
    else if (code === 'Equal' || code === 'KeyQ') { e.preventDefault(); this.toggleLikeCurrent(); }
    else if (code === 'Minus' || code === 'KeyE') { e.preventDefault(); this.toggleHideCurrent(); }
    else if (code === 'KeyZ') { e.preventDefault(); this.toggleViewerZoom(); }
    else if (code === 'KeyR') { e.preventDefault(); this.toggleInvert(); }
    return;
  }

  if (!viewerOpen && !settingsOpen && !isInput && posts.length > 0) {
    const c = this.getColumnCount();
    if (e.key === 'ArrowDown' || code === 'KeyS') { e.preventDefault(); this.navGeom(0, 1); }
    else if (e.key === 'ArrowUp' || code === 'KeyW') { e.preventDefault(); this.navGeom(0, -1); }
    else if (e.key === 'ArrowRight' || code === 'KeyD') { e.preventDefault(); this.navGeom(1, 0); }
    else if (e.key === 'ArrowLeft' || code === 'KeyA') { e.preventDefault(); this.navGeom(-1, 0); }
    else if (e.key === 'Enter') { e.preventDefault(); this.openViewer(this.state.focusedIndex); }
    else if (e.key === 'PageDown') { e.preventDefault(); this.jumpTo(this.state.focusedIndex + c * 3, 1, posts.length); }
    else if (e.key === 'PageUp') { e.preventDefault(); this.jumpTo(this.state.focusedIndex - c * 3, -1, posts.length); }
    else if (e.key === 'Home') { e.preventDefault(); this.jumpTo(0, 1, posts.length); }
    else if (e.key === 'End') { e.preventDefault(); this.jumpTo(posts.length - 1, -1, posts.length); }
    else if (code === 'KeyX') { e.preventDefault(); const p = posts[this.state.focusedIndex]; if (p) this.downloadPost(p); }
    else if (code === 'KeyQ' || code === 'Equal') { e.preventDefault(); const idx = this.state.hoveredIndex >= 0 ? this.state.hoveredIndex : this.state.focusedIndex; const p = posts[idx]; if (p) this.feedToggleLike(p); }
    else if (code === 'KeyE' || code === 'Minus') { e.preventDefault(); const idx = this.state.hoveredIndex >= 0 ? this.state.hoveredIndex : this.state.focusedIndex; const p = posts[idx]; if (p) this.feedToggleHide(p); }
  }

  if (code === 'Slash' && !viewerOpen && !settingsOpen && !isInput) { e.preventDefault(); this.els.searchInput.focus(); }
};

App.onKeyup = function (e) {
  const code = e.code;
  if (code === 'ArrowUp' || code === 'KeyW') this.stopPan(App.DIR.UP);
  else if (code === 'ArrowDown' || code === 'KeyS') this.stopPan(App.DIR.DOWN);
  else if (code === 'ArrowLeft' || code === 'KeyA') this.stopPan(App.DIR.LEFT);
  else if (code === 'ArrowRight' || code === 'KeyD') this.stopPan(App.DIR.RIGHT);
};

App.toggleMediaPlay = function () {
  const m = this.els && this.els.viewerContent ? this.els.viewerContent.querySelector('video') : null;
  if (!m) return;
  if (m.paused) { const p = m.play(); if (p && p.catch) p.catch(() => {}); }
  else m.pause();
};
// Кол-во колонок — единый источник истины с масонри-гридом (учитывает
// ручную настройку state.gridCols, как и masonryColumnCount в feed.js).
App.getColumnCount = function () { return this.masonryColumnCount(); };

// Геометрия карточек для навигации стрелками. Центры кэшируются на 300 мс:
// скролл сдвигает все карточки одинаково и на относительные расстояния
// не влияет, а пересчитывать getBoundingClientRect по всем карточкам на
// каждое нажатие клавиши вызывало layout thrash.
App._cardGeometry = function () {
  const now = performance.now();
  if (!this._geomCache || now - this._geomCache.ts > 300) {
    const items = [];
    document.querySelectorAll('.post-card').forEach(card => {
      const r = card.getBoundingClientRect();
      if (!r.width && !r.height) return;
      items.push({ idx: parseInt(card.dataset.index, 10), cx: r.left + r.width / 2, cy: r.top + r.height / 2 });
    });
    this._geomCache = { ts: now, items };
  }
  return this._geomCache.items;
};

App.navGeom = function (dx, dy) {
  const cur = this.getCardByIndex(this.state.focusedIndex);
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
  } else if (dx !== 0 && this.state.posts.length > 1) {
    this.jumpTo(this.state.focusedIndex + dx, dx, this.state.posts.length);
  }
};

App.jumpTo = function (from, dir, postsLen) {
  let idx = from;
  while (idx >= 0 && idx < postsLen) {
    if (this.getCardByIndex(idx)) { this.state.focusedIndex = idx; this.focusCard(); this.scrollToFocused(); return; }
    idx += dir;
  }
};

App.focusCard = function () {
  document.querySelectorAll('.post-card').forEach((c) => {
    c.classList.toggle('focused', parseInt(c.dataset.index, 10) === this.state.focusedIndex);
  });
  if (this.state.focusedIndex >= 0) this.ensureCardVisible(this.state.focusedIndex);
};

App.ensureCardVisible = function (idx) {
  const c = this.getCardByIndex(idx);
  if (c) c.scrollIntoView({ block: 'nearest' });
};

App.scrollToFocused = function () {
  const c = this.getCardByIndex(this.state.focusedIndex);
  if (c) c.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
};
