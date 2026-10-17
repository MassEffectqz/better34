import { App } from './state.js';
import { t, tf } from './i18n.js';

/**
 * Кэш извлечённых палитр (URL/ID -> { colors: string[], hues: string[] })
 */
const paletteCache = new Map();

/**
 * Классификация RGB в семантическое имя оттенка (hue name).
 * @param {number} r 0-255
 * @param {number} g 0-255
 * @param {number} b 0-255
 * @returns {string}
 */
export function rgbToHueName(r, g, b) {
  const rn = r / 255;
  const gn = g / 255;
  const bn = b / 255;
  const max = Math.max(rn, gn, bn);
  const min = Math.min(rn, gn, bn);
  const l = (max + min) / 2;

  if (l <= 0.12) return 'dark';
  if (l >= 0.88) return 'light';

  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  if (s < 0.15) return 'monochrome';

  let h = 0;
  if (max === rn) {
    h = ((gn - bn) / d + (gn < bn ? 6 : 0)) * 60;
  } else if (max === gn) {
    h = ((bn - rn) / d + 2) * 60;
  } else {
    h = ((rn - gn) / d + 4) * 60;
  }

  if (h >= 345 || h < 18) return 'red';
  if (h >= 18 && h < 45) return 'orange';
  if (h >= 45 && h < 68) return 'yellow';
  if (h >= 68 && h < 160) return 'green';
  if (h >= 160 && h < 198) return 'teal';
  if (h >= 198 && h < 258) return 'blue';
  if (h >= 258 && h < 295) return 'purple';
  return 'pink';
}

/**
 * Конвертация RGB в HEX строку.
 * @param {number} r
 * @param {number} g
 * @param {number} b
 * @returns {string}
 */
export function rgbToHex(r, g, b) {
  return '#' + [r, g, b].map(x => Math.round(x).toString(16).padStart(2, '0')).join('');
}

/**
 * Извлекает цветовую палитру из изображения (OffscreenCanvas/Canvas).
 * @this {any}
 * @param {HTMLImageElement} img
 * @returns {{ colors: string[], hues: string[] }}
 */
App.extractPalette = function (img) {
  if (!img || !img.naturalWidth || !img.naturalHeight) {
    return { colors: [], hues: [] };
  }
  const key = img.src;
  if (paletteCache.has(key)) return paletteCache.get(key);

  try {
    const canvas = document.createElement('canvas');
    canvas.width = 24;
    canvas.height = 24;
    const ctx = canvas.getContext('2d', { willReadFrequently: true });
    if (!ctx) return { colors: [], hues: [] };

    ctx.drawImage(img, 0, 0, 24, 24);
    const data = ctx.getImageData(0, 0, 24, 24).data;

    const counts = new Map();
    for (let i = 0; i < data.length; i += 4) {
      const a = data[i + 3];
      if (a < 128) continue;
      const r = Math.round(data[i] / 16) * 16;
      const g = Math.round(data[i + 1] / 16) * 16;
      const b = Math.round(data[i + 2] / 16) * 16;
      const hex = rgbToHex(r, g, b);
      counts.set(hex, (counts.get(hex) || 0) + 1);
    }

    const sorted = [...counts.entries()].sort((a, b) => b[1] - a[1]);
    const chosen = [];
    const hues = new Set();

    for (const [hex] of sorted) {
      if (chosen.length >= 4) break;
      const r = parseInt(hex.slice(1, 3), 16);
      const g = parseInt(hex.slice(3, 5), 16);
      const b = parseInt(hex.slice(5, 7), 16);
      const hue = rgbToHueName(r, g, b);

      const isDistinct = chosen.every(c => {
        const cr = parseInt(c.slice(1, 3), 16);
        const cg = parseInt(c.slice(3, 5), 16);
        const cb = parseInt(c.slice(5, 7), 16);
        const dist = Math.hypot(r - cr, g - cg, b - cb);
        return dist > 36;
      });

      if (isDistinct || chosen.length < 2) {
        chosen.push(hex);
        hues.add(hue);
      }
    }

    const res = { colors: chosen, hues: [...hues] };
    paletteCache.set(key, res);
    return res;
  } catch {
    return { colors: [], hues: [] };
  }
};

const HUE_TRANSLATIONS = {
  red: () => t('color.red'),
  orange: () => t('color.orange'),
  yellow: () => t('color.yellow'),
  green: () => t('color.green'),
  teal: () => t('color.teal'),
  blue: () => t('color.blue'),
  purple: () => t('color.purple'),
  pink: () => t('color.pink'),
  dark: () => t('color.dark'),
  light: () => t('color.light'),
  monochrome: () => t('color.monochrome'),
};

function getHueLabel(hue) {
  const fn = HUE_TRANSLATIONS[hue];
  return fn ? fn() : hue;
}

/**
 * Отрисовка палитры текущего поста во вьювере.
 * @this {any}
 * @param {any} post
 */
App.renderViewerPalette = function (post) {
  const container = this.els.viewerPalette;
  if (!container) return;
  container.innerHTML = '';
  if (!post) return;

  const img = this.els.viewerContent.querySelector('img');
  if (!img) return;

  const show = () => {
    const pal = this.extractPalette(img);
    container.innerHTML = '';
    if (!pal.colors || !pal.colors.length) return;

    pal.colors.forEach((hex, i) => {
      const hue = pal.hues[i] || 'color';
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'palette-swatch';
      btn.style.backgroundColor = hex;
      const hueLabel = getHueLabel(hue);
      btn.title = `${hex} (${hueLabel}) · ${tf('color.searchBy', { name: hueLabel })}`;
      btn.setAttribute('aria-label', `${hex} - ${hueLabel}`);
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        if (e.shiftKey) {
          this.setColorFilter(hue);
        } else {
          if (navigator && navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(hex);
          }
          this.showToast(tf('color.copied', { hex }), 'success');
        }
      });
      container.appendChild(btn);
    });
  };

  if (img.complete && img.naturalWidth) {
    show();
  } else {
    img.addEventListener('load', show, { once: true });
  }
};

/**
 * Установка фильтра по цвету.
 * @this {any}
 * @param {string} colorName
 */
App.setColorFilter = function (colorName) {
  const popover = document.getElementById('color-popover');
  if (popover && typeof (/** @type {any} */ (popover)).hidePopover === 'function') {
    try { (/** @type {any} */ (popover)).hidePopover(); } catch {}
  }
  let q = (this.state.query || (this.els.searchInput && this.els.searchInput.value) || '').trim();
  q = q.replace(/(?:^|\s)color:\w+/g, '').trim();
  if (colorName) {
    q = q ? `${q} color:${colorName}` : `color:${colorName}`;
  }
  this.setSearchValue(q);
  this.search(q);
};
