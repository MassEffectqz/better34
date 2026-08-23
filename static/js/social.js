// social.js — комментарии к постам: мини-обсуждение между пользователями
// прямо во вьюере. Живое обновление через SSE (см. state.js, type=comment).
import { App } from './state.js';
import { esc, icon } from './utils.js';
import { API } from './api.js';

const MAX_LEN = 500;

App.renderComments = function (postId) {
  const host = this.els.viewerComments;
  if (!host) return;
  this._commentsPostId = postId;
  host.innerHTML =
    '<div class="vc-head">Обсуждение <span class="vc-count" id="vc-count"></span></div>' +
    '<div class="vc-list" id="vc-list"></div>' +
    '<form class="vc-form" id="vc-form">' +
    '<input type="text" id="vc-input" maxlength="' + MAX_LEN + '" placeholder="Написать комментарий..." autocomplete="off">' +
    '<button type="submit" class="btn-ss" title="Отправить">' + icon('arrowUp', 14, true) + '</button>' +
    '</form>';
  const input = host.querySelector('#vc-input');
  const form = host.querySelector('#vc-form');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const text = input.value.trim();
    if (!text || !App.state.viewerOpen) return;
    try {
      await API.post(`/comments/${postId}`, { text });
      input.value = '';
      App.loadComments(postId);
    } catch (err) {
      App.showToast(String(err && err.message || err).slice(0, 80), 'error');
    }
  });
  App.loadComments(postId);
};

App.loadComments = function (postId) {
  const list = document.getElementById('vc-list');
  if (!list || !this.state.viewerOpen) return;
  const me = this.state.user ? (this.state.user.username || '') : '';
  API.get('/comments/' + postId, { fresh: true }).then((d) => {
    // Пока грузились — пост уже сменился.
    if (this._commentsPostId !== postId || !this.state.viewerOpen) return;
    const comments = d && d.comments || [];
    const count = document.getElementById('vc-count');
    if (count) count.textContent = comments.length ? `(${comments.length})` : '';
    if (!comments.length) {
      list.innerHTML = '<div class="vc-empty">Комментариев пока нет — будьте первым</div>';
      return;
    }
    list.innerHTML = comments.map((cm) => {
      const mine = me && cm.username === me;
      const del = mine
        ? `<button type="button" class="vc-del" data-cid="${cm.id}" title="Удалить">${icon('x', 11)}</button>`
        : '';
      const author = cm.nickname && cm.nickname !== cm.username ? cm.nickname : cm.username;
      const avatar = cm.avatar
        ? `<img class="vc-avatar" src="${cm.avatar}" alt="">`
        : '<span class="vc-avatar vc-avatar-empty"></span>';
      return `<div class="vc-item${mine ? ' mine' : ''}">${avatar}` +
        `<div class="vc-body"><div class="vc-meta">${esc(author)} · <time>${esc(cm.created_at.slice(0, 16).replace('T', ' '))}</time>${del}</div>` +
        `<div class="vc-text">${esc(cm.text)}</div></div></div>`;
    }).join('');
    list.scrollTop = list.scrollHeight;
    list.querySelectorAll('.vc-del').forEach(btn => {
      btn.addEventListener('click', async () => {
        try { await API.del(`/comments/${btn.dataset.cid}`); App.loadComments(postId); } catch { /* noop */ }
      });
    });
  }).catch(() => {
    if (list) list.innerHTML = '<div class="vc-empty">Не удалось загрузить комментарии</div>';
  });
};

// SSE: кто-то (возможно с другого устройства) написал в текущий пост.
App._onCommentEvent = function (postId) {
  if (this.state.viewerOpen &&
      this.state.posts[this.state.viewerIndex] &&
      this.state.posts[this.state.viewerIndex].id === postId) {
    this.loadComments(postId);
  }
};
