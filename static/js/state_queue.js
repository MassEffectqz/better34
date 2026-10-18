// state_queue.js — опрос очереди загрузок (SSE / status), модальное окно очереди и управление задачами.
import { App } from './state.js';
import { API } from './api.js';
import { t } from './i18n.js';
import { go, icon, esc } from './utils.js';

export const QueueMethods = {
  startDlPoll() {
    let lastDone = 0;
    let hideTimer = null;
    // Задача 3: уведомление, когда партия загрузок завершилась (см. notify.js).
    let wasActive = false;
    let dlErrors = 0;
    const hideProgress = () => {
      if (this.els && this.els.dlProgress) {
        this.els.dlProgress.classList.add('hidden');
      }
    };
    const updateFromStatus = (d) => {
      const activeCount = (d.queued || 0) + (d.active || 0);
      if (typeof navigator !== 'undefined' && 'setAppBadge' in navigator) {
        if (activeCount > 0) {
          navigator.setAppBadge(activeCount).catch(() => {});
        } else {
          navigator.clearAppBadge().catch(() => {});
        }
      }
      if (this.els.btnHeaderQueue) {
        if (activeCount > 0) {
          this.els.btnHeaderQueue.classList.remove('hidden');
          this.els.btnHeaderQueue.classList.add('active');
          if (this.els.headerQueueCount) this.els.headerQueueCount.textContent = String(activeCount);
        } else {
          this.els.btnHeaderQueue.classList.remove('active');
          this.els.btnHeaderQueue.classList.add('hidden');
        }
      }
      const total = (d.queued || 0) + (d.active || 0) + (d.done || 0);
      if ((d.queued || 0) + (d.active || 0) > 0) {
        if (this.els.dlProgress) this.els.dlProgress.classList.remove('hidden');
        clearTimeout(hideTimer);
        const pct = total > 0 ? (d.done || 0) / total * 100 : 0;
        if (this.els.dlProgressBar) this.els.dlProgressBar.style.width = `${Math.min(pct, 100)}%`;
        if (this.els.dlProgressText) this.els.dlProgressText.textContent = (d.active || 0) > 0 ? `${d.done || 0}/${total}` : `готово: ${d.done || 0}`;
        if ((d.done || 0) > lastDone && this.state.viewerOpen) {
          const p = this.state.posts[this.state.viewerIndex];
          if (p && (d.done_ids || []).includes(p.id) && this.els.viewerProgress) this.els.viewerProgress.textContent = t('viewer.downloaded');
        }
      } else if (total > 0) {
        // Партия завершилась (нет ни queued, ни active) — уведомляем (задача 3).
        if (wasActive && typeof this.notifyDownloadsDone === 'function' && this.notifyDownloadsDone(d.done || 0, dlErrors)) {
          clearTimeout(hideTimer);
          hideTimer = setTimeout(hideProgress, 3000);
        }
        wasActive = false;
        dlErrors = 0;
        if (this.els.dlProgress) this.els.dlProgress.classList.remove('hidden');
        if (this.els.dlProgressBar) this.els.dlProgressBar.style.width = '100%';
        if (this.els.dlProgressText) this.els.dlProgressText.textContent = `готово: ${d.done || 0}`;
        if ((d.done || 0) > lastDone && this.state.viewerOpen) {
          const p = this.state.posts[this.state.viewerIndex];
          if (p && (d.done_ids || []).includes(p.id) && this.els.viewerProgress) this.els.viewerProgress.textContent = t('viewer.downloaded');
        }
        clearTimeout(hideTimer);
        hideTimer = setTimeout(hideProgress, 3000);
      }
      wasActive = (d.queued || 0) + (d.active || 0) > 0;
      lastDone = d ? (d.done || 0) : 0;
    };
    const applyResult = (r) => {
      if (r.duplicate_of) {
        this.state.downloadQueue.delete(r.post_id);
        this.state.downloading.delete(r.post_id);
        this.showToast(`Пост ${r.post_id} — дубликат #${r.duplicate_of}, файл уже скачан`);
        return;
      }
      if (r.success) {
        const p = this.state.posts.find(x => x.id === r.post_id);
        if (p) {
          p.downloaded = true;
          const card = typeof this.getCardByIndex === 'function' ? this.getCardByIndex(this.state.posts.indexOf(p)) : null;
          if (card) card.classList.add('downloaded');
        }
      } else {
        this.state.downloadQueue.delete(r.post_id);
        this.state.downloading.delete(r.post_id);
        dlErrors++;
        this.showToast(`Ошибка скачивания поста ${r.post_id}: ${r.error || 'неизвестная'}`, 'error');
      }
    };
    // SSE: авторизация через cookie-сессию (EventSource шлёт куки сам).
    // Токен в query больше не подставляем: он попадал в access-логи,
    // а страница legacy-токен и так не получает (см. serveIndex).
    if (typeof EventSource !== 'undefined') {
      const es = new EventSource('/api/events');
      es.addEventListener('event', (ev) => {
        let d;
        try { d = JSON.parse(ev.data); } catch { return; }
        if (d.type === 'status') updateFromStatus(d);
        else if (d.type === 'result') applyResult(d);
        else if (d.type === 'comment' && this._onCommentEvent) this._onCommentEvent(d.post_id);
        else if (d.type === 'remote' && d.id > 0) this.onRemotePost(d.id);
        // События 'friends' больше нет: обмен с друзьями идёт только по запросу
        // (открытие профиля или кнопка «Обновить»), поэтому в фоне перерисовывать
        // профиль и вкладку «Друзья» нечего. Кто обменялся — тот и перерисует.
        else if (d.type === 'post_saved' && this.state.isLocal && this.state.autoRefreshFeed) {
          this.loadPosts(true);
        }
      });
      es.onerror = () => {  };
      this._sse = es;
    }
  },

  stopDlPoll() {
    if (this._sse) {
      try { this._sse.close(); } catch {}
      this._sse = null;
    }
  },

  async pollDlQueue() {
    try {
      const d = await API.get('/download/queue');
      return d;
    } catch (err) {
      console.error('pollDlQueue failed:', err);
      return null;
    }
  },

  async pauseDownloads() {
    await API.post('/download/pause');
    if (this.els.dlPause) this.els.dlPause.classList.add('hidden');
    if (this.els.dlResume) this.els.dlResume.classList.remove('hidden');
    this.showToast(t('dl.paused'));
  },

  async resumeDownloads() {
    await API.post('/download/resume');
    if (this.els.dlResume) this.els.dlResume.classList.add('hidden');
    if (this.els.dlPause) this.els.dlPause.classList.remove('hidden');
    this.showToast(t('dl.resumed'));
  },

  async showQueue() {
    if (this.state.viewerOpen) this.closeViewer();
    try {
      const d = await API.get('/download/queue');
      const active = d.active || [];
      const queue = d.queue || [];
      let html = '';
      if (active.length > 0) {
        html += `<div class="queue-active-info">Активны: ${active.join(', ')}</div>`;
      }
      if (queue.length === 0) {
        html += '<div class="queue-empty">Очередь пуста</div>';
      } else {
        // file_type экранируем (строка с сервера), действия — через
        // data-атрибуты и делегирование вместо inline onclick (CSP).
        html += queue.map(j =>
          `<div class="queue-item">
            <span class="queue-item-id">#${j.post_id}</span>
            <span class="queue-item-ext">${esc(j.file_type || '')}</span>
            <div class="queue-item-actions">
              <button data-qact="up" data-id="${j.post_id}" title="${esc(t('jobs.up'))}">${icon('chevronUp', 13)}</button>
              <button data-qact="down" data-id="${j.post_id}" title="${esc(t('jobs.down'))}">${icon('chevronDown', 13)}</button>
              <button data-qact="cancel" data-id="${j.post_id}" title="${esc(t('jobs.cancel'))}" style="color:var(--error)">${icon('x', 13)}</button>
            </div>
          </div>`
        ).join('');
      }
      this.els.queueBody.innerHTML = html;
      if (!this._queueBound) {
        this._queueBound = true;
        this.els.queueBody.addEventListener('click', (ev) => {
          const btn = ev.target.closest('button[data-qact]');
          if (!btn) return;
          const id = parseInt(btn.dataset.id, 10);
          if (btn.dataset.qact === 'up') go(this.queueMoveUp(id));
          else if (btn.dataset.qact === 'down') go(this.queueMoveDown(id));
          else if (btn.dataset.qact === 'cancel') go(this.queueCancel(id));
        });
      }
      this.els.queueModal.classList.remove('hidden');
    } catch (err) { this.showToast(`Ошибка: ${err.message}`, 'error'); }
  },

  hideQueue() {
    if (this.els && this.els.queueModal) {
      this.els.queueModal.classList.add('hidden');
    }
  },

  async queueCancel(id) {
    await API.post(`/download/cancel/${id}`);
    this.showToast(`Загрузка #${id} отменена`);
    this.showQueue();
  },

  async queueMoveUp(id) {
    await API.post(`/download/move-up/${id}`);
    this.showQueue();
  },

  async queueMoveDown(id) {
    await API.post(`/download/move-down/${id}`);
    this.showQueue();
  },
};

export function extendQueue(targetApp) {
  Object.assign(targetApp, QueueMethods);
  return targetApp;
}

export { App };

export const startDlPoll = QueueMethods.startDlPoll;
export const stopDlPoll = QueueMethods.stopDlPoll;
export const pollDlQueue = QueueMethods.pollDlQueue;
export const showQueue = QueueMethods.showQueue;
export const hideQueue = QueueMethods.hideQueue;
export const queueCancel = QueueMethods.queueCancel;
export const queueMoveUp = QueueMethods.queueMoveUp;
export const queueMoveDown = QueueMethods.queueMoveDown;
export const pauseDownloads = QueueMethods.pauseDownloads;
export const resumeDownloads = QueueMethods.resumeDownloads;
