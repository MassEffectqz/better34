import { enqueueMutation } from './offline.js';
import { t } from './i18n.js';

export const API = {
  _cache: {},
  _inflight: {},
  _inflightCtrl: {},
  _inflightAborts: {},
  _cacheTTL: 60000,
  _cacheMax: 200,
  // Дедлайны сетевых запросов. Мобильная сеть (LTE/5G, переключение
  // Wi-Fi↔сотовая, сворачивание приложения) часто не отказывает, а подвешивает
  // соединение: без таймаута спиннер в UI остаётся навсегда. Мутации живут
  // дольше — там же по истечении дедлайна запрос НЕ уходит в оффлайн-очередь
  // (см. post/del/patch): сервер мог его уже применить.
  timeoutMs: 20000,
  mutationTimeoutMs: 30000,
  retryDelayMs: 500,
  maxRetries: 1,
  /** @type {string | null} */ _token: null,
  _err(r) {
    return r.text().then((raw) => {
      try {
        const j = JSON.parse(raw);
        const code = j.error || j.message || raw;
        // Translate server error codes via i18n
        const key = 'err.' + code;
        const translated = t(key);
        return translated !== key ? translated : code;
      } catch {
        return raw || `Код ${r.status}`;
      }
    });
  },
  /** @returns {Record<string, string>} */
  _headers() {
    const h = /** @type {Record<string, string>} */ ({});
    if (this._token === null) {
      const m = document.querySelector('meta[name="briefly-token"]');
      this._token = m ? m.getAttribute('content') || '' : '';
    }
    if (this._token) h['X-Briefly-Token'] = this._token;
    return h;
  },
  // Дедлайн запроса: по истечении прерываем fetch. Флаг timedOut отличает
  // наш таймаут от отмены вызывающим (см. _fetchGet/post/del/patch).
  _deadline(controller, ms) {
    // timer — any: setTimeout в браузере даёт number, в Node-типах Timeout.
    const st = { timedOut: false, timer: /** @type {any} */ (0) };
    st.timer = setTimeout(() => { st.timedOut = true; controller.abort(); }, ms);
    return st;
  },
  _timeoutError() {
    // Помечаем таймаут: оффлайн-очередь не должна переигрывать такой запрос.
    const e = /** @type {any} */ (new Error(t('err.timeout')));
    e.timeout = true;
    return e;
  },
  _sleep(ms) { return new Promise((r) => setTimeout(r, ms)); },
  /**
   * Общая часть мутаций: дедлайн + оффлайн-очередь при сетевом отказе.
   * queueBody — исходный объект (doFlush сам сериализует его при доставке).
   * Таймаут в очередь НЕ кладём: запрос мог дойти до сервера и примениться,
   * а повтор переключил бы лайк/скрытие обратно (см. /api/like/:id).
   * @param {string} method @param {string} url @param {Object} init @param {any} [queueBody]
   */
  async _mutate(method, url, init, queueBody) {
    const ctl = new AbortController();
    const dl = this._deadline(ctl, this.mutationTimeoutMs);
    try {
      const r = await fetch(`/api${url}`, { method, signal: ctl.signal, ...init });
      if (!r.ok) {
        // Помечаем ошибку статусом: 4xx — это ответ сервера «запрос неверен/
        // нет прав», повторять его бессмысленно, и главное — нельзя отдавать
        // его в оффлайн-очередь. Иначе настоящая ошибка маскируется под
        // «успешно отложено», а потом всплывает в общем виде «отклонено
        // сервером действий» (так и выглядело добавление друга: 400 уходило
        // в очередь и возвращало ok).
        const e = new Error(await this._err(r));
        /** @type {any} */ (e).status = r.status;
        throw e;
      }
      const ct = r.headers.get('content-type') || '';
      if (!ct.includes('application/json')) {
        console.error(`[API.${method}] Non-JSON response`, { url: `/api${url}`, status: r.status, contentType: ct });
        throw new Error(`Expected JSON response but got ${ct || 'unknown content type'} (${r.status})`);
      }
      return r.json();
    } catch (err) {
      if (err && err.name === 'AbortError' && dl.timedOut) throw this._timeoutError();
      // 4xx — сервер ответил и отказал: запрос не станет хороше от повтора,
      // поэтому в очередь его не кладём (иначе ошибка не видна вызывающему).
      const st = err && /** @type {any} */ (err).status;
      const clientError = typeof st === 'number' && st >= 400 && st < 500;
      if (!clientError && err && err.name !== 'AbortError' && !err.timeout && await enqueueMutation(method, url, queueBody)) {
        return { ok: true, offline: true };
      }
      throw err;
    } finally {
      clearTimeout(dl.timer);
    }
  },
  // Дедуп + разделяемая отмена: общий fetch прерывается ТОЛЬКО когда
  // отменились все вызывающие. Один abort не роняет остальных.
  _watchAbort(key, signal) {
    const ctl = this._inflightCtrl[key];
    if (!ctl || signal.aborted) return;
    const reg = this._inflightAborts[key] || (this._inflightAborts[key] = []);
    const onAbort = () => {
      const alive = reg.filter(w => w.signal !== signal && !w.signal.aborted);
      if (!alive.length) ctl.abort();
    };
    signal.addEventListener('abort', onAbort, { once: true });
    reg.push({ signal, onAbort });
  },
  _clearInflight(key, promise) {
    if (this._inflight[key] === promise) delete this._inflight[key];
    delete this._inflightCtrl[key];
    const reg = this._inflightAborts[key];
    if (reg) {
      delete this._inflightAborts[key];
      reg.forEach(w => w.signal.removeEventListener('abort', w.onAbort));
    }
  },
  get(e, opts) {
    const key = `GET:${e}`;
    const useCache = !(opts && opts.fresh);
    const cached = useCache ? this._cache[key] : null;
    if (cached && Date.now() - cached.ts < this._cacheTTL) return Promise.resolve(cached.data);
    if (this._inflight[key]) {
      if (opts && opts.signal) this._watchAbort(key, opts.signal);
      return this._inflight[key];
    }
    const controller = new AbortController();
    this._inflightCtrl[key] = controller;
    const promise = this._fetchGet(key, e, controller, useCache).finally(() => this._clearInflight(key, promise));
    this._inflight[key] = promise;
    if (opts && opts.signal) this._watchAbort(key, opts.signal);
    return promise;
  },
  async _fetchGet(key, e, controller, useCache) {
    // Мобильная сеть: соединение может «зависнуть» вместо отказа (LTE, смена
    // Wi-Fi↔LTE, спящий экран). Без дедлайна спиннер в UI висел бы вечно, а
    // одиночный сетевой сбой (TypeError) оставлял бы битую плитку навсегда.
    // Поэтому: дедлайн + один повтор, но только если сервер не ответил —
    // повторять 4xx/5xx и не-JSON бессмысленно.
    let attempt = 0;
    for (;;) {
      const dl = this._deadline(controller, this.timeoutMs);
      let answered = false;
      try {
        const r = await fetch(`/api${e}`, { signal: controller.signal, headers: this._headers() });
        answered = true;
        if (!r.ok) throw new Error(await this._err(r));
        const ct = r.headers.get('content-type') || '';
        if (!ct.includes('application/json')) {
          console.error('[API.get] Non-JSON response', { url: `/api${e}`, status: r.status, contentType: ct });
          throw new Error(`Expected JSON response but got ${ct || 'unknown content type'} (${r.status})`);
        }
        const data = await r.json();
        if (useCache !== false) this._setCache(key, data);
        return data;
      } catch (err) {
        if (dl.timedOut) throw this._timeoutError();
        if (err && err.name === 'AbortError') return;
        if (answered) throw err;
        if (attempt++ >= this.maxRetries) throw err;
        await this._sleep(this.retryDelayMs);
      } finally {
        clearTimeout(dl.timer);
      }
    }
  },
  _setCache(key, data) {
    const now = Date.now();
    this._cache[key] = { data, ts: now };
    let keys = Object.keys(this._cache);
    if (keys.length <= this._cacheMax) return;
    keys.filter(k => now - this._cache[k].ts >= this._cacheTTL).forEach(k => delete this._cache[k]);
    keys = Object.keys(this._cache);
    if (keys.length <= this._cacheMax) return;
    keys.sort((a, b) => (this._cache[a].ts || 0) - (this._cache[b].ts || 0));
    while (Object.keys(this._cache).length > this._cacheMax) delete this._cache[keys.shift()];
  },
  post(e, b, extraHeaders) {
    return this._mutate('POST', e, {
      headers: { 'Content-Type': 'application/json', ...this._headers(), ...(extraHeaders || {}) },
      body: JSON.stringify(b),
    }, b);
  },
  del(e) {
    return this._mutate('DELETE', e, { headers: this._headers() });
  },
  patch(e, b) {
    return this._mutate('PATCH', e, {
      headers: { 'Content-Type': 'application/json', ...this._headers() },
      body: JSON.stringify(b),
    }, b);
  },
  invalidate(pattern) { Object.keys(this._cache).forEach(k => { if (k.includes(pattern)) delete this._cache[k]; }); },
};
