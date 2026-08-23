App._suggestSeq = 0;

App.onSearchInput = function () {
  const q = this.els.searchInput.value;
  if (this.state.recommendActive && q !== this.state.query) {
    this.state.recommendActive = false;
    this.renderModeBar();
  }
  this.state.query = q;
  this.els.searchClear.classList.toggle('visible', q.length > 0);
  // Подсказки — по последнему слову сегмента (после пробела или |).
  const seg = q.split('|').pop();
  const tailMatch = seg.match(/(\S+)$/);
  const tail = tailMatch ? tailMatch[1] : '';
  this._suggTailStart = tail ? q.length - tail.length : -1;
  clearTimeout(this._suggestTimer);
  if (tail.length >= 2) {
    this.hideHistory();
    this._suggestTimer = setTimeout(() => this.fetchSuggestions(tail), 200);
  } else {
    this._suggestSeq++;
    this.renderSuggestions([], false);
    if (q.length === 0 && this.els.searchInput === document.activeElement) this.showHistory();
  }
  clearTimeout(this._searchTimer);
  const nextQuery = q.trim();
  // Повторный поиск того же запроса не запускаем (набрали и стёрли символ).
  this._searchTimer = setTimeout(() => { if (nextQuery !== this._lastSearchQuery) this.search(nextQuery); }, 300);
  this.updateQueryMeta(q);
  this.renderQueryChips(q);
};

// Подстановка выбранного тега на место подсказанного хвоста слова.
App.applySuggestion = function (value) {
  const inp = this.els.searchInput;
  const pos = this._suggTailStart >= 0 ? this._suggTailStart : inp.value.length;
  inp.value = (inp.value.slice(0, pos) + value + ' ').replace(/\s{2,}/g, ' ');
  this.onSearchInput();
};

App.fetchSuggestions = function (tail) {
  const seq = ++this._suggestSeq;
  API.get(`/suggest-local?q=${encodeURIComponent(tail)}`).then(d => {
    if (this._suggestSeq !== seq) return;
    const local = d.tags || [];
    API.get(`/suggest?q=${encodeURIComponent(tail)}`).then(r => {
      if (this._suggestSeq !== seq) return;
      const merged = this.mergeSuggestions(local, r.tags || []);
      this.renderSuggestions(merged);
    }).catch(() => { if (this._suggestSeq === seq) this.renderSuggestions(local); });
  }).catch(() => {
    if (this._suggestSeq !== seq) return;
    API.get(`/suggest?q=${encodeURIComponent(tail)}`).then(d => { if (this._suggestSeq === seq) this.renderSuggestions(d.tags || []); }).catch(() => {});
  });
};

// Оценка: сколько терминов уйдёт на сервер, сколько — в локальную
// фильтрацию (бюджет MaxQueryLen активного провайдера).
App.updateQueryMeta = function (q) {
  const el = this.els.queryMeta;
  if (!el) return;
  const query = q.trim();
  if (!query) { el.classList.remove('visible'); return; }
  let terms = query.split(/\s+/).filter(Boolean).length;
  const hidden = (this.state.profile && this.state.profile.hidden_tags) || [];
  let budget = (this.state.maxQueryLen || 3800) - query.length;
  let local = 0;
  for (const t of hidden) {
    if (budget - (t.length + 2) >= 0) { budget -= t.length + 2; }
    else { local++; }
  }
  if (!hidden.length && terms <= 1) { el.classList.remove('visible'); return; }
  const total = terms + hidden.length;
  el.textContent = local > 0 ? `${total} · ${local} лок.` : `${total}`;
  el.title = local > 0
    ? `Сервер получит ${total - local} тегов, остальные ${local} отфильтруются локально`
    : `Всего тегов: ${total} (в лимите источника)`;
  el.classList.add('visible');
};

App.mergeSuggestions = function (local, remote) {
  const byValue = new Map();
  for (const t of remote) byValue.set(t.value || t, t);
  const out = [];
  const seen = new Set();
  for (const t of local) {
    const key = t.value || t;
    if (seen.has(key)) continue;
    seen.add(key);
    const rem = byValue.get(key);
    out.push(rem && rem.count ? rem : t);
  }
  for (const t of remote) {
    const key = t.value || t;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(t);
  }
  return out;
};

