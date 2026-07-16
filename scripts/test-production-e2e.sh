#!/bin/sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
tmp_root="${TMPDIR:-/var/tmp}"
workspace="$(mktemp -d "$tmp_root/homedex-production-e2e.XXXXXX")"
data_dir="$workspace/data"
log_file="$workspace/homedex.log"
artifact_dir="${HOMEDEX_E2E_ARTIFACT_DIR:-$tmp_root/homedex-playwright}"
pid=""

cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$workspace"
}
trap cleanup EXIT INT TERM

[ -d "$repo_root/web/node_modules/@playwright/test" ] || {
  echo "error: run npm --prefix web ci before production E2E" >&2
  exit 1
}

rm -rf "$artifact_dir"
mkdir -p "$artifact_dir"

# Build in an isolated writable tree. This proves the generated embedded assets
# are the ones served by Go without leaving root-owned or dirty build output in
# the checkout used by later CI steps.
tar -C "$repo_root" \
  --exclude=.git \
  --exclude=dist \
  --exclude=web/node_modules \
  --exclude=web/dist \
  --exclude=web/test-results \
  --exclude=web/playwright-report \
  -cf - . | tar -C "$workspace" -xf -
ln -s "$repo_root/web/node_modules" "$workspace/web/node_modules"
ln -s "$repo_root/web/node_modules" "$workspace/node_modules"

(cd "$workspace/web" && npm run build)
(cd "$workspace" && go run ./demo/seed --data-dir "$data_dir" --reset)
(cd "$workspace" && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$workspace/homedex" ./cmd/homedex)

port="${HOMEDEX_E2E_PORT:-}"
if [ -z "$port" ]; then
  port="$(python3 - <<'PY'
import socket
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    print(listener.getsockname()[1])
PY
)"
fi

HOMEDEX_DATA_DIR="$data_dir" HOMEDEX_LISTEN="127.0.0.1:$port" \
  "$workspace/homedex" >"$log_file" 2>&1 &
pid=$!

for _ in $(seq 1 100); do
  curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1 && break
  sleep 0.05
done
curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null

artifact_json="$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$artifact_dir")"
cat >"$workspace/web/playwright.production.config.ts" <<EOF
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '../demo/e2e',
  timeout: 30_000,
  outputDir: $artifact_json + '/test-results',
  reporter: [['line'], ['html', { outputFolder: $artifact_json + '/html-report', open: 'never' }]],
  use: { baseURL: 'http://127.0.0.1:$port', trace: 'retain-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }]
});
EOF

if ! (cd "$workspace/web" && npm run test:e2e -- --config playwright.production.config.ts); then
  cat "$log_file" >&2
  exit 1
fi

echo "production E2E: seeded SQLite, embedded frontend, and Go API verified"
