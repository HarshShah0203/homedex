#!/bin/sh
set -eu

binary="${1:-./homedex}"
data_dir="$(mktemp -d)"
log_file="$data_dir/homedex.log"
port="${HOMEDEX_SMOKE_PORT:-17377}"
pid=""

cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$data_dir"
}
trap cleanup EXIT INT TERM

start_ms="$(python3 -c 'import time; print(time.monotonic_ns() // 1_000_000)')"
HOMEDEX_DATA_DIR="$data_dir" HOMEDEX_LISTEN="127.0.0.1:$port" HOMEDEX_NO_AUTH=true \
  "$binary" >"$log_file" 2>&1 &
pid=$!

status=""
for _ in $(seq 1 40); do
  if ! kill -0 "$pid" 2>/dev/null; then
    cat "$log_file" >&2
    echo "error: Homedex exited during startup" >&2
    exit 1
  fi
  status="$(curl -sS -o "$data_dir/health.json" -w '%{http_code}' "http://127.0.0.1:$port/api/health" 2>/dev/null || true)"
  [ "$status" = "200" ] && break
  sleep 0.05
done

end_ms="$(python3 -c 'import time; print(time.monotonic_ns() // 1_000_000)')"
elapsed_ms=$((end_ms - start_ms))
[ "$status" = "200" ] || { cat "$log_file" >&2; echo "error: health endpoint did not become ready" >&2; exit 1; }
grep -q '"status":"ok"' "$data_dir/health.json"
curl -fsS "http://127.0.0.1:$port/api/version" | grep -q '"version"'
if [ "$elapsed_ms" -ge 2000 ]; then
  cat "$log_file" >&2
  echo "error: startup took ${elapsed_ms}ms (budget: <2000ms)" >&2
  exit 1
fi
echo "startup smoke: ready in ${elapsed_ms}ms"
