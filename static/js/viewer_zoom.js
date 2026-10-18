import { App } from './state.js';

App._ensureZoomState = function () {
  if (this._zoomScale == null) {
    let sc = 1, active = false;
    try {
      const raw = localStorage.getItem('better34_zoom') ?? localStorage.getItem('briefly_zoom');
      const s = JSON.parse(raw || 'null');
      if (s && typeof s.scale === 'number') { sc = s.scale; active = !!s.active; }
    } catch {}
    this._zoomScale = sc; this._zoomTx = 0; this._zoomTy = 0; this._zoomActive = active;
  }
};

App._maxZoom = function () { return 10; }; 

App._persistZoom = function () {
  try {
    const val = JSON.stringify({ active: this._zoomActive, scale: this._zoomScale || 1 });
    localStorage.setItem('better34_zoom', val);
    localStorage.setItem('briefly_zoom', val);
  } catch {}
};

App._cancelInertia = function () {
  if (this._inertiaRaf) { cancelAnimationFrame(this._inertiaRaf); this._inertiaRaf = null; }
};

App._smoothZoom = function () {
  this._zoomTransition = true;
  clearTimeout(this._zoomSmoothTimer);
  this._zoomSmoothTimer = setTimeout(() => { this._zoomTransition = false; }, 160);
};

App._zoomMetrics = function () {
  const vc = this.els.viewerContent;
  const W = vc.clientWidth || vc.parentElement.clientWidth || 1;
  const H = vc.clientHeight || vc.parentElement.clientHeight || 1;
  const img = this.els.viewerContent.querySelector('img');
  let nw = img ? (img.naturalWidth || 0) : 0;
  let nh = img ? (img.naturalHeight || 0) : 0;
  if (nw <= 0 || nh <= 0) { nw = W; nh = H; }
  const ar = nw / nh;
  let fw, fh;
  if (ar >= W / H) { fw = W; fh = W / ar; } else { fh = H; fw = H * ar; }
  return { W, H, fw, fh };
};

App._zoomMaxPan = function (s) {
  const { W, H, fw, fh } = this._zoomMetrics();
  return {
    maxTX: Math.max(0, (s * fw - W) / 2),
    maxTY: Math.max(0, (s * fh - H) / 2),
  };
};

App.applyZoomTransform = function () {
  this._ensureZoomState();
  const img = this.els.viewerContent.querySelector('img');
  if (!img) return;
  const { fw, fh } = this._zoomMetrics();
  const s = this._zoomActive ? (this._zoomScale || 1) : 1;
  const active = this._zoomActive && s > 1;
  const { maxTX, maxTY } = this._zoomMaxPan(s);
  if (!active) { this._zoomTx = 0; this._zoomTy = 0; }
  this._zoomTx = Math.min(maxTX, Math.max(-maxTX, this._zoomTx || 0));
  this._zoomTy = Math.min(maxTY, Math.max(-maxTY, this._zoomTy || 0));
  img.style.objectFit = 'contain';
  img.style.objectPosition = 'center';
  img.style.transformOrigin = 'center center';
  img.style.width = fw + 'px';
  img.style.height = fh + 'px';
  img.style.transition = this._zoomTransition ? 'transform 0.15s ease-out' : 'none';
  img.style.transform = active ? `translate(${this._zoomTx}px, ${this._zoomTy}px) scale(${s})` : 'none';
  img.style.cursor = active ? 'grab' : 'zoom-in';
  this._updateZoomHud(s, active);
  this._updateZoomLabel(s);
};

App._updateZoomHud = function (s, active) {
  if (!this.els.zThumbX || !this.els.zThumbY || !this.els.zoomHud) return;
  s = s || (this._zoomScale || 1);
  const { W, H, fw, fh } = this._zoomMetrics();
  const { maxTX, maxTY } = this._zoomMaxPan(s);
  let hx = 0.5, hy = 0.5;
  if (maxTX > 0) hx = 0.5 - (this._zoomTx || 0) / (2 * maxTX);
  if (maxTY > 0) hy = 0.5 - (this._zoomTy || 0) / (2 * maxTY);
  const pxW = Math.min(100, (W / (s * fw)) * 100);
  const pxH = Math.min(100, (H / (s * fh)) * 100);
  this.els.zThumbX.style.width = pxW + '%';
  this.els.zThumbX.style.left = Math.max(0, Math.min(100 - pxW, hx * 100 - pxW / 2)) + '%';
  this.els.zThumbY.style.height = pxH + '%';
  this.els.zThumbY.style.top = Math.max(0, Math.min(100 - pxH, hy * 100 - pxH / 2)) + '%';
  this.els.zoomHud.classList.toggle('on', !!active);
};

