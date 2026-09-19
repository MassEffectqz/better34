import { App } from './state.js';
import { _ } from './utils.js';
import { API } from './api.js';

/** @this {AppType} */
App.initAuth = async function () {
  this._resolveAuth = null;
  this.authReady = new Promise(res => { this._resolveAuth = res; });
  let me = null;
  try {
    me = await API.get('/auth/me');
  } catch {
    me = null;
  }
  if (me && me.authed && me.user) {
    this.state.user = me.user;
    this._usersExist = true;
    this.hideAuth(); // дизейблим поля логин-формы (см. hideAuth)
    this._resolveAuth();
    return;
  }
  this._usersExist = !!(me && me.users_exist);
  this.showAuth(this._usersExist ? 'login' : 'register');
  await this.authReady;
};

/** @this {AppType} */
App.showAuth = function (mode) {
  this._authMode = mode === 'register' ? 'register' : 'login';
  const o = _('auth-overlay');
  o.classList.remove('hidden');
  // Пока форма скрыта, поля задизейблены (см. hideAuth) — возвращаем.
  ['auth-username', 'auth-password', 'auth-password2'].forEach(id => { /** @type {HTMLInputElement} */ (_(id)).disabled = false; });
  _('auth-title').textContent = this._authMode === 'register' ? 'Создание аккаунта' : 'Вход';
  _('auth-submit').textContent = this._authMode === 'register' ? 'Создать аккаунт' : 'Войти';
  _('auth-toggle').textContent = this._authMode === 'register' ? 'Уже есть аккаунт? Войти' : 'Нет аккаунта? Создать';
  _('auth-pw2-field').style.display = this._authMode === 'register' ? '' : 'none';
  /** @type {HTMLInputElement} */ (_('auth-password')).autocomplete = this._authMode === 'register' ? 'new-password' : 'current-password';
  /** @type {HTMLInputElement} */ (_('auth-password')).placeholder = this._authMode === 'register' ? 'минимум 6 символов' : 'пароль';
  const meter = _('auth-meter');
  if (meter) {
    meter.style.display = this._authMode === 'register' ? '' : 'none';
    meter.dataset.level = '0';
  }
  const hint = _('auth-hint');
  if (hint) { hint.textContent = ''; hint.className = 'auth-hint'; }
  if (!this._authFormBound) {
    this._authFormBound = true;
    _('auth-form').addEventListener('submit', (e) => e.preventDefault());
    _('auth-password').addEventListener('input', () => this.updateAuthMeter());
    _('auth-password2').addEventListener('input', () => this.updateAuthMatch());
  }
  this.setAuthError('');
  if (window.innerWidth > 768) setTimeout(() => /** @type {HTMLInputElement} */ (_('auth-username')).focus(), 50);
  this.loadAuthChips();
};

/** @this {AppType} */
App.updateAuthMeter = function () {
  const meter = _('auth-meter');
  if (!meter) return;
  const p = /** @type {HTMLInputElement} */ (_('auth-password')).value;
  if (!p) { meter.dataset.level = '0'; return; }
  const variety = Number( /[a-z]/.test(p) ) + Number( /[A-Z]/.test(p) ) + Number( /\d/.test(p) ) + Number( /[^A-Za-z0-9]/.test(p) );
  let level;
  if (p.length < 6) level = 1;
  else if (p.length < 10) level = 2;
  else level = variety >= 2 ? 4 : 3;
  meter.dataset.level = String(level);
};

/** @this {AppType} */
App.updateAuthMatch = function () {
  const hint = _('auth-hint');
  if (!hint) return;
  const p1 = /** @type {HTMLInputElement} */ (_('auth-password')).value;
  const p2 = /** @type {HTMLInputElement} */ (_('auth-password2')).value;
  if (!p2) { hint.textContent = ''; hint.className = 'auth-hint'; return; }
  if (p1 === p2) { hint.textContent = 'пароли совпадают'; hint.className = 'auth-hint ok'; }
  else { hint.textContent = 'пароли не совпадают'; hint.className = 'auth-hint bad'; }
};

/** @this {AppType} */
App.loadAuthChips = async function () {
  if (this._authChipsLoaded) return;
  this._authChipsLoaded = true;
  const cont = document.querySelector('.auth-chips');
  if (!cont) return;
  if (!this._chipsParallaxOn) {
    this._chipsParallaxOn = true;
    document.addEventListener('mousemove', (e) => {
      if (this._rafPending) return;
      this._rafPending = true;
      requestAnimationFrame(() => {
        this._rafPending = false;
        const c = /** @type {HTMLElement} */ (document.querySelector('.auth-chips'));
        if (!c) return;
        c.style.setProperty('--px', ((e.clientX / innerWidth) - 0.5) * 30 + 'px');
        c.style.setProperty('--py', ((e.clientY / innerHeight) - 0.5) * 30 + 'px');
      });
    });
  }
  if (!this._chipsResizeBound) {
    this._chipsResizeBound = true;
    let rt;
    window.addEventListener('resize', () => {
      clearTimeout(rt);
      rt = setTimeout(() => this.renderPopularTags(), 120);
    });
  }
  let tags = [];
  try {
    const d = await API.get('/tags/popular?limit=40');
    tags = (d.tags || []).map(t => t.label || t.value || '').filter(t => t && t.length <= 18);
  } catch {
    return;
  }
  if (!tags.length) return;
  this._popularTags = tags;
  this.renderPopularTags();
};

