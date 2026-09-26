#!/bin/sh
set -eu

# Validates the rendered Compose configuration, not the YAML text: first the
# default pull-only docker-compose.yml, then the same file with the
# docker-compose.build.yml source-build override. Both must keep every
# hardening property.
cd "$(dirname "$0")/.."
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
docker compose -f docker-compose.yml config --format json >"$work_dir/default.json"
docker compose -f docker-compose.yml -f docker-compose.build.yml config --format json >"$work_dir/build.json"

python3 - "$work_dir/default.json" "$work_dir/build.json" <<'PY'
import json
import sys


def check(path, variant):
    with open(path, encoding="utf-8") as handle:
        config = json.load(handle)

    services = config["services"]
    proxy = services["docker-socket-proxy"]
    app = services["homedex"]
    env = proxy.get("environment", {})

    for key in ("CONTAINERS", "IMAGES", "INFO", "NETWORKS", "VERSION"):
        assert str(env.get(key)) == "1", f"{variant}: socket proxy must enable {key}"
    for key in ("POST", "ALLOW_START", "ALLOW_STOP", "ALLOW_RESTARTS"):
        assert str(env.get(key)) == "0", f"{variant}: socket proxy must deny {key}"

    socket_mounts = [m for m in proxy.get("volumes", []) if m.get("target") == "/var/run/docker.sock"]
    assert len(socket_mounts) == 1 and socket_mounts[0].get("read_only") is True, f"{variant}: proxy socket bind must be read-only"
    assert not any(m.get("target") == "/var/run/docker.sock" for m in app.get("volumes", [])), f"{variant}: Homedex must not receive the raw socket"

    for name, service in (("docker-socket-proxy", proxy), ("homedex", app)):
        assert service.get("read_only") is True, f"{variant}: {name} root filesystem must be read-only"
        assert "ALL" in service.get("cap_drop", []), f"{variant}: {name} must drop all capabilities"
        assert "no-new-privileges:true" in service.get("security_opt", []), f"{variant}: {name} must set no-new-privileges"

    ports = app.get("ports", [])
    assert ports and all(p.get("host_ip") in ("127.0.0.1", "::1") for p in ports), f"{variant}: default UI port must bind to loopback"
    assert config["networks"]["discovery"].get("internal") is True, f"{variant}: socket proxy network must be internal"
    return app


default_app = check(sys.argv[1], "default")
# The default file must work on its own after a plain download, so it pulls the
# published image and never needs a build context.
assert "build" not in default_app, "default: Homedex must pull the published image, not build from a checkout"
assert default_app.get("image", "").startswith("ghcr.io/harshshah0203/homedex:"), "default: Homedex must use the published GHCR image"

build_app = check(sys.argv[2], "source build")
assert "build" in build_app, "source build: docker-compose.build.yml must build Homedex from the checkout"
print("compose security contract: passed (default pull-only and source-build override)")
PY
