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

go test ./demo/seed -run 'TestFakeLabSnapshotIsDeterministicAndSecretFree' -count=1
go test ./internal/export -run 'TestMandatoryRedactionAndContextBudget|TestShareExportOmitsPrivateMetadataAndLabels' -count=1
go test ./internal/server -run 'TestShareTokensAreReadOnlyScopedRevocableAndExportsStayPrivate' -count=1
echo "redaction contract: environment ingestion absent; fixture, exports, and shares verified"
