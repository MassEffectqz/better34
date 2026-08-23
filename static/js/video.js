import { App } from './state.js';
import { icon } from './utils.js';
// video.js — видео-часть вьюера: настройки (громкость/скорость), позиции
// просмотра, autoplay-fallback, подсказка звука, ошибки загрузки, PiP.
// Загружается после viewer.js (использует App из utils/state).

// ── Видео: настройки, позиция, autoplay, ошибки ─────────────────────────

App._videoPrefsKey = 'briefly_video_prefs';
App._videoPosKey = 'briefly_video_pos';
App.VIDEO_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2];
// Мьют — сессионная настройка: в localStorage не хранится. Иначе один
// fallback-мьют при заблокированном autoplay «отравлял» настройки и все
// последующие видео открывались беззвучными.
App._loadVideoPrefs = function () {
  if (App._videoPrefsCache) return App._videoPrefsCache;
  let p = {};
  try { p = JSON.parse(localStorage.getItem(App._videoPrefsKey) || '{}') || {}; } catch { /* noop */ }
  App._videoPrefsCache = {
    volume: typeof p.volume === 'number' ? Math.min(1, Math.max(0, p.volume)) : 1,
    muted: false,
    rate: App.VIDEO_RATES.indexOf(p.rate) >= 0 ? p.rate : 1,
  };
  // Миграция формата: выкидываем устаревший persisted-muted из хранилища.
  if (p.v !== 2) App._saveVideoPrefs();
  return App._videoPrefsCache;
};

// Персистим только громкость и скорость.
App._saveVideoPrefs = function () {
  if (!App._videoPrefsCache) return;
  try {
    localStorage.setItem(App._videoPrefsKey, JSON.stringify({
      v: 2,
      volume: App._videoPrefsCache.volume,
      rate: App._videoPrefsCache.rate,
    }));
  } catch { /* noop */ }
};

// Применяем сохранённые громкость/мьют/скорость к каждому новому <video>.
App._applyVideoPrefs = function (v) {
  if (!v) return;
  const p = App._loadVideoPrefs();
  try { v.volume = p.volume; } catch { /* noop */ }
  try { v.muted = p.muted; } catch { /* noop */ }
  try { v.playbackRate = p.rate; } catch { /* noop */ }
};

App.applyPlaybackRate = function (r) {
  if (App.VIDEO_RATES.indexOf(r) < 0) r = 1;
  const p = App._loadVideoPrefs();
  p.rate = r;
  App._saveVideoPrefs();
  const v = App.currentVideo();
  if (v) { try { v.playbackRate = r; } catch { /* noop */ } }
};

App._syncRateSelect = function (r) {
  const sel = this.els && App.els.ssRateSelect;
  if (!sel || typeof sel.value === 'undefined') return;
  const val = String(r != null ? r : App._loadVideoPrefs().rate);
  try { sel.value = val; } catch { /* noop */ }
};

App.currentVideo = function () {
  if (!App.state.viewerOpen) return null;
  const vc = this.els.viewerContent;
  if (!vc || !vc.querySelector) return null;
  const v = vc.querySelector('video');
  return v && v.tagName === 'VIDEO' ? v : null;
};

App.videoSeekBy = function (sec) {
  const v = App.currentVideo();
  if (!v) return false;
  try {
    const d = isFinite(v.duration) ? v.duration : Infinity;
    v.currentTime = Math.min(Math.max(d - 0.05, 0), Math.max(0, (v.currentTime || 0) + sec));
  } catch { /* noop */ }
  return true;
};

App.videoChangeVolume = function (delta) {
  const v = App.currentVideo();
  if (!v) return false;
  try {
    if (v.muted && delta > 0) v.muted = false;
    v.volume = Math.min(1, Math.max(0, Math.round(((v.volume || 0) + delta) * 100) / 100));
  } catch { /* noop */ }
  return true;
};

App.toggleVideoMute = function () {
  const v = App.currentVideo();
  if (!v) return false;
  try { v.muted = !v.muted; } catch { /* noop */ }
  return true;
};

App._currentPostId = function () {
  const post = this.state.posts && this.state.posts[this.state.viewerIndex];
  return post ? post.id : null;
};

// Позиции просмотра: id → секунды, хранятся в localStorage с ограничением.
App._loadVideoPosMap = function () {
  if (App._videoPosMap) return App._videoPosMap;
  let m = {};
  try { m = JSON.parse(localStorage.getItem(App._videoPosKey) || '{}') || {}; } catch { /* noop */ }
  App._videoPosMap = m && typeof m === 'object' ? m : {};
  return App._videoPosMap;
};

App._rememberVideoTime = function (id, t) {
  if (id == null || !isFinite(t) || t < 3) return;
  const m = App._loadVideoPosMap();
  delete m[id];
  m[id] = Math.floor(t);
  const keys = Object.keys(m);
  while (keys.length > 60) { delete m[keys[0]]; keys.shift(); }
  try { localStorage.setItem(App._videoPosKey, JSON.stringify(m)); } catch { /* noop */ }
};

App._savedVideoTime = function (id) {
  if (id == null) return null;
  const t = App._loadVideoPosMap()[id];
  return typeof t === 'number' ? t : null;
};

App._forgetVideoTime = function (id) {
  if (id == null) return;
  const m = App._loadVideoPosMap();
  if (m[id] == null) return;
  delete m[id];
  try { localStorage.setItem(App._videoPosKey, JSON.stringify(m)); } catch { /* noop */ }
};

