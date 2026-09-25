// Объяснения к тегам. Список редактируется в data/odd_tags.json, текст
// показывается в подсказке при наведении на тег в просмотре поста.
//
// Три уровня:
//   yellow — жёлтая подсветка, «фетиш, но нормально»;
//   red    — красная подсветка, «супер странный»;
//   note   — без цвета, просто объяснение тега.
//
// В подсказке только сам текст описания: префиксов вида «Странный —» нет.

// Порядок разделов в файле и в ответе.
const ODD_TAG_LEVELS = ['yellow', 'red', 'note'];
// Порядок проверки при поиске тега — от более строгого к более мягкому.
// Если тег по ошибке попал сразу в два раздела, показываем строгий:
// иначе случайный тег в red тихо выглядел бы как безобидный в yellow.
const ODD_TAG_PRIORITY = ['red', 'yellow', 'note'];

// asMap превращает уровень в Map(tag -> описание). Принимает и объект,
// и массив — чужой отредактированный файл не должен ломать подсказки.
const asMap = (raw) => {
  const out = new Map();
  if (Array.isArray(raw)) {
    for (const t of raw) if (t != null && String(t).trim()) out.set(normTag(t), '');
    return out;
  }
  if (raw && typeof raw === 'object') {
    for (const [t, d] of Object.entries(raw)) {
      const k = normTag(t);
      if (k) out.set(k, typeof d === 'string' ? d : '');
    }
  }
  return out;
};

// normTag приводит тег к тому же виду, что и сервер (normalizeOddTag в
// internal/odd_tags.go): нижний регистр, пробелы и дефисы → подчёркивания.
// Без этого теги с дефисом (t-shirt, o-ring, one-piece_swimsuit) не находились
// никогда: ключ в файле сервер нормализует, а поиск шёл по сырому тегу.
const normTag = (tag) => String(tag || '').trim().toLowerCase()
  .replace(/[\s-]+/g, '_');

export const OddTags = {
  _byLevel: { yellow: new Map(), red: new Map(), note: new Map() },

  async load() {
    let data = null;
    try {
      const res = await fetch('/api/odd-tags', { headers: { Accept: 'application/json' } });
      if (res.ok) data = await res.json();
    } catch { /* сервер недоступен — подсказок не будет */ }
    if (!data || typeof data !== 'object') data = {};
    for (const lvl of ODD_TAG_LEVELS) this._byLevel[lvl] = asMap(data[lvl]);
    return this;
  },

  // Уровень тега или null. При конфликте разделов побеждает более строгий.
  levelOf(tag) {
    const t = normTag(tag);
    if (!t) return null;
    for (const lvl of ODD_TAG_PRIORITY) {
      if (this._byLevel[lvl].has(t)) return lvl;
    }
    return null;
  },

  // Текст объяснения для тега или null, если его нет.
  descOf(tag) {
    const t = normTag(tag);
    if (!t) return null;
    const lvl = this.levelOf(t);
    if (!lvl) return null;
    return this._byLevel[lvl].get(t) || null;
  },
};