App.onSearchKeydown = function (e) {
  const suggOpen = this.els.suggestions.classList.contains('active');
  const histOpen = this.els.historyDropdown.classList.contains('active');

  // Стрелки по истории, когда подсказки не открыты.
  if (!suggOpen && histOpen && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
    e.preventDefault();
    const items = [...this.els.historyDropdown.querySelectorAll('.history-item[data-q]')];
    if (!items.length) return;
    let idx = items.findIndex(i => i.classList.contains('active'));
    idx = e.key === 'ArrowDown' ? Math.min(idx + 1, items.length - 1) : Math.max(idx - 1, 0);
    items.forEach(i => i.classList.remove('active'));
    items[idx].classList.add('active');
    items[idx].scrollIntoView({ block: 'nearest' });
    return;
  }
  if (!suggOpen && histOpen && e.key === 'Enter') {
    e.preventDefault();
    const act = this.els.historyDropdown.querySelector('.history-item.active');
    if (act) act.click();
    return;
  }

  const items = this.els.suggestions.querySelectorAll('.suggestion-item');
  const active = this.els.suggestions.querySelector('.suggestion-item.active');
  let idx = Array.from(items).indexOf(active);
  if (e.key === 'ArrowDown') { e.preventDefault(); idx = Math.min(idx + 1, items.length - 1); items.forEach(i => i.classList.remove('active')); if (items[idx]) items[idx].classList.add('active'); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); idx = Math.max(idx - 1, 0); items.forEach(i => i.classList.remove('active')); if (items[idx]) items[idx].classList.add('active'); }
  else if (e.key === 'Enter') {
    e.preventDefault();
    if (active) {
      const labelSpan = active.querySelector('span');
      const v = active.dataset.value || (labelSpan ? labelSpan.textContent : active.textContent);
      this.renderSuggestions([]);
      this.applySuggestion(v);
      this.search(this.els.searchInput.value.trim());
    }
    else { this.renderSuggestions([]); this.search(this.els.searchInput.value.trim()); }
  }   else if (e.key === 'Escape') {
    if (!this.els.suggestions.classList.contains('active') && !this.els.historyDropdown.classList.contains('active')) {
      // Сброс только при непустом вводе; search() отменит отложенный debounce,
      // иначе «призрачный» таймер вернул бы старый запрос после очистки.
      if (this.els.searchInput.value) {
        this.els.searchInput.value = ''; this.state.query = '';
        this.els.searchClear.classList.remove('visible');
        this.updateQueryMeta('');
        this.renderQueryChips('');
        this.search('');
      }
      this.els.searchInput.blur();
      return;
    }
    this.renderSuggestions([]); this.hideHistory();
  }
};

App.renderSuggestions = function (tags) {
  const el = this.els.suggestions;
  el.innerHTML = '';
  if (!tags.length) { el.classList.remove('active'); return; }
  el.classList.add('active');
  tags.forEach(tag => {
    const label = tag.label || tag.value || tag;
    const value = tag.value || tag;
    const div = document.createElement('div');
    div.className = 'suggestion-item'; div.dataset.value = value;
    const span = document.createElement('span');
    span.textContent = label;
    div.appendChild(span);
    if (tag.count) {
      const cnt = document.createElement('span');
      cnt.className = 'suggestion-count'; cnt.textContent = tag.count.toLocaleString('ru-RU');
      div.appendChild(cnt);
    }
    div.addEventListener('click', () => {
      this.renderSuggestions([]);
      this.applySuggestion(value);
      this.search(this.els.searchInput.value.trim());
    });
    el.appendChild(div);
  });
};

// Чипы разобранного запроса: клик × убирает тег, минус-теги подсвечены.
App.renderQueryChips = function (q) {
  const el = this.els.searchChips || document.getElementById('search-chips');
  if (!el) return;
  el.innerHTML = '';
  const query = (q || '').trim();
  if (!query) return;

  const groups = query.split('|').map(s => s.trim().split(/\s+/).filter(Boolean));
  let tokenIndex = 0;
  groups.forEach((tokens, gi) => {
    tokens.forEach(tok => {
      if (tokenIndex === 0 && gi > 0) {
        const sep = document.createElement('span');
        sep.className = 'qc-sep'; sep.textContent = '|';
        el.appendChild(sep);
      }
      const neg = tok.startsWith('-');
      const chip = document.createElement('span');
      chip.className = 'qc' + (neg ? ' neg' : '');
      const label = document.createElement('span');
      label.textContent = neg ? tok.slice(1) : tok;
      chip.appendChild(label);
      const x = document.createElement('button');
      x.type = 'button'; x.className = 'qc-x'; x.textContent = '×';
      x.setAttribute('aria-label', 'Убрать тег');
      const idx = tokenIndex;
      x.addEventListener('click', () => this.removeChip(idx));
      chip.appendChild(x);
      el.appendChild(chip);
      tokenIndex++;
    });
  });
};

// Удалить i-й токен запроса (обход всех чипов подряд).
App.removeChip = function (removeIdx) {
  const q = this.els.searchInput.value.trim();
  let i = 0;
  const next = q.split('|').map(seg => {
    const toks = seg.trim().split(/\s+/).filter(Boolean);
    const kept = [];
    for (const t of toks) { if (i !== removeIdx) kept.push(t); i++; }
    return kept.join(' ');
  }).filter(s => s.length).join('|');
  this.els.searchInput.value = next;
  this.onSearchInput();
};