App._updateZoomLabel = function (s) {
  if (this.els.zoomLabel) this.els.zoomLabel.textContent = Math.round((s || this._zoomScale || 1) * 100) + '%';
};

App.panBy = function (dxPx, dyPx) {
  if (!this._zoomActive) return;
  this._ensureZoomState();
  const s = this._zoomScale || 1;
  const { maxTX, maxTY } = this._zoomMaxPan(s);
  this._zoomTx = Math.min(maxTX, Math.max(-maxTX, (this._zoomTx || 0) + dxPx));
  this._zoomTy = Math.min(maxTY, Math.max(-maxTY, (this._zoomTy || 0) + dyPx));
  this.applyZoomTransform();
};

App.zoomBy = function (factor, cx, cy) {
  this._ensureZoomState();
  if (!this._zoomActive) return;
  const { W, H } = this._zoomMetrics();
  const s = this._zoomScale;
  const ns = Math.min(this._maxZoom(), Math.max(1, s * factor));
  if (ns === s) return;
  const cxRel = (cx != null && isFinite(cx) ? cx : W / 2) - W / 2;
  const cyRel = (cy != null && isFinite(cy) ? cy : H / 2) - H / 2;
  this._zoomTx = (this._zoomTx || 0) * (ns / s) + cxRel * (1 - ns / s);
  this._zoomTy = (this._zoomTy || 0) * (ns / s) + cyRel * (1 - ns / s);
  this._zoomScale = ns;
  this._smoothZoom();
  this.applyZoomTransform();
  this._persistZoom();
};

App.zoomToEdge = function (axis, pos) {
  this._ensureZoomState();
  if (!this._zoomActive) return;
  const { maxTX, maxTY } = this._zoomMaxPan(this._zoomScale || 1);
  const val = pos ? (axis ? maxTY : maxTX) : (axis ? -maxTY : -maxTX);
  if (axis) this._zoomTy = val; else this._zoomTx = val;
  this.applyZoomTransform();
};

App.panPage = function (dir) {
  this._ensureZoomState();
  if (!this._zoomActive) return;
  const { H } = this._zoomMetrics();
  this.panBy(0, -dir * H * 0.6);
};

App._startPanInertia = function (vx, vy) {
  this._cancelInertia();
  const dec = 0.9;
  const tick = () => {
    if (Math.abs(vx) < 0.4 && Math.abs(vy) < 0.4) { this._inertiaRaf = null; return; }
    if (!this._zoomActive) { this._inertiaRaf = null; return; }
    this.panBy(vx, vy);
    vx *= dec; vy *= dec;
    this._inertiaRaf = requestAnimationFrame(tick);
  };
  this._inertiaRaf = requestAnimationFrame(tick);
};

App._panKeys = null;

App.DIR = { UP: 'up', DOWN: 'down', LEFT: 'left', RIGHT: 'right' };

App._setPanDirection = function (dir, pressed) {
  if (!this._panKeys) this._panKeys = new Set();
  if (pressed) this._panKeys.add(dir); else this._panKeys.delete(dir);
  this._updatePanVelocity();
};

App._updatePanVelocity = function () {
  const k = this._panKeys || new Set();
  if (!this._panVel) this._panVel = { x: 0, y: 0 };
  this._panVel.x = (k.has(App.DIR.RIGHT) ? 1 : 0) - (k.has(App.DIR.LEFT) ? 1 : 0);
  this._panVel.y = (k.has(App.DIR.DOWN) ? 1 : 0) - (k.has(App.DIR.UP) ? 1 : 0);
  if (this._panVel.x || this._panVel.y) {
    if (!this._panRaf) this._runPanLoop();
  } else if (this._panRaf) {
    cancelAnimationFrame(this._panRaf);
    this._panRaf = null;
  }
};

App._runPanLoop = function () {
  const tick = () => {
    if (!this._zoomActive || !this.state.viewerOpen) { this._panRaf = null; return; }
    const vx = this._panVel ? this._panVel.x : 0;
    const vy = this._panVel ? this._panVel.y : 0;
    if (!vx && !vy) { this._panRaf = null; return; }
    const { W, H } = this._zoomMetrics();
    const px = -vx * W * 0.01; 
    const py = -vy * H * 0.01;
    if (px || py) this.panBy(px, py);
    this._panRaf = requestAnimationFrame(tick);
  };
  this._panRaf = requestAnimationFrame(tick);
};

