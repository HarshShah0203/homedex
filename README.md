<div align="center">

# Homedex

**The missing inventory for your homelab.**

Point Homedex at Docker and a supported reverse proxy to build a searchable record of services, hosts, ports, routes, certificates, domains, and changes.

[![CI](https://github.com/HarshShah0203/homedex/actions/workflows/ci.yml/badge.svg)](https://github.com/HarshShah0203/homedex/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

</div>

Homedex is **the ledger, not the map**: it answers “what runs where?” from observed infrastructure instead of asking you to maintain another spreadsheet. It does not start, stop, or reconfigure containers.

> **v0.1 status:** the discovery engine, authenticated API, scheduled reconciliation, change feed, route resolution, TLS/RDAP connectors, exports, read-only shares, notifications, manual records/metadata, and embedded Svelte inventory UI are implemented. The current UI controls for connector editing and several advanced workflows are visual previews; those operations are available through the authenticated APIs but are not all wired to buttons yet.

## Quickstart

The default Compose stack builds locally, binds the UI to loopback, gives Homedex a persistent data volume, and puts a filtering proxy between Homedex and the Docker socket.

```sh
git clone https://github.com/HarshShah0203/homedex.git
cd homedex
docker compose up -d --build
until curl -fsS http://127.0.0.1:7377/api/health; do sleep 1; done
./scripts/add-connector.sh --setup docker "Local Docker" \
  docs/examples/connectors/docker-socket-proxy.json
```

The connector script prompts for a new admin password, creates the local account, saves the encrypted connector config, and runs the first scan. Open <http://127.0.0.1:7377> after it succeeds.

If `7377` is already occupied, set `HOMEDEX_PORT` when running Compose and point `HOMEDEX_URL` at the same loopback port for connector setup.

The onboarding screens currently preview the intended connector wizard but do not persist connector settings. Use `scripts/add-connector.sh` for v0.1. See [the connector guide](docs/CONNECTORS.md) for Traefik, Caddy, Nginx Proxy Manager, TLS, RDAP, remote Docker, and authenticated follow-up connector commands.

### Why the socket proxy matters

`docker-compose.yml` gives the raw socket only to `docker-socket-proxy` and sets `POST=0`, which rejects mutating Docker API methods. Homedex talks to that proxy over an internal network.

A bind such as `/var/run/docker.sock:/var/run/docker.sock:ro` **is not, by itself, a Docker API security boundary**. The mount flag prevents replacing the socket file; it does not make API requests through that socket read-only. Start with [the socket-proxy guide](docs/DOCKER_SOCKET_PROXY.md) before changing the default deployment.

## Deterministic fake lab

The demo is local, contains only fabricated `.example` data, and needs no Docker host or reverse proxy at runtime:

```sh
docker compose -f demo/compose.yml up -d --build
curl -fsS http://127.0.0.1:7377/api/health
# open http://127.0.0.1:7377
```

It seeds the real SQLite schema and API with 3 hosts, 12 services, 16 port allocations, 10 routes, 4 certificates, and 1 domain. Nine routes resolve through network aliases or a published host port; one intentionally broken route exercises the failure state. There is no hosted demo domain. See [demo/README.md](demo/README.md) for reset and native-binary commands.

## Implemented in v0.1

| Area | Current behavior |
|---|---|
| Docker | Discovers host facts, all containers, Compose metadata, image/tag/digest, state/health, ports, networks, aliases, and labels via Unix, TCP/TLS, or SSH endpoints |
| Reverse proxies | Reads Traefik HTTP API, Caddy admin config, and Nginx Proxy Manager proxy-host/certificate APIs |
| Route resolution | Joins upstreams to container network IPs, names/aliases, or host-published ports; unresolved routes are marked broken |
| Expiry data | Probes explicit TLS targets and queries explicit registrable domains through RDAP connectors |
| Inventory | Services, hosts, ports, routes, certificates, domains, connector status, scan history, and changes in SQLite |
| Search | FTS-backed API search plus the UI command palette |
| Scanning | Scan-on-create/update, manual scan API, and enabled-connector schedules (15 minutes by default) |
| Enrichment | Notes, tags, typed custom fields, manual hosts/services, and manual expiry records through authenticated APIs |
| Export | Deterministic Markdown, JSON, per-view CSV, and a 100 KiB context pack with tested label/domain/IP redaction |
| Sharing | Revocable, optionally expiring tokens restricted to read-only inventory/entity/export routes; private notes, fields, and labels are omitted |
| Notifications | Expiry and change rules delivered through configured Shoutrrr URLs, with deduplication and post-commit evaluation |
| Local security | Argon2id admin password, HttpOnly session cookie, CSRF checks, login throttling, secretbox-encrypted connector config |

Docker container environment variables are not represented in Homedex's snapshot model and are never read. Docker labels **are** inventory data and may contain sensitive values; audit labels before granting other people access to the Homedex UI or backups.

## Positioning

| Tool | Best at | Different from Homedex |
|---|---|---|
| NetBox | Intended-state IPAM/DCIM | Richer enterprise model, but generally maintained as source-of-truth data rather than this Docker/proxy discovery path |
| Scanopy | Discovered network topology and diagrams | A map; Homedex is a table-first service/port/route ledger |
| homepage / Homarr | Launching services | A dashboard; Homedex records discovered infrastructure and changes |
| Uptime Kuma / Beszel | Health and metrics | Monitoring; Homedex does not perform uptime monitoring |
| Portainer / Komodo / Dockge | Managing workloads | Management planes; Homedex deliberately has no container lifecycle actions |

These tools can be complementary. Homedex is not a topology visualizer, monitor, orchestrator, or general-purpose CMDB.

## Deployment facts

- One Go process, one HTTP port (`7377`), one SQLite database.
- The container runs as distroless non-root with a read-only root filesystem; only `/data` is writable. Its minimal OpenSSH client supports `ssh://` Docker endpoints when a dedicated key and verified `known_hosts` directory are mounted read-only.
- Compose drops Linux capabilities, sets `no-new-privileges`, isolates the socket proxy, and binds the UI to `127.0.0.1`.
- There is no built-in TLS termination. Use a trusted reverse proxy and set `HOMEDEX_SECURE_COOKIES=true` for HTTPS deployments.
- There is no telemetry or update checker. Outbound connections occur only for configured connectors, TLS targets, and RDAP lookups.
- The image target is **under 30 MiB**, with a **40 MiB hard CI limit**.

Read [SECURITY.md](SECURITY.md), [backup and data handling](docs/BACKUP_AND_DATA.md), and [deployment security](docs/SECURITY_DEPLOYMENT.md) before exposing the UI beyond localhost.

## Build and test

Requirements: Go 1.23+, Node.js 22+, and Docker with Compose v2.

```sh
make build       # builds the web app, syncs embedded assets, then builds Go
make check       # Go/frontend tests and security contract checks
make smoke       # startup budget and seeded API smoke tests
make image       # local distroless image
```

CI runs Go tests with the race detector, `go vet`, frontend checks/tests/build, production E2E against the seeded embedded UI/API, embedded-asset drift, dependency-advisory classification, mandatory export/share redaction tests, the no-environment-ingestion tripwire, fake-lab smoke, startup budget, Compose hardening assertions, container/OpenSSH smoke, binary size, and image size.

Tagged releases are configured through [GoReleaser](.goreleaser.yml) for Linux, macOS, and Windows archives plus checksums/SBOMs. The release workflow builds `linux/amd64`, `linux/arm64`, and `linux/arm/v7` images for GHCR. See [docs/RELEASING.md](docs/RELEASING.md); do not assume an image tag exists until a corresponding GitHub release is published.

## Documentation

- [Docker socket proxy](docs/DOCKER_SOCKET_PROXY.md)
- [Connector configuration](docs/CONNECTORS.md)
- [Backup, restore, and data ownership](docs/BACKUP_AND_DATA.md)
- [Frontend dependency security](docs/DEPENDENCY_SECURITY.md)
- [Deployment security](docs/SECURITY_DEPLOYMENT.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Release process](docs/RELEASING.md)
- [Contributing](CONTRIBUTING.md)

## License

[MIT](LICENSE)