App.showHistory = function () {
  const h = getHistory();
  if (!h.length || this.els.searchInput.value.trim()) return;
  const el = this.els.historyDropdown;
  this._histIdx = -1;
  // Закреплённые сверху, далее по частоте.
  const sorted = [...h].sort((a, b) => (b.pin - a.pin) || (b.count - a.count));
  el.innerHTML = '';
  el.classList.add('active');
  sorted.forEach(entry => {
    const d = document.createElement('div');
    d.className = 'history-item';
    d.dataset.q = entry.q;

    if (entry.pin) {
      const pm = document.createElement('span');
      pm.className = 'pin-mark'; pm.innerHTML = icon('bookmark', 13);
      d.appendChild(pm);
    }
    const lbl = document.createElement('span');
    lbl.className = 'history-label'; lbl.textContent = entry.q;
    d.appendChild(lbl);
    if (entry.count > 1) {
      const c = document.createElement('span');
      c.className = 'history-count'; c.textContent = entry.count; c.title = `${entry.count} поисков`;
      d.appendChild(c);
    }
    const pin = document.createElement('button');
    pin.type = 'button';
    pin.className = 'history-del hist-pin' + (entry.pin ? ' pinned' : '');
    pin.innerHTML = icon('bookmark', 13);
    pin.title = entry.pin ? 'Снять закреп' : 'Закрепить запрос';
    pin.addEventListener('click', (e) => { e.stopPropagation(); togglePinHistory(entry.q); this.showHistory(); });
    d.appendChild(pin);
    const del = document.createElement('button');
    del.type = 'button'; del.className = 'history-del'; del.innerHTML = icon('x', 13); del.title = 'Удалить';
    del.addEventListener('click', (e) => { e.stopPropagation(); removeHistory(entry.q); this.showHistory(); });
    d.appendChild(del);

    d.addEventListener('click', () => {
      el.classList.remove('active');
      this.els.searchInput.value = entry.q;
      this.onSearchInput();
      this.search(entry.q);
    });
    el.appendChild(d);
  });
  const clear = document.createElement('div');
  clear.className = 'history-item history-clear';
  clear.innerHTML = `${icon('x', 14)}<span style="color:var(--text-dim)">Очистить (закреплённые останутся)</span>`;
  clear.addEventListener('click', (e) => { e.stopPropagation(); clearHistory(); el.classList.remove('active'); });
  el.appendChild(clear);
};

App.hideHistory = function () { this.els.historyDropdown.classList.remove('active'); };

App.suggestProfileTag = function (type) {
  const input = type === 'fav' ? this.els.favTagInput : this.els.hiddenTagInput;
  const el = type === 'fav' ? this.els.favSuggestions : this.els.profileSuggestions;
  const q = input.value.trim();
  if (q.length < 2) {
    this._suggestSeq++;
    el.classList.remove('active');
    el.innerHTML = '';
    return;
  }
  const seq = ++this._suggestSeq;
  API.get(`/suggest?q=${encodeURIComponent(q)}`).then(d => {
    if (this._suggestSeq !== seq) return; // устаревший ответ — набрали другой тег
    el.innerHTML = '';
    if (!d.tags || !d.tags.length) { el.classList.remove('active'); return; }
    el.classList.add('active');
    d.tags.forEach(tag => {
      const label = tag.label || tag.value || tag;
      const value = tag.value || tag;
      const div = document.createElement('div');
      div.className = 'suggestion-item'; div.textContent = label;
      div.addEventListener('click', () => {
        input.value = value;
        el.classList.remove('active');
        this.addTag(type);
      });
      el.appendChild(div);
    });
  }).catch(() => {});
};

App.search = async function (query) {
  // Явный поиск (Enter/подсказка/чип/история) отменяет отложенный debounce,
  // чтобы один и тот же запрос не ушёл на сервер дважды.
  clearTimeout(this._searchTimer);
  this._lastSearchQuery = query;
  if (this.state.recommendActive) {
    this.state.recommendActive = false;
    this.renderModeBar();
  }
  if (query && query !== this._lastHistoryQuery) {
    this._lastHistoryQuery = query;
    addHistory(query);
  }
  this.state.query = query;
  this.state.isLocal = false;
  if (query) this._clearFeedCache();
  this.els.btnLocal.innerHTML = icon('house', 18);
  this.pushState(query, null);
  this.renderFilterChips();
  if (!query) { await this.loadPosts(true); return; }
  await this.loadPosts(true);
};

// Логотип → на главную: сброс режимов, поиска и роута.
App.goHome = function () {
  this.state.isLocal = false;
  if (this.els.btnLocal) this.els.btnLocal.innerHTML = icon('house', 18);
  if (this.state.viewerOpen) { this.closeViewer(); }
  if (this.els.searchInput.value) {
    this.els.searchInput.value = '';
    this.state.query = '';
    this.els.searchClear.classList.remove('visible');
    this.updateQueryMeta('');
    this.renderQueryChips('');
  }
  return this.search('');
};

App.toggleLocal = function () {
  this.state.isLocal = !this.state.isLocal;
  this.els.btnLocal.innerHTML = this.state.isLocal
    ? icon('folder', 18)
    : icon('house', 18);
  this.loadPosts(true);
  this.els.searchInput.focus();
};
