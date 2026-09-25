import { App } from './state.js';
import { icon, getHistory, addHistory, removeHistory, togglePinHistory, clearHistory } from './utils.js';
import { API } from './api.js';
App._suggestSeq = 0;
App._profileSuggestSeq = 0;
App._nlSearching = false;
App._suggTailStart = -1;
App._suggTailEnd = -1;
App._suggPrefix = '';

// Разделители тегов в строке поиска: пробел и «|» (ленты-чередования).
const SUGG_BREAK = ch => ch === ' ' || ch === '\t' || ch === '\n' || ch === '|';
// Служебные префиксы booru-синтаксиса: -tag (исключить), +tag (обязательно),
// ~tag (или). Это часть ввода, а не имени тега, — в запрос автодополнения
// такие символы попадать не должны, иначе подсказки по -tag пусты.
const SUGG_OP_RE = /^[-+~^]+/;

// Активное слово считаем ПО КАРЕТКЕ, а не по последнему слову строки.
// Иначе при правке тега в середине запроса подсказки приходят для чужого
// слова, а подстановка съедает весь хвост после каретки.
App.suggestWord = function (inp) {
  const v = inp.value;
  const pos = typeof inp.selectionStart === 'number' ? inp.selectionStart : v.length;
  let start = Math.max(0, Math.min(pos, v.length));
  let end = start;
  while (start > 0 && !SUGG_BREAK(v[start - 1])) start--;
  while (end < v.length && !SUGG_BREAK(v[end])) end++;
  const raw = v.slice(start, end);
  const m = raw.match(SUGG_OP_RE);
  const prefix = m ? m[0] : '';
  return { start, end, raw, prefix, word: raw.slice(prefix.length) };
};

App.onSearchInput = function () {
  const q = this.els.searchInput.value;
  if (this.state.recommendActive && q !== this.state.query) {
    this.state.recommendActive = false;
    this.renderModeBar();
  }
  this.state.query = q;
  this.els.searchClear.classList.toggle('visible', q.length > 0);
  // Подсказки — по слову под кареткой (границы запоминаем для подстановки).
  const w = this.suggestWord(this.els.searchInput);
  this._suggTailStart = w.start;
  this._suggTailEnd = w.end;
  this._suggPrefix = w.prefix;
  const tail = w.word;
  // NL-запрос («?котики в шляпах») — фраза для модели, а не тег: теги по ней
  // предлагать бессмысленно, поэтому автодополнение не включаем.
  const isNL = q.trimStart().startsWith('?');
  clearTimeout(this._suggestTimer);
  if (tail.length >= 2 && !isNL) {
    this.hideHistory();
    this._suggestTimer = setTimeout(() => this.fetchSuggestions(tail), 200);
  } else {
    this._suggestSeq++;
    this.renderSuggestions([], false);
    if (q.length === 0 && this.els.searchInput === document.activeElement) this.showHistory();
  }
  clearTimeout(this._searchTimer);
  const nextQuery = q.trim();
  // AI-запросы (?...) ждут 1.2с, обычные — 300мс.
  const delay = isNL ? 1200 : 300;
  // Повторный поиск того же запроса не запускаем (набрали и стёрли символ).
  this._searchTimer = setTimeout(() => { if (nextQuery !== this._lastSearchQuery) this.search(nextQuery); }, delay);
  this.updateQueryMeta(q);
  this.renderQueryChips(q);
};

