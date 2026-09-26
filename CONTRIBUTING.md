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

The default `docker-compose.yml` pulls the published image. To run the full stack from your checkout with the same hardening, add the source-build override:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

`npm --prefix web run build:demo` builds the static live demo (fabricated data, no backend) into `web/dist-demo`, which the Pages workflow publishes from `main`. It never touches the embedded assets.

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

For HTTP APIs, use `connectors.GetJSON`, `connectors.PostJSON` (a JSON body) or `connectors.PostForm` (a form body) with a client that has an explicit timeout, such as `connectors.Client`. Each takes the scan's context, reads at most `connectors.MaxResponseBytes`, turns a non-2xx answer into a `*connectors.StatusError` named by `WithLabel`, and accepts `WithBasicAuth`, `WithBearerToken` and `WithHeader`. POST is only for retrieval that needs it, such as a login or a GraphQL query, never for a request that changes the connected system. The POST helpers never follow a redirect, so a body carrying a credential is not resent to another host. `GetJSON` follows redirects as the client does when no `WithBasicAuth`, `WithBearerToken` or `WithHeader` option is given. Go resends a custom header such as `X-API-Key` to any host a redirect names, and `Authorization` to the same host over plain HTTP, so with any of those options `GetJSON` follows a redirect only to the same host name (the port may change) and never from HTTPS to plain HTTP. A same-host upgrade from `http://` to `https://` keeps working; any other redirect comes back as a `*connectors.StatusError` with `RedirectRefused` set. Every `WithHeader` counts, even a harmless `Accept`, because the helper cannot tell a key from any other header. Never send a credential through your own redirect-following request instead.

A connector writes only its own rows, so a source that describes a machine another connector already reports emits a view host rather than a second machine. Kind `tailscale` is a tailnet device; kind `dns` is the set of names a local resolver answers for one IP, with that IP as `Address` and the names as `Aliases`. Route resolution links a view to the one machine reported at the same address and follows it to that machine's ports. A DNS view links by its IP only, never by a name, and a loopback or unspecified record links to nothing. Kinds `unraid`, `truenas` and `k8s-node` are machines, like `docker` and `ssh`. Any other new host kind needs a migration that widens the hosts table's kind check.

The existing Docker, Traefik, Caddy, and NPM connectors demonstrate different patterns. Their exact line counts are not an API or complexity promise.

## Pull requests

- Keep commits scoped and explain the user-visible behavior.
- Include the commands you ran and their results.
- Do not include real hostnames, addresses, tokens, cookies, certificates, or exported lab data in tests or screenshots.
- Run formatters only on files you changed.
- Update operational docs when configuration, persistence, ports, or trust boundaries change.

Security reports must follow [SECURITY.md](SECURITY.md), not the public issue tracker.
