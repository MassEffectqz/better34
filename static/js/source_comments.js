// source_comments.js — комментарии поста, подтянутые с бура-источника
// (dapi s=comment). Локальные комментарии (social.js) — отдельная сущность:
// здесь всё read-only, кэшируется на сервере, по кнопке.
import { App } from './state.js';
import { esc } from './utils.js';
import { API } from './api.js';
import { t, tf } from './i18n.js';

const MAX_SRC_BODY = 2000;

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

App.renderSourceComments = function (post) {
  const host = this.els.viewerSourceComments;
  if (!host) return;
  this._srcCommentsPostId = post.id;
  const site = this.sourceSiteOf(post);
  const count = post.comment_count || 0;
  const title = site ? tf('srcComments.title', { site }) : t('srcComments.titlePlain');
  const badge = this.hasSourceComments(post)
    ? ' <span class="vc-count" title="' + esc(t('srcComments.badge')) + '">' + (count ? '(' + count + ')' : '•') + '</span>'
    : '';
  host.className = 'viewer-comments src-comments';
  host.innerHTML =
    '<div class="vc-head">' + esc(title) + badge + '</div>' +
    '<div class="vc-list" id="vc-src-list"></div>' +
    '<div class="vc-src-actions">' +
    '<button type="button" class="btn-ss" id="vc-src-load">' + esc(t('srcComments.load')) + '</button>' +
    '<button type="button" class="btn-ss hidden" id="vc-src-refresh">' + esc(t('srcComments.refresh')) + '</button>' +
    '</div>';
  const list = host.querySelector('#vc-src-list');
  const loadBtn = host.querySelector('#vc-src-load');
  const refreshBtn = host.querySelector('#vc-src-refresh');
  const run = (refresh) => this.loadSourceComments(post.id, site, { refresh, list, refreshBtn });
  if (loadBtn) loadBtn.addEventListener('click', () => run(false));
  if (refreshBtn) refreshBtn.addEventListener('click', () => run(true));
  if (!site) {
    if (list) list.innerHTML = '<div class="vc-empty">' + esc(t('srcComments.noSource')) + '</div>';
    if (loadBtn) loadBtn.classList.add('hidden');
    return;
  }
  // Первичная проверка идёт из кэша (refresh=0) — источник не трогаем,
  // пока пользователь не нажмёт кнопку.
  this.loadSourceComments(post.id, site, { list, refreshBtn, passive: true });
};

App.loadSourceComments = function (postId, site, opts) {
  const o = opts || {};
  const list = o.list || document.getElementById('vc-src-list');
  if (!list) return;
  if (this._srcCommentsPostId !== postId || !this.state.viewerOpen) return;
  const q = new URLSearchParams();
  if (site) q.set('site', site);
  q.set('source_id', String(postId));
  // refresh=1 — только явное нажатие «обновить»; иначе берём кэш.
  if (o.refresh) q.set('refresh', '1');
  if (list) list.innerHTML = '<div class="vc-loading"><span class="pf-more-spin"></span> ' + esc(t('srcComments.loading')) + '</div>';
  const buttons = Array.from((list.parentElement || document).querySelectorAll('.vc-src-actions .btn-ss'));
  buttons.forEach((b) => { b.disabled = true; b.setAttribute('aria-busy', 'true'); });
  // this.API — точка подмены для тестов, по умолчанию реальный клиент.
  const api = this.API || API;
  api.get('/posts/' + postId + '/source-comments?' + q.toString(), { fresh: true })
    .then((d) => {
      if (this._srcCommentsPostId !== postId || !this.state.viewerOpen) return;
      const data = d || {};
      if (data.unsupported) {
        list.innerHTML = '<div class="vc-empty">' + esc(t('srcComments.unsupported')) + '</div>';
        if (o.refreshBtn) o.refreshBtn.classList.add('hidden');
        return;
      }
      const comments = data.comments || [];
      if (o.refreshBtn) o.refreshBtn.classList.remove('hidden');
      if (!comments.length) {
        list.innerHTML = '<div class="vc-empty">' + esc(t('srcComments.empty')) + '</div>';
        return;
      }
      list.innerHTML = comments.map((c) => {
        // Текст с чужого сайта: экранируем всё, внешние ссылки не рендерим.
        const body = String(c.body || '');
        const shown = body.length > MAX_SRC_BODY
          ? esc(body.slice(0, MAX_SRC_BODY)) + '…'
          : esc(body);
        const when = String(c.created_at || '').slice(0, 16).replace('T', ' ');
        return '<div class="vc-item"><div class="vc-body">' +
          '<div class="vc-meta">' + esc(c.author || '?') + (when ? ' · <time>' + esc(when) + '</time>' : '') + '</div>' +
          '<div class="vc-text">' + shown + '</div></div></div>';
      }).join('');
    })
    .catch(() => {
      if (this._srcCommentsPostId !== postId) return;
      list.innerHTML = '<div class="vc-empty">' + esc(t('srcComments.failed')) + '</div>';
    })
    .finally(() => {
      buttons.forEach((b) => { b.disabled = false; b.removeAttribute('aria-busy'); });
    });
};
