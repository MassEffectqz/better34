#!/usr/bin/env bash
set -euo pipefail

bin=$(realpath "${1:?usage: smoke.sh /absolute/or/local/binary}")
port=${BRIEFLY_SMOKE_PORT:-39127}
tmp=$(mktemp -d)
pid=''
cleanup() {
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

(
  cd "$tmp"
  exec env BRIEFLY_HOST=127.0.0.1 BRIEFLY_PORT="$port" BRIEFLY_TLS=0 "$bin" >server.log 2>&1
) &
pid=$!

for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:$port/api/healthz" >/dev/null; then break; fi
  sleep 0.25
done
curl -fsS "http://127.0.0.1:$port/" | grep -q '<!DOCTYPE html>'
curl -fsS "http://127.0.0.1:$port/static/css/style.css" >/dev/null
curl -fsS "http://127.0.0.1:$port/static/js/dist/app.js" >/dev/null
curl -fsS "http://127.0.0.1:$port/api/healthz" >/dev/null
[[ -d "$tmp/data" ]]
echo "smoke ok: $port"
