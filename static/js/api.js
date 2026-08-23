export const API = {
  _cache: {},
  _inflight: {},
  _inflightCtrl: {},
  _inflightAborts: {},
  _cacheTTL: 60000,
  _cacheMax: 200,
  _token: null,
  _err(r) {
    return r.text().then((t) => {
      try {
        const j = JSON.parse(t);
        return j.error || j.message || t;
      } catch {
        return t || `Код ${r.status}`;
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
    try {
      const r = await fetch(`/api${e}`, { signal: controller.signal, headers: this._headers() });
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
      if (err.name === 'AbortError') return;
      throw err;
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
  async post(e, b, extraHeaders) { const r = await fetch(`/api${e}`, { method:'POST', headers:{'Content-Type':'application/json', ...this._headers(), ...(extraHeaders || {})}, body:JSON.stringify(b) }); if (!r.ok) throw new Error(await this._err(r)); const ct = r.headers.get('content-type') || ''; if (!ct.includes('application/json')) { console.error('[API.post] Non-JSON response', { url: `/api${e}`, status: r.status, contentType: ct }); throw new Error(`Expected JSON response but got ${ct || 'unknown content type'} (${r.status})`); } return r.json(); },
  async del(e) { const r = await fetch(`/api${e}`, { method:'DELETE', headers:this._headers() }); if (!r.ok) throw new Error(await this._err(r)); const ct = r.headers.get('content-type') || ''; if (!ct.includes('application/json')) { console.error('[API.del] Non-JSON response', { url: `/api${e}`, status: r.status, contentType: ct }); throw new Error(`Expected JSON response but got ${ct || 'unknown content type'} (${r.status})`); } return r.json(); },
  async patch(e, b) { const r = await fetch(`/api${e}`, { method:'PATCH', headers:{'Content-Type':'application/json', ...this._headers()}, body:JSON.stringify(b) }); if (!r.ok) throw new Error(await this._err(r)); const ct = r.headers.get('content-type') || ''; if (!ct.includes('application/json')) { console.error('[API.patch] Non-JSON response', { url: `/api${e}`, status: r.status, contentType: ct }); throw new Error(`Expected JSON response but got ${ct || 'unknown content type'} (${r.status})`); } return r.json(); },
  invalidate(pattern) { Object.keys(this._cache).forEach(k => { if (k.includes(pattern)) delete this._cache[k]; }); },
};
