// static/js/worker.js — Web Worker для выноса ресурсоемких задач (палитра, хэмминг, фильтрация тегов)
// Работает изолированно от основного UI-потока.

function rgbToHueName(r, g, b) {
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

function rgbToHex(r, g, b) {
  return '#' + [r, g, b].map(x => Math.round(x).toString(16).padStart(2, '0')).join('');
}

function extractPalette(data, _width, _height) {
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
  const hues = [];

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
      hues.push(hue);
    }
  }

  return { colors: chosen, hues };
}

function hexHammingDistance(h1, h2) {
  if (!h1 || !h2 || h1.length !== h2.length) return 64;
  let dist = 0;
  for (let i = 0; i < h1.length; i++) {
    const v1 = parseInt(h1[i], 16) || 0;
    const v2 = parseInt(h2[i], 16) || 0;
    let xor = v1 ^ v2;
    while (xor > 0) {
      dist += xor & 1;
      xor >>= 1;
    }
  }
  return dist;
}

function sortSimilar(targetPhash, posts, maxDist) {
  const results = [];
  const limit = typeof maxDist === 'number' ? maxDist : 15;
  for (let i = 0; i < posts.length; i++) {
    const p = posts[i];
    if (!p.phash) continue;
    const d = hexHammingDistance(targetPhash, p.phash);
    if (d <= limit) {
      results.push({ post: p, dist: d });
    }
  }
  results.sort((a, b) => a.dist - b.dist);
  return results.map(r => r.post);
}

self.onmessage = function (e) {
  const { id, action, payload } = e.data || {};
  try {
    let result;
    switch (action) {
      case 'extractPalette': {
        const { data, width, height } = payload;
        result = extractPalette(data, width, height);
        break;
      }
      case 'sortSimilar': {
        const { targetPhash, posts, maxDist } = payload;
        result = sortSimilar(targetPhash, posts, maxDist);
        break;
      }
      case 'hammingDistance': {
        const { h1, h2 } = payload;
        result = hexHammingDistance(h1, h2);
        break;
      }
      default:
        throw new Error(`Unknown worker action: ${action}`);
    }
    self.postMessage({ id, ok: true, result });
  } catch (err) {
    self.postMessage({ id, ok: false, error: err && err.message ? err.message : String(err) });
  }
};