App._chipSeed = 12345;
/** @this {AppType} */
App._chipRnd = function () {
  this._chipSeed = (this._chipSeed * 1103515245 + 12345) & 0x7fffffff;
  return this._chipSeed / 0x7fffffff;
};

/** @this {AppType} */
App.buildChipSlots = function () {
  this._chipSeed = 12345;
  const slots = [];
  for (let i = 0; i < 64; i++) {
    let x = 0, y = 0, t;
    for (t = 0; t < 50; t++) {
      x = 2 + this._chipRnd() * 95;
      y = 2 + this._chipRnd() * 93;
      if (!(Math.abs(x - 50) < 17 && Math.abs(y - 50) < 30)) break;
    }
    if (t >= 50) {
      x = 2 + this._chipRnd() * 95;
      y = 2 + this._chipRnd() * 93;
    }
    slots.push({ x: Math.round(x * 10) / 10, y: Math.round(y * 10) / 10 });
  }
  return slots;
};

/** @this {AppType} */
App.renderPopularTags = function () {
  const cont = document.querySelector('.auth-chips');
  if (!cont || !this._popularTags || !this._popularTags.length) return;
  cont.innerHTML = '';

  const mobile = window.matchMedia('(max-width: 600px)').matches;
  if (mobile) {
    const slots = [{ x: 8, y: 12 }, { x: 72, y: 16 }, { x: 10, y: 84 }, { x: 70, y: 86 }];
    this._popularTags.slice(0, 4).forEach((t, i) => {
      const s = document.createElement('span');
      s.className = `auth-chip c${i % 2 === 0 ? 1 : 4}`;
      s.textContent = t;
      s.style.left = slots[i].x + '%';
      s.style.top = slots[i].y + '%';
      s.style.right = 'auto';
      s.style.bottom = 'auto';
      s.style.animation = 'chipIn .6s ease-out both, chipFloat 11s ease-in-out infinite';
      s.style.animationDelay = `${(i * 0.12).toFixed(2)}s, -${(i * 2.5).toFixed(1)}s`;
      s.style.rotate = ((i - 1.5) * 2).toFixed(1) + 'deg';
      cont.appendChild(s);
    });
    return;
  }

  const slots = this.buildChipSlots();
  const placed = [];
  let placedN = 0;
  for (const tag of this._popularTags.slice(0, 40)) {
    const estW = tag.length * 7.5 + 32;
    const estH = 26;
    for (const slot of slots) {
      if (slot.used) continue;
      const rect = {
        x: slot.x / 100 * cont.clientWidth,
        y: slot.y / 100 * cont.clientHeight,
        w: estW, h: estH
      };
      if (rect.x < 4 || rect.x + rect.w > cont.clientWidth - 4 ||
          rect.y < 4 || rect.y + rect.h > cont.clientHeight - 4) continue;
      let hit = false;
      for (const p of placed) {
        if (rect.x < p.x + p.w + 8 && p.x < rect.x + rect.w + 8 &&
            rect.y < p.y + p.h + 8 && p.y < rect.y + rect.h + 8) {
          hit = true;
          break;
        }
      }
      if (hit) continue;
      slot.used = true;
      placed.push(rect);
      const s = document.createElement('span');
      s.className = `auth-chip c${(placedN % 8) + 1}`;
      s.textContent = tag;
      s.style.left = slot.x + '%';
      s.style.top = slot.y + '%';
      s.style.right = 'auto';
      s.style.bottom = 'auto';
      s.style.fontSize = (0.58 + this._chipRnd() * 0.2).toFixed(2) + 'rem';
      s.style.opacity = (0.2 + this._chipRnd() * 0.2).toFixed(2);
      const dur = (8 + this._chipRnd() * 8).toFixed(1);
      const phase = (this._chipRnd() * 10).toFixed(1);
      const stag = ((placedN % 10) * 0.07).toFixed(2);
      s.style.animation = `chipIn .6s ease-out both, chipFloat ${dur}s ease-in-out infinite`;
      s.style.animationDelay = `${stag}s, -${phase}s`;
      s.style.setProperty('--sway', ((this._chipRnd() - 0.5) * 24).toFixed(0) + 'px');
      s.style.setProperty('--pr', (this._chipRnd() - 0.5).toFixed(2));
      s.style.rotate = ((this._chipRnd() - 0.5) * 8).toFixed(1) + 'deg';
      s.style.filter = `hue-rotate(${Math.round(this._chipRnd() * 70 - 35)}deg)`;
      cont.appendChild(s);
      placedN++;
      break;
    }
  }
};