// Сохраняет позицию текущего <video> из DOM (вызывать ДО смены поста).
App._stashCurrentVideoTime = function () {
  const vc = this.els && this.els.viewerContent;
  if (!vc || !vc.querySelector) return;
  const v = vc.querySelector('img, video');
  if (!v || v.tagName !== 'VIDEO') return;
  const post = this.state.posts && this.state.posts[this.state.viewerIndex];
  if (!post || post.file_type !== 'video') return;
  if (v.currentTime > 0) App._rememberVideoTime(post.id, v.currentTime);
};

App._resumeVideoPosition = function (post) {
  const v = App.currentVideo();
  if (!v || !post) return;
  const t = App._savedVideoTime(post.id);
  if (t == null || t < 1) return;
  try {
    if (isFinite(v.duration) && t >= v.duration - 1) { App._forgetVideoTime(post.id); return; }
    v.currentTime = t;
  } catch { /* noop */ }
};

// Вешаем на элемент слушатели, сохраняющие громкость/скорость/позицию.
App._bindVideoEvents = function (v) {
  if (!v || !v.addEventListener || v._brieflyBound) return;
  v._brieflyBound = true;
  let lastSave = 0;
  v.addEventListener('volumechange', () => {
    const p = App._loadVideoPrefs();
    p.volume = v.volume;
    // Программный мьют из autoplay-fallback не считаем пользовательским
    // выбором: в сессии он остаётся локальным для этого видео.
    if (App._suppressMuteSync) {
      App._suppressMuteSync = false;
    } else {
      p.muted = !!v.muted;
    }
    App._saveVideoPrefs();
    if (!v.muted) App._removeUnmuteHint();
  });
  v.addEventListener('ratechange', () => {
    const r = v.playbackRate || 1;
    const p = App._loadVideoPrefs();
    if (p.rate !== r) { p.rate = r; App._saveVideoPrefs(); }
    App._syncRateSelect(r);
  });
  v.addEventListener('timeupdate', () => {
    const now = Date.now();
    if (now - lastSave < 2000 || v.paused) return;
    lastSave = now;
    App._rememberVideoTime(App._currentPostId(), v.currentTime);
  });
  v.addEventListener('ended', () => { App._forgetVideoTime(App._currentPostId()); });
};

// Autoplay со звуком браузеры блокируют без жеста пользователя:
// при отказе включаем muted-autoplay и показываем подсказку про звук.
App._autoplayVideo = function (v) {
  if (!v) return;
  let pr = null;
  try { pr = v.play(); } catch { return; }
  if (pr && typeof pr.catch === 'function') {
    pr.catch(() => {
      if (!App.state.viewerOpen || !v.isConnected) return;
      if (!v.muted) App._suppressMuteSync = true;
      try { v.muted = true; } catch { /* noop */ }
      let p2 = null;
      try { p2 = v.play(); } catch { return; }
      if (p2 && p2.catch) p2.catch(() => {});
      App._showUnmuteHint(v);
    });
  }
};

App._showUnmuteHint = function (v) {
  if (!v || !v.isConnected) return;
  const host = v.parentElement;
  if (!host || !host.appendChild) return;
  App._removeUnmuteHint();
  const hint = document.createElement('button');
  hint.type = 'button';
  hint.className = 'video-unmute-hint';
  hint.innerHTML = icon('volumeX', 14, true) + '<span>Включить звук</span>';
  hint.addEventListener('click', (e) => {
    e.stopPropagation();
    try { v.muted = false; v.volume = App._loadVideoPrefs().volume || 1; } catch { /* noop */ }
    let p = null;
    try { p = v.play(); } catch { /* noop */ }
    if (p && p.catch) p.catch(() => {});
    App._removeUnmuteHint();
  });
  App._unmuteHintEl = hint;
  host.appendChild(hint);
};

App._removeUnmuteHint = function () {
  if (App._unmuteHintEl) {
    try { App._unmuteHintEl.remove(); } catch { /* noop */ }
    App._unmuteHintEl = null;
  }
};

App._showViewerMediaError = function (post) {
  const vc = this.els.viewerContent;
  if (!vc || !vc.appendChild) return;
  App._hideViewerMediaError();
  const p = post || (this.state.posts && this.state.posts[this.state.viewerIndex]);
  const box = document.createElement('div');
  box.className = 'viewer-media-error';
  const txt = document.createElement('div');
  txt.className = 'vme-text';
  txt.textContent = `Не удалось загрузить ${p && p.file_type === 'video' ? 'видео' : 'изображение'}${p ? ' #' + p.id : ''}`;
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn-ss';
  btn.innerHTML = icon('refresh', 14) + '<span>Повторить</span>';
  btn.addEventListener('click', () => {
    App._hideViewerMediaError();
    App.renderViewer(true);
  });
  box.appendChild(txt);
  box.appendChild(btn);
  vc.appendChild(box);
  App._viewerErrorEl = box;
};

App._hideViewerMediaError = function () {
  if (App._viewerErrorEl) {
    try { App._viewerErrorEl.remove(); } catch { /* noop */ }
    App._viewerErrorEl = null;
  }
};

App.togglePictureInPicture = function () {
  const v = App.currentVideo();
  const doc = typeof document !== 'undefined' ? document : {};
  if (!v || !doc.pictureInPictureEnabled || !v.requestPictureInPicture) {
    App.showToast('Картинка в картинке не поддерживается', 'error');
    return;
  }
  if (doc.pictureInPictureElement === v) {
    doc.exitPictureInPicture().catch(() => {});
    return;
  }
  v.requestPictureInPicture().catch(() => App.showToast('Не удалось открыть PiP', 'error'));
};
