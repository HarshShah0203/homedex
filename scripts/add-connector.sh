#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: scripts/add-connector.sh [--setup|--no-auth] KIND NAME CONFIG_JSON

Examples:
  scripts/add-connector.sh --setup docker "Local Docker" docs/examples/connectors/docker-socket-proxy.json
  scripts/add-connector.sh traefik "Gateway Traefik" docs/examples/connectors/traefik.json

HOMEDEX_URL defaults to http://127.0.0.1:7377. Unless --no-auth is used,
the script reads HOMEDEX_PASSWORD or prompts without echo. --setup creates the
initial admin account before adding the connector.
EOF
}

mode="login"
case "${1:-}" in
  --setup) mode="setup"; shift ;;
  --no-auth) mode="no-auth"; shift ;;
esac
[ "$#" -eq 3 ] || { usage >&2; exit 2; }

kind="$1"
name="$2"
config_path="$3"
base_url="${HOMEDEX_URL:-http://127.0.0.1:7377}"
[ -f "$config_path" ] || { echo "error: config file not found: $config_path" >&2; exit 2; }

work="$(mktemp -d)"
trap 'stty echo 2>/dev/null || true; rm -rf "$work"' EXIT INT TERM
cookie="$work/cookies"
csrf=""

if [ "$mode" != "no-auth" ]; then
  if [ -z "${HOMEDEX_PASSWORD:-}" ]; then
    printf 'Homedex admin password: ' >&2
    stty -echo
    IFS= read -r HOMEDEX_PASSWORD
    stty echo
    printf '\n' >&2
    export HOMEDEX_PASSWORD
  fi
  python3 - "$work/password.json" <<'PY'
import json
import os
import sys
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump({"password": os.environ["HOMEDEX_PASSWORD"]}, handle)
PY

  auth_path="auth/login"
  [ "$mode" = "setup" ] && auth_path="setup"
  status="$(curl -sS -c "$cookie" -o "$work/auth.json" -w '%{http_code}' \
    -H 'Content-Type: application/json' --data-binary "@$work/password.json" \
    "$base_url/api/$auth_path")"
  if [ "$mode" = "setup" ] && [ "$status" = "409" ]; then
    status="$(curl -sS -c "$cookie" -o "$work/auth.json" -w '%{http_code}' \
      -H 'Content-Type: application/json' --data-binary "@$work/password.json" \
      "$base_url/api/auth/login")"
  fi
  if [ "$status" != "200" ]; then
    cat "$work/auth.json" >&2
    echo "error: authentication/setup returned HTTP $status" >&2
    exit 1
  fi
  csrf="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf"])' "$work/auth.json")"
fi

python3 - "$kind" "$name" "$config_path" "$work/connector.json" <<'PY'
import json
import sys
kind, name, config_path, output_path = sys.argv[1:]
with open(config_path, encoding="utf-8") as handle:
    config = json.load(handle)
with open(output_path, "w", encoding="utf-8") as handle:
    json.dump({"kind": kind, "name": name, "config": config, "enabled": True, "schedule_minutes": 15}, handle)
PY

set -- -sS -o "$work/result.json" -w '%{http_code}' -H 'Content-Type: application/json'
if [ "$mode" != "no-auth" ]; then
  set -- "$@" -b "$cookie" -H "X-Homedex-CSRF: $csrf"
fi
status="$(curl "$@" --data-binary "@$work/connector.json" "$base_url/api/connectors")"
cat "$work/result.json"
[ "$status" = "201" ] || { echo "error: connector scan returned HTTP $status" >&2; exit 1; }
echo "connector added and initial scan completed"
