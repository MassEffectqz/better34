// Декодер BlurHash — парный к internal/blurhash.go (стандартный алгоритм
// blurha.sh): base83, DC 4 символа, AC по 4 символа. Отдаёт ImageData
// для отрисовки плейсхолдера на canvas до загрузки полной картинки.

const B83 = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~';

function b83Decode(str, length) {
  let value = 0;
  for (let i = 1; i <= length; i++) {
    const c = B83.indexOf(str[i - 1]);
    if (c < 0) return -1;
    value += c * Math.pow(83, length - i);
  }
  return value;
}

function srgbToLinear(v) {
  const x = v / 255;
  return x <= 0.04045 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
}

function linearToSrgb(v) {
  const x = Math.max(0, Math.min(1, v));
  return x <= 0.0031308 ? Math.round(x * 12.92 * 255 + 0.5) : Math.round((1.055 * Math.pow(x, 1 / 2.4) - 0.055) * 255 + 0.5);
}

function signPow(v, exp) {
  return (v < 0 ? -1 : 1) * Math.pow(Math.abs(v), exp);
}

export function decodeBlurHash(hash, width, height) {
  if (!hash || hash.length < 6) return null;
  const sizeFlag = b83Decode(hash[0], 1);
  if (sizeFlag < 0) return null;
  const numY = Math.floor(sizeFlag / 9) + 1;
  const numX = (sizeFlag % 9) + 1;
  const quantMax = b83Decode(hash[1], 1);
  if (quantMax < 0) return null;
  const actualMax = (quantMax + 1) / 166;
  if (hash.length !== 2 + 4 + 4 * (numX * numY - 1)) return null;

  const colours = [];
  for (let i = 0; i < numX * numY; i++) {
    if (i === 0) {
      const v = b83Decode(hash.substring(2, 6), 4);
      if (v < 0) return null;
      colours.push([srgbToLinear(v >> 16), srgbToLinear((v >> 8) & 255), srgbToLinear(v & 255)]);
    } else {
      const v = b83Decode(hash.substring(4 + i * 4, 8 + i * 4), 4);
      if (v < 0) return null;
      const qR = Math.floor(v / 361), qG = Math.floor(v / 19) % 19, qB = v % 19;
      colours.push([
        signPow((qR - 9) / 9, 0.5) * actualMax,
        signPow((qG - 9) / 9, 0.5) * actualMax,
        signPow((qB - 9) / 9, 0.5) * actualMax,
      ]);
    }
  }

  const cosX = [];
  for (let i = 0; i < numX; i++) cosX[i] = new Float32Array(width);
  const cosY = [];
  for (let j = 0; j < numY; j++) cosY[j] = new Float32Array(height);
  for (let i = 0; i < numX; i++) for (let x = 0; x < width; x++) cosX[i][x] = Math.cos(Math.PI * i * x / width);
  for (let j = 0; j < numY; j++) for (let y = 0; y < height; y++) cosY[j][y] = Math.cos(Math.PI * j * y / height);

  const pixels = new Uint8ClampedArray(width * height * 4);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      let r = 0, g = 0, b = 0;
      for (let j = 0; j < numY; j++) {
        for (let i = 0; i < numX; i++) {
          const basis = (i === 0 && j === 0 ? 1 : 2) * cosX[i][x] * cosY[j][y];
          const c = colours[j * numX + i];
          r += basis * c[0];
          g += basis * c[1];
          b += basis * c[2];
        }
      }
      const o = 4 * (y * width + x);
      pixels[o] = linearToSrgb(r);
      pixels[o + 1] = linearToSrgb(g);
      pixels[o + 2] = linearToSrgb(b);
      pixels[o + 3] = 255;
    }
  }
  return new ImageData(pixels, width, height);
}