// precompress.mjs — предсжатие собранного esbuild-бандла в .br и .gz.
// Запускается автоматически после `npm run build` (см. package.json).
// Отдаётся через servePrecompressed() в middleware.go: CPU на сжатие не
// тратится ни на одном запросе, а статический brotli -11 дополнительно
// выигрывает ~10-15% размера у brotli-6 "на лету".

import { readFileSync, writeFileSync, statSync } from "node:fs";
import {
  constants,
  gzipSync,
  brotliCompressSync,
  brotliDecompressSync,
  gunzipSync,
} from "node:zlib";

const file = "static/js/dist/app.js";
const src = readFileSync(file);
const st = statSync(file);

// Качество brotli 11: текстовые бандлы выигрывают ещё ~10-15% против
// brotli-6 "на лету". Константу берём из zlib.constants — числовой литерал
// 6 означает BROTLI_PARAM_LARGE_WINDOW, а не качество (оно равно 1), и
// такой вызов писал повреждённый .br, который браузер отклонял с
// ERR_CONTENT_DECODING_FAILED.
const br = brotliCompressSync(src, {
  params: { [constants.BROTLI_PARAM_QUALITY]: 11 },
});

// Уровень gzip 9 — бандл сжимается один раз на сборке, CPU не важен.
const gz = gzipSync(src, { level: 9, mtime: false });

// Round-trip проверка: сервер отдаёт .br/.gz как есть, без повторного сжатия,
// поэтому повреждённый файл убьёт всю страницу (ERR_CONTENT_DECODING_FAILED).
// Ловим это на сборке, а не в браузере.
if (!brotliDecompressSync(br).equals(src)) {
  throw new Error("precompress: brotli round-trip mismatch");
}
if (!gunzipSync(gz).equals(src)) {
  throw new Error("precompress: gzip round-trip mismatch");
}

writeFileSync(file + ".br", br);
writeFileSync(file + ".gz", gz);

console.log(
  `precompress: ${(st.size / 1024).toFixed(1)}KB -> br ${(br.length / 1024).toFixed(1)}KB, gz ${(gz.length / 1024).toFixed(1)}KB`
);