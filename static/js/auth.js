import { App } from './state.js';
import { _ } from './utils.js';
import { API } from './api.js';
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
    this._resolveAuth();
    return;
  }
  this._usersExist = !!(me && me.users_exist);
  this.showAuth(this._usersExist ? 'login' : 'register');
  await this.authReady;
};

App.showAuth = function (mode) {
  this._authMode = mode === 'register' ? 'register' : 'login';
  const o = _('auth-overlay');
  o.classList.remove('hidden');
  _('auth-title').textContent = this._authMode === 'register' ? 'Создание аккаунта' : 'Вход';
  _('auth-submit').textContent = this._authMode === 'register' ? 'Создать аккаунт' : 'Войти';
  _('auth-toggle').textContent = this._authMode === 'register' ? 'Уже есть аккаунт? Войти' : 'Нет аккаунта? Создать';
  _('auth-pw2-field').style.display = this._authMode === 'register' ? '' : 'none';
  _('auth-password').autocomplete = this._authMode === 'register' ? 'new-password' : 'current-password';
  _('auth-password').placeholder = this._authMode === 'register' ? 'минимум 6 символов' : 'пароль';
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
  setTimeout(() => _('auth-username').focus(), 50);
  this.loadAuthChips();
};

App.updateAuthMeter = function () {
  const meter = _('auth-meter');
  if (!meter) return;
  const p = _('auth-password').value;
  if (!p) { meter.dataset.level = '0'; return; }
  const variety = /[a-z]/.test(p) + /[A-Z]/.test(p) + /\d/.test(p) + /[^A-Za-z0-9]/.test(p);
  let level;
  if (p.length < 6) level = 1;
  else if (p.length < 10) level = 2;
  else level = variety >= 2 ? 4 : 3;
  meter.dataset.level = level;
};

App.updateAuthMatch = function () {
  const hint = _('auth-hint');
  if (!hint) return;
  const p1 = _('auth-password').value;
  const p2 = _('auth-password2').value;
  if (!p2) { hint.textContent = ''; hint.className = 'auth-hint'; return; }
  if (p1 === p2) { hint.textContent = 'пароли совпадают'; hint.className = 'auth-hint ok'; }
  else { hint.textContent = 'пароли не совпадают'; hint.className = 'auth-hint bad'; }
};

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
        const c = document.querySelector('.auth-chips');
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
App._chipRnd = function () {
  this._chipSeed = (this._chipSeed * 1103515245 + 12345) & 0x7fffffff;
  return this._chipSeed / 0x7fffffff;
};

App.buildChipSlots = function () {
  this._chipSeed = 12345;
  const slots = [];
  for (let i = 0; i < 64; i++) {
    let x, y, t;
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

App.hideAuth = function () {
  _('auth-overlay').classList.add('hidden');
};

App.setAuthError = function (msg) {
  const el = _('auth-error');
  el.textContent = msg;
  el.style.display = msg ? '' : 'none';
  if (msg) {
    const card = document.querySelector('.auth-card');
    if (card) {
      card.classList.remove('shake');
      void card.offsetWidth;
      card.classList.add('shake');
    }
  }
};

App.authSubmit = async function () {
  const u = _('auth-username').value.trim().toLowerCase();
  const p = _('auth-password').value;
  if (!u || !p) { this.setAuthError('Заполните логин и пароль'); return; }
  if (this._authMode === 'register') {
    if (p !== _('auth-password2').value) { this.setAuthError('Пароли не совпадают'); return; }
    if (p.length < 6) { this.setAuthError('Пароль: минимум 6 символов'); return; }
  }
  const btn = _('auth-submit');
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

App.afterLogin = async function () {
  await this.loadProfile();
  this.updateAuthUI();
  this.showToast(`Добро пожаловать, ${this.state.user.nickname || this.state.user.username}!`);
  await this.loadPosts(true);
};

App.authToggleMode = function () {
  this.showAuth(this._authMode === 'register' ? 'login' : 'register');
};

App.updateAuthUI = function () {
  const u = this.state.user;
  const item = document.querySelector('.header-menu-item[data-action="logout"]');
  if (item) item.style.display = u ? '' : 'none';
};

App.logout = async function () {
  try { await API.post('/auth/logout'); } catch {}
  API.invalidate('/');
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