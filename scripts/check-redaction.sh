#!/bin/sh
set -eu

# Docker environment variables are deliberately absent from Homedex's domain
# model. This source-level tripwire complements connector and fixture tests by
# failing if a future Docker implementation starts reading Config.Env.
violations="$(
  find internal/connectors internal/domain -type f -name '*.go' ! -name '*_test.go' \
    -exec grep -Hn -E '\.Config\.Env([^[:alnum:]_]|$)|ContainerJSON[^\n]*Env' {} \; || true
)"
if [ -n "$violations" ]; then
  printf '%s\n' "$violations"
  echo "error: Docker environment ingestion is forbidden" >&2
  exit 1
fi

# The Tailscale device list also carries node/machine keys, the owner's email
# and public endpoints. The connector decodes an allow-list of fields; fail if
# any of those ever appear in its decode structs or it asks for every field.
violations="$(
  find internal/connectors/tailscale -type f -name '*.go' ! -name '*_test.go' \
    -exec grep -Hn -E 'json:"(machineKey|nodeKey|tailnetLockKey|tailnetLockError|user|clientConnectivity|advertisedRoutes|enabledRoutes)"|fields=all' {} \; || true
)"
if [ -n "$violations" ]; then
  printf '%s\n' "$violations"
  echo "error: Tailscale key/user/endpoint ingestion is forbidden" >&2
  exit 1
fi

# The Proxmox connector reads only the token's own access/permissions,
# cluster/resources, cluster/status and, for running guests, the agent's
# network-get-interfaces and the container interface list, with GET. Fail if it
# ever decodes MAC addresses, agent statistics, tags or pools, names a guest
# config, console, monitor, storage, any other access path or other agent
# path, or issues anything but GET.
violations="$(
  find internal/connectors/proxmox -type f -name '*.go' ! -name '*_test.go' \
    -exec grep -Hn -E 'json:"(hwaddr|hardware-address|statistics|tags|pool|hastate|lock|description|sshkeys|cipassword|env)"|"[^"]*/(config|pending|cloudinit|monitor|vncproxy|termproxy|spiceproxy|storage)([/?"]|$)|"[^"]*/access(["?]|$|/([^p"]|p[^e]|permissions[^"]))|/agent/(exec|file-|set-user-password|get-users|get-fsinfo|get-osinfo|ping)|Method(Post|Put|Patch|Delete)|"(POST|PUT|PATCH|DELETE)"' {} \; || true
)"
if [ -n "$violations" ]; then
  printf '%s\n' "$violations"
  echo "error: Proxmox connector reaches beyond its read-only allow-list" >&2
  exit 1
fi

go test ./internal/connectors/tailscale -run 'TestScanNeverIngestsKeysUsersOrEndpoints|TestErrorsNeverEchoCredentials' -count=1
go test ./internal/connectors/proxmox -run 'TestStatusErrorsNeverEchoUpstreamText|TestDecodeConfig|TestScanReportsNodesAndGuestsOfACluster|TestMalformedClusterResponsesFailTheScan' -count=1
go test ./demo/seed -run 'TestFakeLabSnapshotIsDeterministicAndSecretFree' -count=1
go test ./internal/export -run 'TestMandatoryRedactionAndContextBudget|TestShareExportOmitsPrivateMetadataAndLabels' -count=1
go test ./internal/server -run 'TestShareTokensAreReadOnlyScopedRevocableAndExportsStayPrivate' -count=1
echo "redaction contract: environment, tailnet key and Proxmox guest config ingestion absent; fixture, exports, and shares verified"