// Подстановка выбранного тега вместо слова под кареткой. Заменяем ровно
// диапазон [start,end), а не «до конца строки»: иначе выбор подсказки в
// середине запроса стирал последующие теги. Префикс (-, ~, …) сохраняем.
App.applySuggestion = function (value) {
  const inp = this.els.searchInput;
  const v = inp.value;
  let start = this._suggTailStart;
  let end = this._suggTailEnd;
  if (!(start >= 0 && end > start)) { start = v.length; end = v.length; }
  const raw = v.slice(start, end);
  const prefix = (raw.match(SUGG_OP_RE) || [''])[0];
  const after = v.slice(end);
  const next = (v.slice(0, start) + prefix + value + (after ? ' ' + after : ' '))
    .replace(/\s{2,}/g, ' ');
  inp.value = next;
  // Каретка — за подставленным тегом, чтобы продолжить ввод с него.
  if (typeof inp.setSelectionRange === 'function') {
    const pos = (inp.value.indexOf(value, start) + value.length) || next.length;
    try { inp.setSelectionRange(pos, pos); } catch { /* некоторые типы input не поддерживают */ }
  }
  this.onSearchInput();
};

// suggestRenderDelayMs — сколько ждём удалённый источник, прежде чем
// показать локальные подсказки. Локальная база (SQLite) отвечает почти
// мгновенно, поэтому пауза нужна только чтобы дать догнать /suggest.
const SUGGEST_RENDER_DELAY_MS = 120;

App.fetchSuggestions = function (tail) {
  const seq = ++this._suggestSeq;
  // Раньше список рендерился дважды: сначала по локальным счётчикам, затем
  // по удалённым. Из-за этого подсказки на глазах переставлялись, а числа
  // менялись (2 → 90000) — выглядело как конфликт двух разных подсказок.
  const localP = API.get(`/suggest-local?q=${encodeURIComponent(tail)}`)
    .then(d => d.tags || [])
    .catch(() => []);
  const remoteP = API.get(`/suggest?q=${encodeURIComponent(tail)}`)
    .then(r => r.tags || [])
    .catch(() => null); // null = удалённый источник недоступен

  // Гонка: обычно выигрывают оба источника — тогда список рендерится один
  // раз. Если /suggest не ответил за SUGGEST_RENDER_DELAY_MS, показываем
  // локальные подсказки, НЕ закрывая гонку: когда удалённый ответ всё же
  // придёт, он дорисуется поверх.
  //
  // Раньше таймер ставил settled = true, и удалённый ответ отбрасывался
  // навсегда. На практике популярнейший тег (umamusume по «umamu») не
  // показывался, пока пользователь не вводил его целиком: в списке
  // оставались только теги из локальной базы, где umamusume не значился.
  const timer = setTimeout(() => {
    if (this._suggestSeq !== seq) return;
    localP.then(local => {
      if (this._suggestSeq !== seq || !local.length) return;
      this.renderSuggestions(this.mergeSuggestions(local, [], tail));
    });
  }, SUGGEST_RENDER_DELAY_MS);

  Promise.all([localP, remoteP]).then(([local, remote]) => {
    if (this._suggestSeq !== seq) return;
    clearTimeout(timer);
    // Удалённый — источник правды по счётчикам, даже если список уже был
    // показан по таймеру.
    this.renderSuggestions(this.mergeSuggestions(local, remote || [], tail));
  });
};

