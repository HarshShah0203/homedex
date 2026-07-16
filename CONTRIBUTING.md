# Contributing to Homedex

Homedex accepts focused fixes, tests, documentation, and read-only connectors. Open an issue before undertaking a broad product change so work does not diverge from the deliberately narrow inventory scope.

## Ground rules

- **Connected systems stay read-only.** A connector may authenticate and issue retrieval requests, but it must not start, stop, deploy, edit, or delete infrastructure.
- **Never ingest Docker container environment variables.** Do not add them to snapshots, logs, fixtures, exports, or persistence.
- Treat labels, proxy metadata, connector errors, and fixture files as potentially sensitive.
- Do not add telemetry, analytics, or mandatory cloud services.
- Preserve deterministic ordering and stable natural keys so repeated scans remain idempotent.
- Add tests for new connector parsing, reconciliation, security-sensitive behavior, and failure handling.

## Development setup

Install:

- Go 1.23 or newer
- Node.js 22 and npm
- Docker with Compose v2 for container and demo smoke tests
- Python 3 and curl for the portable smoke scripts

```sh
git clone https://github.com/HarshShah0203/homedex.git
cd homedex
npm --prefix web ci
go test ./...
npm --prefix web test
make build
```

`npm --prefix web run build` copies fingerprinted frontend output into `internal/server/static`, where the Go binary embeds it. Commit those generated embedded assets whenever frontend source changes. Do not hand-edit the generated files.

Useful checks:

```sh
make check
make smoke
docker compose -f demo/compose.yml up --build -d
```

`make check` includes the Docker-environment ingestion tripwire and hardened Compose assertions. The CI workflow additionally uses the race detector and enforces binary/image budgets.

## Connector shape

Connectors implement:

```go
type Connector interface {
    Kind() string
    Validate(context.Context, Config) error
    Scan(context.Context, Config) (domain.Snapshot, error)
}
```

A connector returns a complete observed `Snapshot` for its source and never writes the database. The engine owns persistence, diffing, gone-state handling, and route reconciliation.

When adding a connector:

1. Decode and validate a small explicit config type.
2. Give every entity a stable natural key.
3. Use bounded requests and honor context cancellation.
4. Return retrieval errors with enough context to diagnose the endpoint.
5. Add recorded, scrubbed fixtures and unit tests for both success and malformed input.
6. Document the least-privilege account/network setup in `docs/CONNECTORS.md`.
7. Verify that no mutation method or secret-bearing field is read.

The existing Docker, Traefik, Caddy, and NPM connectors demonstrate different patterns. Their exact line counts are not an API or complexity promise.

## Pull requests

- Keep commits scoped and explain the user-visible behavior.
- Include the commands you ran and their results.
- Do not include real hostnames, addresses, tokens, cookies, certificates, or exported lab data in tests or screenshots.
- Run formatters only on files you changed.
- Update operational docs when configuration, persistence, ports, or trust boundaries change.

Security reports must follow [SECURITY.md](SECURITY.md), not the public issue tracker.
