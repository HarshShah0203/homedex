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

go test ./internal/connectors/tailscale -run 'TestScanNeverIngestsKeysUsersOrEndpoints|TestErrorsNeverEchoCredentials' -count=1
go test ./demo/seed -run 'TestFakeLabSnapshotIsDeterministicAndSecretFree' -count=1
go test ./internal/export -run 'TestMandatoryRedactionAndContextBudget|TestShareExportOmitsPrivateMetadataAndLabels' -count=1
go test ./internal/server -run 'TestShareTokensAreReadOnlyScopedRevocableAndExportsStayPrivate' -count=1
echo "redaction contract: environment and tailnet key ingestion absent; fixture, exports, and shares verified"
