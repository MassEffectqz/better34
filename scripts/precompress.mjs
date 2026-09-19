// precompress.mjs — предсжатие собранного esbuild-бандла в .br и .gz.
// Запускается автоматически после `npm run build` (см. package.json).
// Отдаётся через servePrecompressed() в middleware.go: CPU на сжатие не
// тратится ни на одном запросе, а статический brotli -11 дополнительно
// выигрывает ~10-15% размера у brotli-6 "на лету".

import { readFileSync, writeFileSync, statSync } from "node:fs";
import { gzipSync, brotliCompressSync } from "node:zlib";

const file = "static/js/dist/app.js";
const src = readFileSync(file);
const st = statSync(file);

// Качество brotli 11 (BROTLI_PARAM_QUALITY = 6): текстовые бандлы
// выигрывают ещё ~10-15% против brotli-6 "на лету".
const br = brotliCompressSync(src, { params: { [6]: 11 } });

// Уровень gzip 9 — бандл сжимается один раз на сборке, CPU не важен.
const gz = gzipSync(src, { level: 9, mtime: false });

writeFileSync(file + ".br", br);
writeFileSync(file + ".gz", gz);

console.log(
  `precompress: ${(st.size / 1024).toFixed(1)}KB -> br ${(br.length / 1024).toFixed(1)}KB, gz ${(gz.length / 1024).toFixed(1)}KB`
);