// Оценка: сколько терминов уйдёт на сервер, сколько — в локальную
// фильтрацию (бюджет MaxQueryLen активного провайдера).
App.updateQueryMeta = function (q) {
  const el = this.els.queryMeta;
  if (!el) return;
  const query = q.trim();
  if (!query) { el.classList.remove('visible'); return; }
  const terms = query.split(/\s+/).filter(Boolean).length;
  const hidden = (this.state.profile && this.state.profile.hidden_tags) || [];
  // Сервер prepend'ит rating-метатеги к запросу (в первую | группу) до
  // подсчёта бюджета MaxQueryLen — учитываем. Бюджет применяется к каждой
  // группе отдельно, поэтому берём худшую группу: длина группы + метатеги
  // (для первой) вместо длины всего запроса.
  const ratingTerms = {
    sfw: ['-rating:explicit', '-rating:questionable', '-rating:sensitive'],
    nsfw: ['-rating:general', '-rating:safe'],
  };
  const rt = ratingTerms[this.state.ratingFilter] || [];
  const ratingLen = rt.reduce((n, t) => n + t.length + 1, 0); // тег + пробел
  const groups = query.split('|').map(s => s.trim()).filter(Boolean);
  let worstLen = 0;
  groups.forEach((g, i) => {
    worstLen = Math.max(worstLen, g.length + (i === 0 ? ratingLen : 0));
  });
  // U10: budget не может быть отрицательным — иначе hidden-теги не будут учтены.
  let budget = Math.max(0, (this.state.maxQueryLen || 3800) - worstLen);
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

// suggTier — релевантность тега к набранному префиксу; чем меньше, тем выше
// в списке. Держим в паре с suggestionTier (internal/rule34.go): сервер
// ранжирует удалённые подсказки, клиент сводит их с локальными — правила
// должны совпадать, иначе порядок «прыгает» при подливании локальных.
//
// 0 — точное совпадение, 1 — префикс, 2 — совпадение на границе слова
// после «_», 3 — вхождение в середине имени, -1 — не подходит вовсе.
const suggTier = (value, prefix) => {
  if (!prefix) return 0;
  const v = String(value || '').toLowerCase();
  if (v === prefix) return 0;
  if (v.startsWith(prefix)) return 1;
  const i = v.indexOf(prefix);
  if (i > 0) return v[i - 1] === '_' ? 2 : 3;
  return -1;
};

// Слияние локальных и удалённых подсказок + ранжирование.
//
// Счётчики несопоставимы: локальный — посты в твоей базе (единицы), удалённый
// — посты на всём сайте (тысячи). Поэтому сортируем по удалённому счётчику,
// а локальный берём только там, где удалённого нет.
//
// Порядок: релевантность (suggTier) → популярность → короче имя → алфавит.
// SUGGEST_MAX — сколько подсказок показываем. Раньше список не ограничивался
// вообще: на короткий префикс («uma») приходило 92 тега, и нужный «umamusume»
// уезжал за пределы видимой части выпадающего списка. Плюс в списке оказывались
// локальные совпадения по границе слова (doma_umaru, himouto!_umaru-chan), хотя
// пользователь набирал префикс с начала.
const SUGGEST_MAX = 12;

App.mergeSuggestions = function (local, remote, prefix) {
  const pref = (prefix || '').toLowerCase();
  const norm = t => (t && t.value ? t.value : t || '').toString().trim();
  const out = [];
  const seen = new Set();
  const add = t => {
    const k = norm(t).toLowerCase();
    if (!k || seen.has(k)) return;
    if (suggTier(k, pref) < 0) return;
    seen.add(k);
    out.push(t);
  };
  // Удалённые идут первыми: по ним счётчики популярности, по локальным — нет.
  for (const t of remote) add(t);
  for (const t of local) add(t);

  // Удалённый счётчик побеждает локальный: он отражает популярность на
  // сайте, локальный же — сколько постов скачал лично ты. Он же используется
  // для показа в подсказке, иначе у общего тега висел бы счётчик «1».
  const remoteCount = new Map();
  for (const t of remote) {
    const k = norm(t).toLowerCase();
    if (k && !remoteCount.has(k)) remoteCount.set(k, Number(t.count) || 0);
  }
  const knownToSite = t => remoteCount.has(norm(t).toLowerCase());
  const countOf = t => {
    const k = norm(t).toLowerCase();
    return remoteCount.has(k) ? remoteCount.get(k) : (Number(t.count) || 0);
  };
  for (const t of out) {
    const k = norm(t).toLowerCase();
    const rc = remoteCount.get(k);
    if (rc) t.count = rc;
    // Помечаем источник счётчика: локальный «4» и удалённый «4» — разные вещи.
    t.local = rc === undefined;
  }

  // Шкалы несопоставимы: локальный счётчик — это посты в твоей базе, удалённый
  // — посты на всём сайте. Раньше они сравнивались напрямую, и тег, который
  // сайт не отдал (gelbooru режет выдачу до 100 записей), оказывался в списке
  // с счётчиком «4» — 4 скачанных поста — рядом с «364» постов на сайте.
  // Поэтому теги, известные сайту, всегда идут выше локальных-only, а внутри
  // каждой группы сортируем по своему счётчику.
  return out
    .map((t, i) => ({ t, i }))
    .sort((a, b) => suggTier(norm(a.t), pref) - suggTier(norm(b.t), pref)
      || (knownToSite(b.t) ? 1 : 0) - (knownToSite(a.t) ? 1 : 0)
      || countOf(b.t) - countOf(a.t)
      || norm(a.t).length - norm(b.t).length
      || norm(a.t).toLowerCase().localeCompare(norm(b.t).toLowerCase())
      || a.i - b.i)
    .map(x => x.t);
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
  if (!tags.length) {
    el.classList.remove('active');
    el.removeAttribute('role');
    this.els.searchInput.setAttribute('aria-expanded', 'false');
    this.els.searchInput.removeAttribute('aria-activedescendant');
    return;
  }
  // Обрезаем до SUGGEST_MAX: на коротком префиксе сайт отдаёт десятки тегов,
  // и список без ограничения просто не помещался в выпадающее окно.
  tags = tags.slice(0, SUGGEST_MAX);
  el.classList.add('active');
  el.setAttribute('role', 'listbox');
  el.setAttribute('id', 'suggestions-listbox');
  this.els.searchInput.setAttribute('aria-expanded', 'true');
  this.els.searchInput.setAttribute('aria-controls', 'suggestions-listbox');
  this.els.searchInput.setAttribute('aria-autocomplete', 'list');
  tags.forEach((tag, i) => {
    const label = tag.label || tag.value || tag;
    const value = tag.value || tag;
    const div = document.createElement('div');
    div.className = 'suggestion-item';
    div.dataset.value = value;
    div.setAttribute('role', 'option');
    div.setAttribute('id', `sugg-opt-${i}`);
    const span = document.createElement('span');
    span.textContent = label;
    div.appendChild(span);
    if (tag.count) {
      const cnt = document.createElement('span');
      // local=true — счётчик из скачанной базы, а не с сайта. Подписываем
      // явно: «4» без пояснения читалось как «4 поста на сайте», хотя это
      // 4 скачанных поста (у umamusume на gelbooru их 213 596).
      cnt.className = 'suggestion-count' + (tag.local ? ' local' : '');
      cnt.textContent = tag.local
        ? `${tag.count.toLocaleString('ru-RU')} лок.`
        : tag.count.toLocaleString('ru-RU');
      if (tag.local) cnt.title = `Скачано постов: ${tag.count}. На сайте тег может быть заметно популярнее.`;
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
  // Закреплённые сверху, далее по частоте.
  const sorted = [...h].sort((a, b) => (b.pin - a.pin) || (b.count - a.count));
  el.innerHTML = '';
  el.classList.add('active');
  el.setAttribute('role', 'listbox');
  el.setAttribute('id', 'history-listbox');
  this.els.searchInput.setAttribute('aria-expanded', 'true');
  this.els.searchInput.setAttribute('aria-controls', 'history-listbox');
  this.els.searchInput.setAttribute('aria-autocomplete', 'list');
  sorted.forEach((entry, i) => {
    const d = document.createElement('div');
    d.className = 'history-item';
    d.dataset.q = entry.q;
    d.setAttribute('role', 'option');
    d.setAttribute('id', `hist-opt-${i}`);

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

App.hideHistory = function () {
  this.els.historyDropdown.classList.remove('active');
  this.els.historyDropdown.removeAttribute('role');
  this.els.searchInput.setAttribute('aria-expanded', 'false');
  this.els.searchInput.removeAttribute('aria-activedescendant');
};

App.suggestProfileTag = function (type) {
  const input = type === 'fav' ? this.els.favTagInput : this.els.hiddenTagInput;
  const el = type === 'fav' ? this.els.favSuggestions : this.els.profileSuggestions;
  const q = input.value.trim();
  if (q.length < 2) {
    this._profileSuggestSeq++;
    el.classList.remove('active');
    el.innerHTML = '';
    return;
  }
  // Отдельный счётчик (не _suggestSeq): набор в поисковой строке не должен
  // отбрасывать in-flight подсказки профиля и наоборот.
  const seq = ++this._profileSuggestSeq;
  API.get(`/suggest?q=${encodeURIComponent(q)}`).then(d => {
    if (this._profileSuggestSeq !== seq) return; // устаревший ответ — набрали другой тег
    el.innerHTML = '';
    if (!d.tags || !d.tags.length) { el.classList.remove('active'); return; }
    el.classList.add('active');
    // Тот же порядок, что и в строке поиска: по популярности, точное
    // совпадение первым (это те же теги, что и /suggest отдаёт).
    this.mergeSuggestions([], d.tags, q).forEach(tag => {
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
  // Семантический поиск: «?котики в шляпах» → Ollama → теговый запрос.
  if (query && query.trim().startsWith('?') && query.trim().length > 1) {
    // Защита от дубля: state.loading — фаг постов, не блокирует search().
    // Без этого быстрый повторный ввод уходит вторым запросом к Ollama (двойной инференс).
    if (this._nlSearching) return;
    const nl = query.trim().slice(1).trim();
    this._nlSearching = true;
    this.showToast('Спрашиваю модель…');
    this.state.loading = true;
    this.els.searchBox.classList.add('loading');
    try {
      const d = await API.get('/nl-search?q=' + encodeURIComponent(nl));
      if (!d.query) throw new Error('пустой ответ');
      query = d.query;
      this.els.searchInput.value = query;
      this.onSearchInput();
      this.showToast('Распознано: ' + query, 'success');
    } catch (e) {
      this.showToast('Семантический поиск недоступен: ' + (e && e.message || 'ошибка'), 'error');
      this._nlSearching = false;
      this.state.loading = false;
      this.els.searchBox.classList.remove('loading');
      return;
    } finally {
      this._nlSearching = false;
    }
  }
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
  // Выход из режима сетки (лайки/скрытые/коллекция): без сброса modeBar
  // продолжал показывать прежний режим, а пагинация была заблокирована
  // (loadMore и sentinel работают только при displayMode === 'search').
  this.state.displayMode = 'search';
  this.state.displayIds = [];
  if (query) this._clearFeedCache();
  this.els.btnLocal.innerHTML = icon('house', 18);
  this.pushState(query, null);
  this.renderQueryChips(query);
  // forceRefresh=true: добавляет v=timestamp к URL, чтобы обойти кэш API.get.
  // Без этого поиск возвращал кэшированные результаты предыдущего запроса.
  await this.loadPosts(true, null, true);
};

// Логотип → на главную: сброс режимов, поиска и роута.
App.goHome = function () {
  this.state.isLocal = false;
  this.state.focusedIndex = -1; // U6: сброс выделения карточки
  if (this.els.btnLocal) this.els.btnLocal.innerHTML = icon('house', 18);
  if (this.state.viewerOpen) { this.closeViewer({ keepUrl: true }); }
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
  // Сброс режима сетки — та же причина, что и в search(): локальная лента
  // поверх «лайков» блокировала пагинацию и врала в modeBar.
  this.state.displayMode = 'search';
  this.state.displayIds = [];
  this.els.btnLocal.innerHTML = this.state.isLocal
    ? icon('folder', 18)
    : icon('house', 18);
  // Переключатель «Все/Новое/Виденное» виден только в локальной ленте.
  if (this.els.viewedToggle) {
    this.els.viewedToggle.classList.toggle('hidden', !this.state.isLocal);
    if (!this.state.isLocal) {
      this.state.viewedFilter = '';
      this.els.viewedToggle.querySelectorAll('.vt-btn').forEach(x => x.classList.toggle('active', x.dataset.viewed === ''));
    }
  }
  this.loadPosts(true);
  this.els.searchInput.focus();
};