/** @this {AppType} */
App.hideAuth = function () {
  // Очищаем пароли и дизейблим поля формы: пока аккаунт авторизован,
  // форма входа не должна выглядеть для браузера как «активная логин-форма»,
  // иначе он постоянно предлагает «Сохранить пароль?» при любом вводе.
  ['auth-username', 'auth-password', 'auth-password2'].forEach((id) => {
    const el = /** @type {HTMLInputElement} */ (_(id));
    el.value = '';
    el.disabled = true;
    if (el === document.activeElement) el.blur();
  });
  _('auth-overlay').classList.add('hidden');
};

/** @this {AppType} */
App.setAuthError = function (msg) {
  const el = _('auth-error');
  el.textContent = msg;
  el.style.display = msg ? '' : 'none';
  if (msg) {
    const card = document.querySelector('.auth-card');
    if (card) {
      card.classList.remove('shake');
      void /** @type {HTMLElement} */ (card).offsetWidth;
      card.classList.add('shake');
    }
  }
};

/** @this {AppType} */
App.authSubmit = async function () {
  const u = /** @type {HTMLInputElement} */ (_('auth-username')).value.trim().toLowerCase();
  const p = /** @type {HTMLInputElement} */ (_('auth-password')).value;
  if (!u || !p) { this.setAuthError('Заполните логин и пароль'); return; }
  if (this._authMode === 'register') {
    if (p !== /** @type {HTMLInputElement} */ (_('auth-password2')).value) { this.setAuthError('Пароли не совпадают'); return; }
    if (p.length < 6) { this.setAuthError('Пароль: минимум 6 символов'); return; }
  }
  const btn = /** @type {HTMLButtonElement} */ (_('auth-submit'));
  btn.disabled = true;
  btn.classList.add('loading');
  this.setAuthError('');
  try {
    const r = await API.post(this._authMode === 'register' ? '/auth/register' : '/auth/login', { username: u, password: p });
    this.state.user = r.user;
    this._usersExist = true;
    API.invalidate('/');
    this.hideAuth();
    this._resolveAuth();
    await this.afterLogin();
  } catch (err) {
    const msg = err.message || 'Ошибка';
    this.setAuthError(msg.length > 120 ? msg.substring(0, 120) : msg);
  } finally {
    btn.disabled = false;
    btn.classList.remove('loading');
  }
};

// QR-вход с экрана авторизации: телефон сканирует QR, показанный ПК
// (ПК залогинен → Настройки → Показать QR). Телефон считывает ссылку
// /qr?t=TOKEN и переходит на неё — дальше страница /qr сама обработает.
/** @this {AppType} */
App.authQR = async function () {
  this.setAuthError('');
  if (!('BarcodeDetector' in window) && !navigator.mediaDevices?.getUserMedia) {
    this.setAuthError('Камера недоступна — откройте ссылку вручную');
    return;
  }
  const ov = document.createElement('div');
  ov.id = 'auth-qr-scan-overlay';
  ov.style.cssText = 'position:fixed;inset:0;background:#000;z-index:3100;display:flex;flex-direction:column;align-items:center;justify-content:center;color:#eee;font-family:system-ui';
  ov.innerHTML =
    '<div style="font-size:15px;font-weight:600;margin-bottom:12px">Сканируйте QR с ПК</div>' +
    '<video id="auth-qr-video" autoplay playsinline style="width:260px;height:260px;border-radius:12px;object-fit:cover;background:#222"></video>' +
    '<div id="auth-qr-scan-status" style="font-size:12px;opacity:.7;margin-top:10px">Наведите камеру на QR</div>' +
    '<button id="auth-qr-scan-cancel" style="margin-top:14px;padding:8px 20px;border:none;border-radius:8px;background:#555;color:#eee;cursor:pointer;font-size:13px">Отмена</button>';
  document.body.appendChild(ov);
  this._qrScanOverlay = ov;

  let stream = null;
  let scanning = true;
  const cleanup = () => {
    scanning = false;
    if (stream) stream.getTracks().forEach(t => t.stop());
    if (ov.parentNode) ov.remove();
  };
  /** @type {HTMLElement} */ (ov.querySelector('#auth-qr-scan-cancel')).addEventListener('click', cleanup);
  ov.addEventListener('click', (e) => { if (e.target === ov) cleanup(); });

  try {
    stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } });
    const video = /** @type {HTMLVideoElement} */ (ov.querySelector('#auth-qr-video'));
    video.srcObject = stream;
    await video.play();

    const detect = async () => {
      if (!scanning) return;
      if ('BarcodeDetector' in window) {
        try {
          const barcodes = await new /** @type {any} */ (BarcodeDetector)({ formats: ['qr_code'] }).detect(video);
          if (barcodes.length) {
            const val = barcodes[0].rawValue;
            // Только ссылки этого же origin: QR со сторонним URL
            // (https://evil.com/...?x=/qr?t=1) не должен уводить со страницы.
            try {
              const u = new URL(val, location.href);
              if (u.origin === location.origin && u.pathname === '/qr') {
                cleanup();
                location.href = u.href;
                return;
              }
            } catch {}
          }
        } catch {}
      }
      requestAnimationFrame(detect);
    };
    if ('BarcodeDetector' in window) {
      detect();
    } else {
      /** @type {HTMLElement} */ (ov.querySelector('#auth-qr-scan-status')).textContent =
        'BarcodeDetector недоступен — откройте ссылку вручную';
    }
  } catch (err) {
    cleanup();
    this.setAuthError('Не удалось открыть камеру: ' + (err.message || err));
  }
};

