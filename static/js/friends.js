// friends.js вЂ” РІРєР»Р°РґРєР° В«Р”СЂСѓР·СЊСЏВ»: РѕР±РјРµРЅ Р»Р°Р№РєР°РјРё, РєРѕР»Р»РµРєС†РёСЏРјРё Рё РєРѕРјРјРµРЅС‚Р°СЂРёСЏРјРё
// РЅР°РїСЂСЏРјСѓСЋ РјРµР¶РґСѓ РёРЅСЃС‚Р°РЅСЃР°РјРё better34, Р±РµР· РѕР±С‰РµРіРѕ СЃРµСЂРІРµСЂР°.
//
// РЎРёРЅС…СЂРѕРЅРёР·Р°С†РёСЏ С„РѕРЅРѕРІР°СЏ (СЃРµСЂРІРµСЂ С…РѕРґРёС‚ СЃР°Рј РєР°Р¶РґС‹Рµ 5 РјРёРЅСѓС‚), Р° РєРЅРѕРїРєР°
// В«РћР±РјРµРЅСЏС‚СЊСЃСЏВ» РґРµР»Р°РµС‚ С‚Рѕ Р¶Рµ СЃР°РјРѕРµ РїРѕ С‚СЂРµР±РѕРІР°РЅРёСЋ. Р”РѕР±Р°РІР»РµРЅРёРµ РґСЂСѓРіР° вЂ” РїРѕ
// С‚РµРєСЃС‚РѕРІРѕРјСѓ РєРѕРґСѓ: РґР»РёРЅРЅР°СЏ СЃС‚СЂРѕРєР° РєРѕРїРёСЂСѓРµС‚СЃСЏ Рё РІСЃС‚Р°РІР»СЏРµС‚СЃСЏ, СЂСѓРєР°РјРё РЅРµ
// РЅР°Р±РёСЂР°РµС‚СЃСЏ (QR СЃРѕР·РЅР°С‚РµР»СЊРЅРѕ РЅРµ РґРµР»Р°РµРј вЂ” РєРѕРґР° РІСЃС‚Р°РІР»СЏРµС‚СЃСЏ РѕРґРёРЅ СЂР°Р· Р·Р° РІСЃС‘
// РІСЂРµРјСЏ, Р° С†РµРЅР° РІ СЃРѕС‚РЅРё СЃС‚СЂРѕРє СЌРЅРєРѕРґРµСЂР° С‚РѕРіРѕ РЅРµ СЃС‚РѕРёС‚).
import { App } from './state.js';
import { icon, esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

// Р­Р»РµРјРµРЅС‚С‹ РІРєР»Р°РґРєРё РёС‰РµРј СЃР°РјРё, С‡РµСЂРµР· getElementById, Р° РЅРµ С‡РµСЂРµР· this.els.
// РџСЂРёС‡РёРЅР°: this.els Р·Р°РїРѕР»РЅСЏРµС‚СЃСЏ РІРЅСѓС‚СЂРё App.init(), Р° РїСЂРёРІСЏР·РєР° РєРЅРѕРїРѕРє РёРґС‘С‚
// СЂР°РЅСЊС€Рµ (app.js Р·РѕРІС‘С‚ bindFriendsUI РґРѕ init) вЂ” С‡РµСЂРµР· els РІСЃРµ РїРѕР»СЏ Р±С‹Р»Рё Р±С‹
// undefined Рё РѕР±СЂР°Р±РѕС‚С‡РёРєРё РЅРµ РЅР°РІРµСЃРёР»РёСЃСЊ Р±С‹. РўР°Рє Р¶Рµ РїРѕСЃС‚СѓРїР°РµС‚
// offline_library.js.
const $ = (id) => document.getElementById(id);

// РљРµС€ СЃРїРёСЃРєР° РЅР° РІСЂРµРјСЏ СЃРµСЃСЃРёРё: РїРµСЂРµРєР»СЋС‡РµРЅРёРµ РІРєР»Р°РґРѕРє С‚СѓРґР°-РѕР±СЂР°С‚РЅРѕ РЅРµ РґРѕР»Р¶РЅРѕ
// РґС‘СЂРіР°С‚СЊ СЃРµС‚СЊ. РљРЅРѕРїРєР° В«РћР±РјРµРЅСЏС‚СЊСЃСЏВ» РєРµС€ СЃР±СЂР°СЃС‹РІР°РµС‚.
let friendsCache = null;

/** В«5 РјРёРЅСѓС‚ РЅР°Р·Р°РґВ» Рё РїРѕРґРѕР±РЅРѕРµ вЂ” С‡С‚РѕР±С‹ СЃС‚Р°С‚СѓСЃ С‡РёС‚Р°Р»СЃСЏ, Р° РЅРµ РєР°Рє РјРµС‚РєР° РІСЂРµРјРµРЅРё. */
// Коды ошибок, которые сервер кладёт в last_error (см. friendStatusErr).
// Раньше здесь показывался голый «friend_no_back» или «403» — пользователь
// не понимал, что делать. Теперь это готовая подсказка.
const FRIEND_ERR_KEYS = {
  friend_no_back: 'friends.errNoBack',
  friend_bad_key: 'friends.errBadKey',
  friend_host_blocked: 'friends.errHostBlocked',
  friend_forbidden: 'friends.errForbidden',
  friend_too_big: 'friends.errTooBig',
  friend_server_error: 'friends.errServer',
};

/** Текст ошибки обмена: известные коды переводим, неизвестные — общим текстом. */
function friendErrText(raw) {
  const key = FRIEND_ERR_KEYS[raw];
  return key ? t(key) : t('friends.errOther');
}

/** «5 минут назад» и подобное: полный текст, если время разбирается. */
function whenText(raw) {
  const ms = Date.parse(String(raw || '').replace(' ', 'T'));
  if (Number.isNaN(ms)) return String(raw || '').slice(0, 16).replace('T', ' ');
  const diff = Date.now() - ms;
  if (diff < 60_000) return t('friends.justNow');
  if (diff < 3_600_000) return tf('friends.minutesAgo', { m: Math.floor(diff / 60_000) });
  if (diff < 86_400_000) return tf('friends.hoursAgo', { h: Math.floor(diff / 3_600_000) });
  return String(raw).slice(0, 16).replace('T', ' ');
}

App.loadFriends = async function (force) {
  if (friendsCache && !force) return friendsCache;
  try {
    const res = await API.get('/friends');
    friendsCache = (res && res.friends) || [];
  } catch {
    friendsCache = [];
  }
  return friendsCache;
};

App.renderFriends = async function () {
  const host = $('friends-list');
  if (!host) return;
  const list = await this.loadFriends(false);
  const counter = $('st-friends');
  if (counter) counter.textContent = String(list.length);

  if (!list.length) {
    host.innerHTML = `<p class="profile-empty">${esc(t('friends.empty'))}</p>`;
    return;
  }
  host.innerHTML = '';
  list.forEach((f) => {
    const row = document.createElement('div');
    row.className = 'friend-item';
    // РђРІР°С‚Р°СЂ РґСЂСѓРіР° вЂ” РІРЅРµС€РЅРёР№ URL: РїСЂРѕРїСѓСЃРєР°РµРј С‚РѕР»СЊРєРѕ Р±РµР·РѕРїР°СЃРЅС‹Рµ СЃС…РµРјС‹, РёРЅР°С‡Рµ
    // РїРѕРґСЃС‚Р°РІР»РµРЅРЅС‹Р№ javascript: СѓС€С‘Р» Р±С‹ РїСЂСЏРјРѕ РІ src (С‚Р° Р¶Рµ РїСЂРѕРІРµСЂРєР°, С‡С‚Рѕ
    // РІ social.js РґР»СЏ РєРѕРјРјРµРЅС‚Р°СЂРёРµРІ).
    const avatarOK = f.avatar && /^(https?:\/\/|\/|data:image\/)/.test(f.avatar);
    const avatar = avatarOK
      ? `<img class="friend-avatar" src="${esc(f.avatar)}" alt="">`
      : `<span class="friend-avatar friend-avatar-empty">${icon('user', 15)}</span>`;
    const name = f.nickname || f.username || f.url;
    const status = f.last_error
      ? `<span class="friend-status friend-status-err" title="${esc(f.last_error)}">${esc(friendErrText(f.last_error))}</span>`
      : (f.last_sync
        ? `<span class="friend-status">${esc(tf('friends.synced', { when: whenText(f.last_sync) }))}</span>`
        : `<span class="friend-status friend-status-wait">${esc(t('friends.pending'))}</span>`);
    row.innerHTML = `
      ${avatar}
      <span class="friend-body">
        <span class="friend-name">${esc(name)}</span>
        <span class="friend-url">${esc(f.url)}</span>
        ${status}
      </span>
      <button class="btn-icon btn-icon-sm friend-del" data-id="${esc(f.id)}" title="${esc(t('friends.remove'))}" aria-label="${esc(t('friends.remove'))}">${icon('trash', 14)}</button>`;
    const del = row.querySelector('.friend-del');
    del.addEventListener('click', async () => {
      const ok = await this.confirmDialog({
        title: t('friends.removeTitle'),
        message: tf('friends.removeMsg', { name }),
        okText: t('confirm.ok'),
        danger: true,
      });
      if (!ok) return;
      try {
        await API.del(`/friends/${encodeURIComponent(/** @type {HTMLElement} */ (del).dataset.id)}`);
        friendsCache = null;
        this.renderFriends();
        this.showToast(t('friends.removed'));
      } catch { /* РЅРµ СѓРґР°Р»РёР»СЃСЏ вЂ” РѕСЃС‚Р°С‘С‚СЃСЏ РІ СЃРїРёСЃРєРµ */ }
    });
    host.appendChild(row);
  });
};

/** РџРѕРєР°Р·С‹РІР°РµС‚ РјРѕР№ РєРѕРґ РґР»СЏ РїРµСЂРµРґР°С‡Рё РґСЂСѓРіСѓ. */
App.showFriendCode = async function () {
  const box = $('friends-code-box');
  const out = $('friends-code');
  if (!box || !out) return;
  try {
    const res = await API.get('/friends/code');
    out.textContent = (res && res.code) || '';
    box.classList.remove('hidden');
  } catch {
    this.showToast(t('friends.codeFailed'));
  }
};

/** РћРґРЅРѕРєСЂР°С‚РЅС‹Р№ РѕР±РјРµРЅ РїРѕ РєРЅРѕРїРєРµ (С„РѕРЅРѕРІС‹Р№ С†РёРєР» РІСЃС‘ СЂР°РІРЅРѕ СЂР°Р±РѕС‚Р°РµС‚ СЃР°Рј). */
App.syncFriendsNow = async function () {
  const btn = $('btn-friends-sync');
  if (btn) btn.disabled = true;
  try {
    const res = await API.post('/friends/sync');
    const synced = (res && res.synced) || {};
    let total = 0;
    Object.values(synced).forEach((/** @type {any} */ st) => {
      total += (st.likes || 0) + (st.collections || 0) + (st.comments || 0);
    });
    friendsCache = null;
    await this.renderFriends();
    // РњРѕРіР»Рё РїСЂРёР№С‚Рё С‡СѓР¶РёРµ Р»Р°Р№РєРё Рё РєРѕР»Р»РµРєС†РёРё вЂ” РїРµСЂРµС‡РёС‚С‹РІР°РµРј РїСЂРѕС„РёР»СЊ, РёРЅР°С‡Рµ
    // СЃС‡С‘С‚С‡РёРєРё РЅР° РІРєР»Р°РґРєР°С… РѕСЃС‚Р°Р»РёСЃСЊ Р±С‹ СЃС‚Р°СЂС‹РјРё.
    if (typeof this.loadProfile === 'function') {
      await this.loadProfile();
      if (typeof this.renderCollections === 'function') this.renderCollections();
    }
    this.showToast(total > 0 ? tf('friends.syncedGot', { n: total }) : t('friends.syncedNone'));
  } catch {
    this.showToast(t('friends.syncFailed'));
  } finally {
    if (btn) btn.disabled = false;
  }
};

/** Р”РѕР±Р°РІР»РµРЅРёРµ РґСЂСѓРіР° РїРѕ РєРѕРґСѓ: РїСЂРѕР±СѓРµРј Р±СѓС„РµСЂ РѕР±РјРµРЅР°, РёРЅР°С‡Рµ РїСЂРѕСЃРёРј РІРІРµСЃС‚Рё. */
/** Префикс friend-кода — по нему отсекаем мусор из буфера обмена. */
const FRIEND_CODE_PREFIX = 'briefly-friend-v1:';

/**
 * Добавление друга по коду.
 *
 * Буфер берём только если в нём ПОХОЖЕ НА КОД: обычно там лежит что-то
 * другое (скопированный текст, картинка), и раньше мы молча отправляли это на
 * добавление — пользователь получал отказ вместо возможности вставить код
 * руками. Если в буфере не код — раскрываем поле ввода.
 */
App.addFriendByCode = async function () {
  let code = '';
  try {
    // readText требует https и разрешения; при отказе просто спрашиваем.
    const clip = await navigator.clipboard.readText();
    if (String(clip || '').trim().startsWith(FRIEND_CODE_PREFIX)) code = clip;
  } catch { /* буфер недоступен — спросим вручную */ }
  if (!code) {
    this.openFriendCodeInput();
    return;
  }
  await this.submitFriendCode(code);
};

/**
 * Отправляет код на добавление.
 * @param {string} code
 */
App.submitFriendCode = async function (code) {
  const panel = $('friends-add-form');
  const input = $('friends-add-input');
  code = String(code || '').trim();
  if (!code) return;
  try {
    await API.post('/friends', { code });
    friendsCache = null;
    if (input) input.value = '';
    if (panel) panel.classList.add('hidden');
    await this.renderFriends();
    this.showToast(t('friends.added'));
  } catch (err) {
    // С бэка приходит код в error: friend_code_invalid / friend_exists /
    // friend_self. Показываем по нему — так пользователь поймёт причину.
    // API бросает new Error(код) — код в message (api.js _err).
    const reason = String((err && (err && err.message)) || '');
    const known = {
      friend_code_invalid: t('friends.errCodeInvalid'),
      friend_exists: t('friends.errExists'),
      friend_self: t('friends.errSelf'),
    }[reason];
    this.showToast(known || t('friends.addFailed'));
  }
};

/**
 * Раскрывает поле ввода кода. window.prompt здесь плох: он блокирует
 * страницу, на телефоне выглядит как системный диалог, а код длинный.
 * Поэтому обычное поле прямо в панели.
 */
App.openFriendCodeInput = function () {
  const panel = $('friends-add-form');
  const input = $('friends-add-input');
  if (!panel || !input) return;
  panel.classList.remove('hidden');
  input.focus();
};

/**
 * РџСЂРёРІСЏР·РєР° РєРЅРѕРїРѕРє РІРєР»Р°РґРєРё. Р’С‹Р·С‹РІР°РµС‚СЃСЏ РѕРґРёРЅ СЂР°Р· РїСЂРё СЃС‚Р°СЂС‚Рµ, РґРѕ App.init(),
 * РїРѕСЌС‚РѕРјСѓ СЌР»РµРјРµРЅС‚С‹ РёС‰РµРј СЃР°РјРё (СЃРј. $ РІС‹С€Рµ), Р° РЅРµ С‡РµСЂРµР· this.els.
 */
App.bindFriendsUI = function () {
  // РџРѕРІС‚РѕСЂРЅС‹Р№ РІС‹Р·РѕРІ (РїРµСЂРµР·Р°РіСЂСѓР·РєР° РјРѕРґСѓР»СЏ РІ С‚РµСЃС‚Р°С…) РЅРµ РґРѕР»Р¶РµРЅ РІРµС€Р°С‚СЊ
  // РѕР±СЂР°Р±РѕС‚С‡РёРєРё РґРІР°Р¶РґС‹ вЂ” РёРЅР°С‡Рµ РєР»РёРє РѕС‚РїСЂР°РІР»СЏР» Р±С‹ РґРІР° Р·Р°РїСЂРѕСЃР°.
  if (this._friendsBound) return;
  this._friendsBound = true;

  const bind = (id, fn) => {
    const el = $(id);
    if (el) el.addEventListener('click', fn);
  };

  bind('btn-friends-code', () => this.showFriendCode());
  bind('btn-friends-sync', () => this.syncFriendsNow());
  bind('btn-friends-add', () => this.addFriendByCode());
  // Подтверждение ввода кода: кнопка и Enter в поле.
  bind('btn-friends-add-confirm', () => {
    const input = $('friends-add-input');
    this.submitFriendCode(input ? input.value : '');
  });
  const addInput = $('friends-add-input');
  if (addInput) {
    addInput.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        this.submitFriendCode(addInput.value);
      }
    });
  }
  bind('btn-friends-code-copy', async () => {
    const out = $('friends-code');
    const code = out ? out.textContent : '';
    if (!code) return;
    try {
      await navigator.clipboard.writeText(code);
      this.showToast(t('friends.codeCopied'));
    } catch {
      this.showToast(t('friends.codeCopyFailed'));
    }
  });
};
