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
