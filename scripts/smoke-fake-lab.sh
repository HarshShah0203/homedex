#!/bin/sh
set -eu

binary="${1:-./homedex}"
data_dir="$(mktemp -d)"
log_file="$data_dir/homedex.log"
port="${HOMEDEX_DEMO_SMOKE_PORT:-17378}"
pid=""

cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$data_dir"
}
trap cleanup EXIT INT TERM

go run ./demo/seed --data-dir "$data_dir" --reset
HOMEDEX_DATA_DIR="$data_dir" HOMEDEX_LISTEN="127.0.0.1:$port" HOMEDEX_NO_AUTH=true \
  "$binary" >"$log_file" 2>&1 &
pid=$!

for _ in $(seq 1 60); do
  curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1 && break
  sleep 0.05
done
curl -fsS "http://127.0.0.1:$port/api/services?limit=500" >"$data_dir/services.json"
curl -fsS "http://127.0.0.1:$port/api/routes?limit=500" >"$data_dir/routes.json"
curl -fsS "http://127.0.0.1:$port/api/connectors?limit=500" >"$data_dir/connectors.json"
curl -fsS "http://127.0.0.1:$port/api/export/context?mask_domains=true&mask_external_ips=true" >"$data_dir/context.md"

python3 - "$data_dir" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
services = json.loads((root / "services.json").read_text())["items"]
routes = json.loads((root / "routes.json").read_text())["items"]
connectors = json.loads((root / "connectors.json").read_text())["items"]
context = (root / "context.md").read_bytes()
assert len(services) == 12, len(services)
assert len(routes) == 10, len(routes)
assert sum(route["status"] == "broken" for route in routes) == 1
assert sum(route["resolve_confidence"] == "medium" for route in routes) == 1
assert len(connectors) == 1 and not connectors[0]["enabled"]
assert len(context) <= 100 * 1024, len(context)
assert b".lab.example" not in context
assert b"203.0.113.10" not in context
assert b"domain-001.invalid" in context
assert b"[EXTERNAL-IP]" in context
assert b"10.0.20.10" in context  # RFC1918 addresses are intentionally not external-IP masked.
print("fake-lab smoke: inventory, route outcomes, and masked context export verified")
PY
