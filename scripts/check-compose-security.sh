#!/bin/sh
set -eu

config_file="$(mktemp)"
trap 'rm -f "$config_file"' EXIT
docker compose -f docker-compose.yml config --format json >"$config_file"

python3 - "$config_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    config = json.load(handle)

services = config["services"]
proxy = services["docker-socket-proxy"]
app = services["homedex"]
env = proxy.get("environment", {})

for key in ("CONTAINERS", "IMAGES", "INFO", "NETWORKS", "VERSION"):
    assert str(env.get(key)) == "1", f"socket proxy must enable {key}"
for key in ("POST", "ALLOW_START", "ALLOW_STOP", "ALLOW_RESTARTS"):
    assert str(env.get(key)) == "0", f"socket proxy must deny {key}"

socket_mounts = [m for m in proxy.get("volumes", []) if m.get("target") == "/var/run/docker.sock"]
assert len(socket_mounts) == 1 and socket_mounts[0].get("read_only") is True, "proxy socket bind must be read-only"
assert not any(m.get("target") == "/var/run/docker.sock" for m in app.get("volumes", [])), "Homedex must not receive the raw socket"

for name, service in (("docker-socket-proxy", proxy), ("homedex", app)):
    assert service.get("read_only") is True, f"{name} root filesystem must be read-only"
    assert "ALL" in service.get("cap_drop", []), f"{name} must drop all capabilities"
    assert "no-new-privileges:true" in service.get("security_opt", []), f"{name} must set no-new-privileges"

ports = app.get("ports", [])
assert ports and all(p.get("host_ip") in ("127.0.0.1", "::1") for p in ports), "default UI port must bind to loopback"
assert config["networks"]["discovery"].get("internal") is True, "socket proxy network must be internal"
print("compose security contract: passed")
PY
