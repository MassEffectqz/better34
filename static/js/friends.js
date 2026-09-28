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
    // Вся строка открывает профиль друга: его лайки, дизлайки и теги.
    // Кнопка корзины перехватывает клик сама (stopPropagation), иначе
    // удаление ещё и открывало бы профиль того, кого убирают.
    row.classList.add('friend-item-link');
    row.setAttribute('role', 'button');
    row.tabIndex = 0;
    const open = () => this.openFriendProfile(f.id);
    row.addEventListener('click', open);
    row.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        open();
      }
    });
    const del = row.querySelector('.friend-del');
    del.addEventListener('click', async (e) => {
      e.stopPropagation();
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
/**
 * ── Профиль друга ───────────────────────────────────────────────────────────
 * Это отдельная страница с адресом /friend/<id>[/<tab>], а не панель во вкладке
 * «Друзья»: по ней работают «назад» в браузере, перезагрузка и прямая ссылка.
 *
 * Данные приходят одним снимком (/friends/:id/profile), а сами посты клиент
 * добирает через общий _fetchPostsByIds — он умеет пачками и сам достраивает
 * deferred. Кэш по id друга: переключение туда-обратно не должно дёргать сеть.
 */
const friendProfileCache = new Map();

/** Сколько плиток рисуем за раз: остальное — кнопкой «показать ещё». */
const FRIEND_PAGE = 60;

/** Вкладки профиля друга в порядке показа. */
const FRIEND_TABS = ['likes', 'disliked', 'favtags', 'dislikedtags', 'collections'];

/** Адрес страницы друга. Таб «likes» не пишем — это адрес по умолчанию. */
function friendUrl(id, tab) {
  return tab && tab !== 'likes' ? `/friend/${id}/${tab}` : `/friend/${id}`;
}

/**
 * Открывает страницу профиля друга.
 *
 * opts.push !== false — добавить запись в историю. Из разбора URL (popstate,
 * стартовая загрузка) передаём push:false, иначе «назад» на каждом шаге
 * подменял бы историю новой записью и зациклил переходы.
 */
App.openFriendProfile = async function (id, tab, opts) {
  const box = $('friend-profile');
  if (!box) return;
  const wantTab = FRIEND_TABS.includes(tab) ? tab : 'likes';
  if (!this._friendProfile || this._friendProfile.id !== id) {
    this._friendProfile = { id, data: null, tab: wantTab, shown: FRIEND_PAGE };
  } else {
    this._friendProfile.tab = wantTab;
  }
  // Класс на body прячет ленту (см. CSS): страница друга отдельная, а не
  // поверхность поверх сетки, иначе при скролле выглядывали бы обе.
  document.body.classList.add('friend-profile-open');
  if (opts === undefined || opts.push !== false) {
    const url = friendUrl(id, wantTab);
    if (url !== this._lastURL) {
      this._lastURL = url;
      history.pushState({ friendId: id, friendTab: wantTab }, '', url);
    }
  }
  await this.renderFriendProfile();
};

/** Возврат к ленте: пушим историю, чтобы «назад» не вернул страницу друга. */
App.closeFriendProfile = function () {
  const box = $('friend-profile');
  if (box) box.classList.add('hidden');
  document.body.classList.remove('friend-profile-open');
  this._friendProfile = null;
  // Возврат на ленту: postUrl(query, null) — канонический адрес, который
  // понимает parseLocation (пустой запрос даёт «/»).
  const back = this.postUrl(this.state.query, null);
  if (this._lastURL && this._lastURL.startsWith('/friend/') && this._lastURL !== back) {
    this._lastURL = back;
    history.pushState({ query: this.state.query, postId: null }, '', back);
  }
};

/** Смена вкладки: адрес меняется, поэтому «назад» листает по вкладкам. */
App._friendProfileTab = async function (tab) {
  const st = this._friendProfile;
  if (!st || !st.id || st.tab === tab) return;
  st.tab = tab;
  st.shown = FRIEND_PAGE;
  const url = friendUrl(st.id, tab);
  this._lastURL = url;
  history.pushState({ friendId: st.id, friendTab: tab }, '', url);
  await this.renderFriendProfileTabs();
};

App.renderFriendProfile = async function () {
  const box = $('friend-profile');
  if (!box) return;
  const st = this._friendProfile;
  if (!st || !st.id) return;
  if (!st.data) {
    let res = friendProfileCache.get(st.id);
    if (!res) {
      try {
        res = await API.get(`/friends/${encodeURIComponent(st.id)}/profile`);
        // Кэшируем только успех: при сетевой ошибке повторим при следующем клике.
        friendProfileCache.set(st.id, res);
      } catch {
        res = { friend: { id: st.id, url: '', likes: [], disliked: [], fav_tags: [], disliked_tags: [], collections: [] } };
      }
    }
    st.data = res;
  }
  const f = (st.data && st.data.friend) || {};
  const name = f.nickname || f.url || '';

  box.innerHTML = `
    <div class="friend-profile-head">
      <button id="btn-friend-back" class="btn btn-sm" title="${esc(t('friends.back'))}">
        ${icon('chevronLeft', 14)} <span>${esc(t('friends.back'))}</span>
      </button>
      <div class="friend-profile-id">
        <div class="friend-profile-name">${esc(name)}</div>
        <div class="friend-profile-url">${esc(f.url || '')}</div>
      </div>
      <button id="btn-friend-profile-sync" class="btn btn-sm" title="${esc(t('friends.syncTitle'))}">
        ${icon('refresh', 14)} <span>${esc(t('friends.sync'))}</span>
      </button>
    </div>
    <div class="friend-profile-stats">${this._friendStatsHTML(f)}</div>
    <div class="friend-profile-tabs" role="tablist"></div>
    <div id="friend-profile-body" class="friend-profile-body"></div>`;

  $('btn-friend-back').addEventListener('click', () => this.closeFriendProfile());
  $('btn-friend-profile-sync').addEventListener('click', () => this._friendProfileSync(st));
  await this.renderFriendProfileTabs();
};

/** Обмен по кнопке из профиля: сбрасываем кэш и перерисовываем. */
App._friendProfileSync = async function (st) {
  const btn = $('btn-friend-profile-sync');
  if (btn) btn.disabled = true;
  try {
    await API.post('/friends/sync');
    friendProfileCache.delete(st.id);
    friendsCache = null;
    st.data = null;
    await this.renderFriendProfile();
  } catch {
    this.showToast(t('friends.syncFailed'));
  } finally {
    const again = $('btn-friend-profile-sync');
    if (again) again.disabled = false;
  }
};

/** Счётчики на шапке профиля: сколько всего у друга на каждой вкладке. */
App._friendStatsHTML = function (f) {
  const cells = [
    [(f.likes || []).length, t('friends.tabLikes')],
    [(f.disliked || []).length, t('friends.tabDisliked')],
    [(f.fav_tags || []).length, t('friends.tabFavTags')],
    [(f.collections || []).length, t('friends.tabCollections')],
  ];
  const when = f.synced_at ? tf('friends.synced', { when: whenText(f.synced_at) }) : t('friends.pending');
  return cells.map(([n, label]) => `
    <span class="friend-stat"><b>${esc(String(n))}</b><span>${esc(label)}</span></span>`).join('') +
    `<span class="friend-stat friend-stat-when">${esc(when)}</span>`;
};

/** Кнопка вкладки с числом: число держим в data-tab, чтобы счётчики не спорили с id. */
App._friendTabBtn = function (key, list, label) {
  const st = this._friendProfile;
  const n = (list || []).length;
  const active = !!st && st.tab === key;
  return `<button class="friend-tab${active ? ' active' : ''}" data-tab="${esc(key)}" role="tab" aria-selected="${active ? 'true' : 'false'}">${esc(label)} <span class="friend-tab-n">${esc(String(n))}</span></button>`;
};

App.renderFriendProfileTabs = async function () {
  const st = this._friendProfile;
  const body = $('friend-profile-body');
  const box = $('friend-profile');
  if (!st || !body || !box) return;
  const f = (st.data && st.data.friend) || {};
  const tabs = box.querySelector('.friend-profile-tabs');
  const lists = {
    likes: f.likes, disliked: f.disliked, favtags: f.fav_tags,
    dislikedtags: f.disliked_tags, collections: f.collections,
  };
  const labels = {
    likes: t('friends.tabLikes'), disliked: t('friends.tabDisliked'),
    favtags: t('friends.tabFavTags'), dislikedtags: t('friends.tabDislikedTags'),
    collections: t('friends.tabCollections'),
  };
  tabs.innerHTML = FRIEND_TABS.map((k) => this._friendTabBtn(k, lists[k], labels[k])).join('');
  tabs.querySelectorAll('.friend-tab').forEach((el) => {
    el.addEventListener('click', () => this._friendProfileTab(el.dataset.tab));
  });

  body.innerHTML = '';
  if (st.tab === 'favtags') return this._friendTags(body, f.fav_tags || [], 'fav');
  if (st.tab === 'dislikedtags') return this._friendDislikedTags(body, f.disliked_tags || []);
  if (st.tab === 'collections') return this._friendCollections(body, f.collections || []);
  return this._friendPosts(body, st.tab === 'disliked' ? (f.disliked || []) : (f.likes || []));
};

/** Избранные теги друга: те же чипы, что в своём профиле. */
App._friendTags = function (body, tags, kind) {
  if (!tags.length) {
    body.innerHTML = `<p class="profile-empty">${esc(t('friends.profileEmpty'))}</p>`;
    return;
  }
  const frag = document.createDocumentFragment();
  tags.forEach((tag) => {
    const d = document.createElement('div');
    // profile-tag-item fav|hidden — те же строки, что в своём профиле: свои
    // классы выглядели бы иначе в светлой и тёмной теме.
    d.className = `profile-tag-item ${kind === 'fav' ? 'fav' : 'hidden'}`;
    d.title = tag;
    d.innerHTML = `${icon(kind === 'fav' ? 'bookmark' : 'eye', 11)}<span>${esc(tag)}</span>`;
    frag.appendChild(d);
  });
  body.appendChild(frag);
};

/** Штрафные теги: тег и счётчик «голосов против». */
App._friendDislikedTags = function (body, tags) {
  if (!tags.length) {
    body.innerHTML = `<p class="profile-empty">${esc(t('friends.profileEmpty'))}</p>`;
    return;
  }
  body.innerHTML = tags.map((d) => `
    <div class="friend-dislike-row">
      <span class="profile-tag-item hidden">${icon('eye', 11)}<span>${esc(d.tag)}</span></span>
      <span class="friend-dislike-n">${esc(tf('friends.dislikeCount', { n: d.count }))}</span>
    </div>`).join('');
};

/** Альбомы друга: имя и сколько постов. */
App._friendCollections = function (body, colls) {
  if (!colls.length) {
    body.innerHTML = `<p class="profile-empty">${esc(t('friends.profileEmpty'))}</p>`;
    return;
  }
  body.innerHTML = colls.map((c) => `
    <div class="friend-coll-row">
      <span class="friend-coll-name">${esc(c.name)}</span>
      <span class="friend-coll-n">${esc(tf('friends.postsCount', { n: c.count }))}</span>
    </div>`).join('');
};

/** Сетка постов друга: лайки или дизлайки, с догрузкой по кнопке. */
App._friendPosts = async function (body, ids) {
  if (!ids.length) {
    body.innerHTML = `<p class="profile-empty">${esc(t('friends.profileEmpty'))}</p>`;
    return;
  }
  const st = this._friendProfile;
  const grid = document.createElement('div');
  // profile-thumbs, а не выдуманный класс: колонки и правила плиток заданы
  // именно для него, своими стилями мы бы получили несовместимую сетку.
  grid.className = 'profile-thumbs friend-post-grid';
  const more = document.createElement('button');
  more.className = 'btn btn-sm friend-profile-more';
  body.appendChild(grid);
  body.appendChild(more);

  const slice = ids.slice(0, st.shown);
  // Скелетоны на время загрузки: сетка не прыгает, когда плитки доедут.
  grid.innerHTML = slice.map(() => '<div class="pf-thumb-skeleton"></div>').join('');

  let res;
  try {
    res = await this._fetchPostsByIds(slice);
  } catch {
    grid.innerHTML = `<p class="profile-empty">${esc(t('friends.postsFailed'))}</p>`;
    more.classList.add('hidden');
    return;
  }
  // Пока грузили, пользователь мог уйти к другому другу: в чужую сетку не рисуем.
  if (!this._friendProfile || this._friendProfile.id !== st.id) return;
  grid.innerHTML = '';
  const frag = document.createDocumentFragment();
  res.posts.forEach((post) => frag.appendChild(this._friendTile(post)));
  // Посты, которых нет ни у нас, ни на источнике (удалён, CDN отдал 404):
  // показываем заглушку с id — видно, что запись учтена, а не молчаливая дыра.
  (res.unresolved || []).forEach((id) => {
    const tile = document.createElement('div');
    tile.className = 'pf-thumb pf-broken pf-missing';
    tile.title = tf('pf.postUnavailable', { id });
    const fb = document.createElement('div');
    fb.className = 'pf-fallback';
    fb.textContent = tf('pf.missingTile', { id });
    tile.appendChild(fb);
    frag.appendChild(tile);
  });
  grid.appendChild(frag);
  if (!res.posts.length && !(res.unresolved || []).length) {
    grid.innerHTML = `<p class="profile-empty">${esc(t('friends.postsNone'))}</p>`;
  }

  const left = ids.length - st.shown;
  more.classList.toggle('hidden', left <= 0);
  if (left > 0) {
    more.textContent = tf('friends.showMore', { n: Math.min(left, FRIEND_PAGE) });
    more.onclick = () => {
      st.shown += FRIEND_PAGE;
      this.renderFriendProfileTabs();
    };
  }
};

/**
 * Кандидаты превью по убыванию предпочтения. Логика повторяет профиль: у
 * лайков друга preview_url на CDN часто протухает, а локальная миниатюра или
 * оригинал живы, поэтому при ошибке загрузки перебираем следующего.
 */
const friendThumbCandidates = (post) => {
  const out = [];
  const push = (u) => { if (u && !out.includes(u)) out.push(u); };
  const proxy = (u) => `/api/proxy?url=${encodeURIComponent(u)}&kind=preview`;
  if (post.preview_url) push(proxy(post.preview_url));
  if (post.downloaded && post.thumb_path) push(`/api/thumb/${post.id}`);
  if (post.sample_url) push(proxy(post.sample_url));
  if (post.file_url) push(proxy(post.file_url));
  if (!out.length && post.downloaded) push(`/api/thumb/${post.id}`);
  return out;
};

/** Одна плитка поста в профиле друга: превью + открытие по клику. */
App._friendTile = function (post) {
  const tile = document.createElement('div');
  tile.className = 'pf-thumb';
  tile.tabIndex = 0;
  tile.dataset.pfId = String(post.id);
  const img = document.createElement('img');
  img.loading = 'lazy';
  img.alt = '';
  const srcs = friendThumbCandidates(post);
  if (srcs.length) img.src = srcs[0];
  tile.appendChild(img);
  // Теги в подсказке: так же, как в ленте и в своей вкладке лайков.
  if (post.tags) tile.title = post.tags;
  // Перебор кандидатов: превью с CDN могло протухнуть, а локальная миниатюра
  // жива. Без этого плитка друга молча осталась бы битой.
  let attempt = 0;
  img.addEventListener('error', () => {
    if (attempt >= srcs.length) {
      tile.classList.add('pf-broken');
      return;
    }
    attempt++;
    img.src = srcs[attempt];
  });
  const open = () => this.openPostById(post.id);
  tile.addEventListener('click', open);
  tile.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      open();
    }
  });
  return tile;
};

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
