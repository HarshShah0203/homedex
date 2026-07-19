# Homedex Architecture

Homedex v0.1 is one Go process, one HTTP port (default `7377`), and one SQLite database. The Svelte SPA is compiled into `internal/server/static` and embedded in the binary.

```text
Docker / proxy / TLS / RDAP sources
                  |
                  v
       read-only connectors
                  |
                  v
        typed full snapshots
                  |
                  v
   reconcile + diff + route resolver
                  |
                  v
       SQLite (WAL + FTS5)
                  |
                  v
      JSON API / SSE / embedded SPA
```

## Runtime startup

`cmd/homedex/main.go`:

1. Resolves `HOMEDEX_DATA_DIR` (default `data`) and `HOMEDEX_LISTEN` (default `:7377`).
2. Creates or loads the secretbox key.
3. Opens SQLite and applies embedded, ordered SQL migrations.
4. Registers Docker, Traefik, Caddy, NPM, TLS probe, and RDAP connectors.
5. Starts the enabled-connector scheduler.
6. Serves health/version, setup/auth, inventory, connector, scan, search, SSE, and SPA routes.

The runtime has no required database server, queue, cache, cloud account, or telemetry service.

## Persistence

`modernc.org/sqlite` provides a pure-Go SQLite driver, so release builds use `CGO_ENABLED=0`. Startup applies SQL files from `internal/store/migrations` inside transactions. Connections use:

- `journal_mode=WAL`
- `busy_timeout=5000`
- `foreign_keys=ON`

The application serializes snapshot reconciliation with one process-local mutex while allowing concurrent readers. A scan run and its entity/port/change updates commit atomically.

Main records include connectors, hosts, services, service network aliases, ports, routes, certificates, domains, scan runs, changes, sessions/shares, tags/custom fields, manual expiries, notification rules, and delivery deduplication. FTS5 indexes searchable host/service/route/tag text.

## Connector boundary

```go
type Connector interface {
    Kind() string
    Validate(context.Context, Config) error
    Scan(context.Context, Config) (domain.Snapshot, error)
}
```

Connectors decode encrypted configuration, retrieve source state, and return domain snapshots. They do not receive the database handle. The engine owns reconciliation and marks no-longer-observed entities as gone rather than immediately deleting them.

Docker's snapshot model deliberately has no environment-variable field. It retains the network addresses and aliases needed for route resolution.

## Route resolution

For every active proxy route, resolution tries deterministic evidence in order:

1. Docker network IP plus matching internal port → `high` confidence.
2. Container name or network alias plus matching internal port → `high` confidence.
3. Docker host address plus a unique published port → `medium` confidence.
4. No unique match → `broken`, confidence `none`.

Resolution is rerun after snapshots are applied. The deterministic demo includes all three outcomes.

## Authentication and secrets

First-run setup stores an Argon2id password hash. Successful setup/login creates an opaque session token and separate CSRF token; only hashes are persisted. Connector config is secretbox-encrypted with an instance key from `/data/instance.key` or `HOMEDEX_SECRET`.

`HOMEDEX_NO_AUTH=true` bypasses both session and CSRF checks. It is a deployment escape hatch for a trusted authenticating reverse proxy, not a safe public mode.

## Build and release

The Dockerfile has separate Node, minimal OpenSSH-closure, Go dependency, application build, seed build, and distroless runtime stages. BuildKit cache mounts keep npm, module, and Go build caches out of final layers. The final image contains the application binary, distroless CA certificates, `ssh`/`ssh-keyscan` plus only their runtime libraries/configuration, an empty non-root SSH directory, and an empty `/data` directory.

GoReleaser cross-compiles CGO-free archives. GitHub Actions separately uses Buildx/QEMU for multi-architecture OCI images. CI targets an uncompressed image size below 30 MiB and fails at 40 MiB.

## Operational boundaries

- Homedex writes its own SQLite state; “read-only” refers to connected infrastructure, not `/data`.
- The process serves HTTP, not HTTPS.
- TLS and RDAP scans are explicit connector configurations in v0.1; proxy routes do not automatically create those connector records.
- The backend implements export, share, entity-enrichment, manual-entity, change-review, and notification APIs. The web UI wires the setup wizard, source management, change review, route register, expiry reminders, and context export to those APIs; remaining API-only surfaces (shares, manual entities, entity enrichment) are reachable through the authenticated JSON API. The README's implementation table is the user-facing contract.
