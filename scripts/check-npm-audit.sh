#!/bin/sh
set -eu

tmp_root="${TMPDIR:-/var/tmp}"
report="$tmp_root/homedex-npm-audit-$$.json"
production_report="$tmp_root/homedex-npm-audit-production-$$.json"
cleanup() { rm -f "$report" "$production_report"; }
trap cleanup EXIT INT TERM

status=0
(cd web && npm audit --json >"$report") || status=$?
if [ "$status" -ne 1 ]; then
  echo "error: full npm audit returned unexpected status $status" >&2
  exit 1
fi

(cd web && npm audit --omit=dev --json >"$production_report")

python3 - "$report" "$production_report" <<'PY'
import json
import sys

full = json.load(open(sys.argv[1], encoding="utf-8"))
production = json.load(open(sys.argv[2], encoding="utf-8"))

expected = {
    "@vitest/mocker": ("moderate", False),
    "esbuild": ("moderate", False),
    "vite": ("high", False),
    "vite-node": ("moderate", False),
    "vitest": ("critical", True),
}
actual = full.get("vulnerabilities", {})
if set(actual) != set(expected):
    raise SystemExit(f"npm advisory set changed: expected {sorted(expected)}, got {sorted(actual)}")

for name, (severity, direct) in expected.items():
    item = actual[name]
    if item.get("severity") != severity or item.get("isDirect") is not direct:
        raise SystemExit(f"unexpected classification for {name}: {item}")
    fix = item.get("fixAvailable")
    if not isinstance(fix, dict) or fix.get("name") != "vitest" or fix.get("isSemVerMajor") is not True:
        raise SystemExit(f"{name} no longer has the documented Vitest-major remediation: {fix}")

counts = full.get("metadata", {}).get("vulnerabilities", {})
expected_counts = {"info": 0, "low": 0, "moderate": 3, "high": 1, "critical": 1, "total": 5}
if counts != expected_counts:
    raise SystemExit(f"npm severity counts changed: expected {expected_counts}, got {counts}")

production_counts = production.get("metadata", {}).get("vulnerabilities", {})
if production_counts.get("total") != 0:
    raise SystemExit(f"production npm dependencies have advisories: {production_counts}")

print("npm audit contract: 0 production findings; 5 documented Vitest-2 dev-only records")
PY