App.startPan = function (dx, dy) {
  if (!this._zoomActive) return;
  this._cancelInertia();
  if (dx) this._setPanDirection(dx > 0 ? App.DIR.RIGHT : App.DIR.LEFT, true);
  if (dy) this._setPanDirection(dy > 0 ? App.DIR.DOWN : App.DIR.UP, true);
};

App.stopPan = function (dir) {
  this._setPanDirection(dir, false);
};

App._cancelPan = function () {
  if (this._panRaf) { cancelAnimationFrame(this._panRaf); this._panRaf = null; }
  if (this._panKeys) this._panKeys.clear();
  if (this._panVel) { this._panVel.x = 0; this._panVel.y = 0; }
};

App._resetFullscreen = function () {
  this.els.viewerContent.classList.remove('fullscreen');
  this.els.viewerHead.style.display = '';
  this.els.viewerFoot.style.display = '';
  this.els.prevBtn.style.display = '';
  this.els.nextBtn.style.display = '';
};

App.toggleFullscreen = function () {
  const vc = this.els.viewerContent;
  if (!vc.classList.contains('fullscreen')) {
    vc.classList.add('fullscreen');
    this.els.viewerHead.style.display = 'none';
    this.els.viewerFoot.style.display = 'none';
    this.els.prevBtn.style.display = 'none';
    this.els.nextBtn.style.display = 'none';
    // Шапка и подвал (теги, «Похожие») скрыты — отменяем в-полёте запросы
    // похожих и счётчиков тегов, чтобы не расходовать лимиты API впустую.
    this._relToken = (this._relToken || 0) + 1;
    if (this._relAbort) { this._relAbort.abort(); this._relAbort = null; }
    if (this._countAbort) { this._countAbort.abort(); this._countAbort = null; }
  } else {
    vc.classList.remove('fullscreen');
    this.els.viewerHead.style.display = '';
    this.els.viewerFoot.style.display = '';
    this.updateNavButtons();
    // Восстанавливаем скрытое в полном экране: теги (при необходимости
    // догрузят счётчики) и похожие для текущего поста, если ещё не загружены.
    this._renderViewerTags();
    const post = this.state.posts[this.state.viewerIndex];
    if (post && post.id && this._relLoadedFor !== post.id) this.scheduleRelated(post);
  }
  this.applyZoomTransform();
};

App.toggleViewerZoom = function () {
  this._ensureZoomState();
  this._cancelInertia();
  if (this._zoomActive) {
    this._zoomActive = false;
  } else {
    this._zoomActive = true;
    this._zoomScale = Math.max(this._zoomScale || 1, 2);
    this._zoomTx = 0; this._zoomTy = 0; 
  }
  this._smoothZoom();
  this.applyZoomTransform();
  this._persistZoom();
};

App._bindZoomControls = function () {
  const bind = (id, fn) => { const el = this.els[id]; if (el) el.addEventListener('click', fn); };
  bind('zoomIn', () => { this._ensureZoomState(); if (!this._zoomActive) { this._zoomActive = true; } this.zoomBy(1.25); });
  bind('zoomOut', () => { if (this._zoomActive) this.zoomBy(1 / 1.25); });
  bind('zoomFit', () => { if (this._zoomActive) this.toggleViewerZoom(); else this.applyZoomTransform(); });
};

// Ctrl/Cmd+ЛКМ (и средняя кнопка) по медиа во вьювере — открыть файл в новой
// вкладке, как обычную ссылку. Обычный клик не перехватываем: он управляет
// HUD/зумом, а Ctrl+ЛКМ в браузере всегда означает «открыть в новой вкладке».
App._bindViewerNewTab = function () {
  const mediaOf = (e) => (e.target && e.target.closest ? e.target.closest('img, video') : null);
  const openFile = (e) => {
    const post = this.state.posts[this.state.viewerIndex];
    if (!post) return;
    e.preventDefault();
    this.openInNewTab(this._viewerMediaPlan(post).url);
  };
  this.els.viewerContent.addEventListener('click', (e) => {
    if (!this.isOpenInNewTabClick(e) || !mediaOf(e)) return;
    openFile(e);
  });
  this.els.viewerContent.addEventListener('auxclick', (e) => {
    if (e.button !== 1 || !mediaOf(e)) return;
    openFile(e);
  });
};
