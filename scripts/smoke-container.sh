#!/bin/sh
set -eu

image="${1:-homedex:local}"
container="homedex-smoke-$$"
volume="homedex-smoke-data-$$"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker volume create "$volume" >/dev/null
docker run -d --name "$container" \
  --read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges \
  -e HOMEDEX_NO_AUTH=true -v "$volume:/data" -p 127.0.0.1::7377 \
  "$image" >/dev/null
port="$(docker port "$container" 7377/tcp | sed -n 's/.*://p')"

for _ in $(seq 1 60); do
  curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1 && break
  sleep 0.05
done
curl -fsS "http://127.0.0.1:$port/api/health" | grep -q '"status":"ok"'
curl -fsS "http://127.0.0.1:$port/api/version" | grep -q '"version"'
case "$(docker inspect "$container" --format '{{.Config.User}}')" in
  65532|65532:65532) ;;
  *) echo "error: container is not configured as UID 65532" >&2; exit 1 ;;
esac
[ "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" = "true" ]
echo "container smoke: healthy as non-root with a read-only root filesystem"