/** @this {AppType} */
App.afterLogin = async function () {
  await this.loadProfile();
  this.updateAuthUI();
  const name = this.state.user ? this.state.user.nickname || this.state.user.username : '';
  this.showToast(`Добро пожаловать, ${name}!`);
  await this.loadPosts(true);
};

/** @this {AppType} */
App.authToggleMode = function () {
  this.showAuth(this._authMode === 'register' ? 'login' : 'register');
};

/** @this {AppType} */
App.updateAuthUI = function () {
  const u = this.state.user;
  const item = document.querySelector('.header-menu-item[data-action="logout"]');
  if (item) /** @type {HTMLElement} */ (item).style.display = u ? '' : 'none';
  // Заполняем скрытое username-поле формы смены пароля — без него Chrome
  // ругается "[DOM] Password forms should have username fields" в консоли.
  const un = document.getElementById('input-current-username');
  if (un && u && u.username) un.value = u.username;
};

/** @this {AppType} */
App.changePassword = async function () {
  const current = (this.els.inputCurrentPassword?.value || '').trim();
  const next = this.els.inputNewPassword?.value || '';
  const unField = document.getElementById('input-current-username');
  if (unField && !unField.value && this.state.user?.username) unField.value = this.state.user.username;
  if (!current || next.length < 6) {
    this.showToast('Введите текущий и новый пароль (мин. 6 символов)', 'error');
    return;
  }
  try {
    const r = await API.post('/auth/password', { current_password: current, new_password: next });
    if (r.ok) {
      this.showToast('Пароль изменён', 'success');
      if (this.els.inputCurrentPassword) this.els.inputCurrentPassword.value = '';
      if (this.els.inputNewPassword) this.els.inputNewPassword.value = '';
    }
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
};

/** @this {AppType} */
App.logoutOthers = async function () {
  const ok = await this.confirmDialog({
    title: 'Завершить другие сессии?',
    message: 'Все устройства, кроме текущего, будут разлогинены.',
    okText: 'Завершить',
    danger: true,
  });
  if (!ok) return;
  try {
    const r = await API.post('/auth/logout-others');
    this.showToast(`Отозвано сессий: ${r.revoked || 0}`, 'success');
  } catch (err) {
    this.showToast(`Ошибка: ${err.message}`, 'error');
  }
};

/** @this {AppType} */
App.logout = async function () {
  const ok = await this.confirmDialog({ title: 'Выйти из аккаунта?', message: 'Вы будете перенаправлены на страницу входа.', okText: 'Выйти', danger: true });
  if (!ok) return;
  try { await API.post('/auth/logout'); } catch {}
  API.invalidate('/');
  // Сбрасываем кэш авторизации API (legacy-токен читается из meta —
  // после логина/логаута он должен перечитаться заново).
  API._token = null;
  this.state.user = null;
  this.state.profile = { liked_posts: [], hidden_posts: [], presets: [], fav_tags: [], hidden_tags: [], nickname: '', avatar: '' };
  this.state.posts = [];
  this._thumbs = {};
  if (this.state.profileOpen) this.toggleProfile();
  if (this.state.settingsOpen) this.toggleSettings();
  this.clearGrid();
  this.loadProfileMeta();
  this.renderProfile();
  this.updateAuthUI();
  this.showAuth(this._usersExist ? 'login' : 'register');
  this._resolveAuth = null;
  this.authReady = new Promise(res => { this._resolveAuth = res; });
  await this.authReady;
  await this.loadPosts(true);
};
