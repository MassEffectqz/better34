// source_comments.js — комментарии поста, подтянутые с бура-источника
// (dapi s=comment). Рендерит их social.js: там же они сливаются с локальными
// в один список. Здесь только загрузка/кэш — без DOM.
import { App } from './state.js';
import { API } from './api.js';

const MAX_SRC_BODY = 2000;
App.SRC_COMMENT_MAX = MAX_SRC_BODY;

// Сайты, которые уже ответили «комментариев нет / сервис выключен»: кнопку
// загрузки для них больше не показываем, чтобы не спрашивать вхолостую.
App._srcUnsupported = new Set();

// ── Фоновая предзагрузка комментариев источника ───────────────────────────
// Источник сам сообщает в выдаче has_comments=true. Раз раз это известно,
// тянем комментарии в кэш заранее — к моменту открытия поста они уже на
// месте, и кнопка «Загрузить» обычно не нужна. Очередь строго
// последовательная с паузой: чужий сайт не любит частых запросов.
const PREFETCH = {
  delayMs: 4000, // пауза между запросами — не грузим чужой сайт чаще
  maxQueue: 40,  // больше в очередь не набираем
  maxTotal: 20,  // бюджет на сессию: столько постов тихо обогатим
};
App._srcPrefetch = { queue: [], done: 0, running: false };

// sourceSiteOf вытаскивает имя сайта-источника из поста. У постов из выдачи
// поле source пустое (заполняется при скачивании), поэтому сайт определяем по
// хосту медиа — это единственный надёжный признак в ленте.
App.sourceSiteOf = function (post) {
  if (!post) return '';
  if (post.source) return String(post.source).toLowerCase();
  const host = (() => {
    try { return new URL(post.file_url || post.preview_url || '').hostname; }
    catch { return ''; }
  })();
  if (!host) return '';
  const known = { 'gelbooru.com': 'gelbooru', 'rule34.xxx': 'rule34', 'safebooru.org': 'safebooru', 'hypnohub.net': 'hypnohub' };
  for (const [suffix, name] of Object.entries(known)) {
    if (host === suffix || host.endsWith('.' + suffix)) return name;
  }
  return '';
};

// hasSourceComments — показывать ли индикатор в ленте: источник сообщил о
// комментариях (has_comments) либо мы их уже загружали (comment_count).
App.hasSourceComments = function (post) {
  return !!(post && (post.has_comments || (post.comment_count || 0) > 0));
};

// loadSourceComments — комментарии источника для поста. Возвращает промис с
// {site, comments, count, cached, unsupported}; DOM не трогает.
//
// opts.refresh — только по явной кнопке «обновить»; иначе сервер отдаёт кэш,
// а если его нет — сам сходит на источник (обычный клик по «загрузить»).
// opts.cachedOnly — строго кэш (cached=1): при открытии поста. Сервер в этом
// режиме не ходит на бур даже при промахе — иначе каждый просмотр стоил бы
// запроса к чужому сайту.
App.loadSourceComments = function (postId, site, opts) {
  const o = opts || {};
  if (!site) return Promise.resolve({ site: '', comments: [], count: 0, unsupported: true });
  if (o.cachedOnly && this._srcUnsupported.has(site)) {
    return Promise.resolve({ site, comments: [], count: 0, unsupported: true });
  }
  const q = new URLSearchParams();
  q.set('site', site);
  q.set('source_id', String(postId));
  if (o.refresh) q.set('refresh', '1');
  if (o.cachedOnly) q.set('cached', '1');
  // this.API — точка подмены для тестов, по умолчанию реальный клиент.
  const api = this.API || API;
  return api.get('/posts/' + postId + '/source-comments?' + q.toString(), { fresh: true })
    .then((d) => {
      const data = d || {};
      if (data.unsupported) this._srcUnsupported.add(site);
      return {
        site: data.site || site,
        comments: data.comments || [],
        count: data.count || 0,
        cached: !!data.cached,
        unsupported: !!data.unsupported,
      };
    })
    .catch(() => ({ site, comments: [], count: 0, unsupported: false, error: true }));
};

// scheduleSourceCommentsPrefetch набирает в очередь посты из выдачи, у которых
// источник отметил has_comments. Уже закэшированные, посты с неизвестным
// сайтом и сайты, ответившие «не поддерживает», пропускаем.
App.scheduleSourceCommentsPrefetch = function (posts) {
  const st = this._srcPrefetch;
  if (!posts || !posts.length) return;
  if (typeof this.sourceSiteOf !== 'function' || typeof this.loadSourceComments !== 'function') return;
  const openId = this.state.viewerOpen && this.state.posts[this.state.viewerIndex]
    ? this.state.posts[this.state.viewerIndex].id : null;
  for (const p of posts) {
    if (!p || !p.id || !p.has_comments) continue;
    if ((p.comment_count || 0) > 0) continue;       // уже знаем число
    if (p.id === openId) continue;                 // открытый пост не трогаем
    const site = this.sourceSiteOf(p);
    if (!site || this._srcUnsupported.has(site)) continue;
    const key = site + ':' + p.id;
    if (st.seen && st.seen.has(key)) continue;
    if (st.queue.length >= PREFETCH.maxQueue) break;
    st.seen = st.seen || new Set();
    st.seen.add(key);
    st.queue.push({ id: p.id, site });
  }
  this._pumpSourceCommentsPrefetch();
};

// _pumpSourceCommentsPrefetch берёт по одному посту и тихо кладёт комментарии
// в серверный кэш. Повторная попытка не мешает: сервер отдаёт кэш сам, а сеть
// не трогает, если он уже есть.
App._pumpSourceCommentsPrefetch = function () {
  const st = this._srcPrefetch;
  if (st.running) return;
  if (st.done >= PREFETCH.maxTotal) { st.queue.length = 0; return; }
  const job = st.queue.shift();
  if (!job) return;
  // Без сети смысла нет: ждём, пока пользователь вернётся к серверу.
  if (typeof navigator !== 'undefined' && navigator.onLine === false) {
    st.queue.unshift(job);
    return;
  }
  st.running = true;
  this.loadSourceComments(job.id, job.site, {})
    .then((d) => {
      st.done++;
      if (d.unsupported) {
        // Сайт комментарии не отдаёт — выкидываем его из очереди целиком.
        this._srcUnsupported.add(job.site);
        st.queue = st.queue.filter((j) => j.site !== job.site);
      }
      const n = d.comments && d.comments.length ? (d.count || d.comments.length) : 0;
      if (n > 0) {
        const post = (this.state.posts || []).find((p) => p.id === job.id);
        if (post) {
          post.comment_count = n;
          if (typeof this.updateCardCommentsBadge === 'function') this.updateCardCommentsBadge(post);
        }
      }
    })
    .catch(() => { /* офлайн или сбой — просто пропускаем */ })
    .finally(() => {
      st.running = false;
      setTimeout(() => this._pumpSourceCommentsPrefetch(), PREFETCH.delayMs);
    });
};
