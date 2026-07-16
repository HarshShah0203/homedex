# Homedex Architecture

Single process, one port (default **7377**), one SQLite file. No external services. Works air-gapped.

```
                        ┌────────────────────────────────────────────┐
                        │              homedex (1 binary)            │
                        │                                            │
  Docker hosts ────────▶│  Connectors ──▶ Snapshot ──▶ Diff Engine   │
  (socket/proxy/TCP)    │  (goroutines)      │            │          │
  Traefik API ─────────▶│                    ▼            ▼          │
  Caddy admin API ─────▶│                 SQLite (WAL, FTS5)         │
  NPM API ─────────────▶│                    │                       │
  TLS probes ──────────▶│                    ▼                       │
  RDAP ────────────────▶│   REST API + SSE  ◀── embedded Svelte SPA  │
                        │        │                                   │
                        │        ▼                                   │
                        │  shoutrrr notifications (ntfy/discord/…)   │
                        └────────────────────────────────────────────┘
```

## Stack

| Layer | Choice |
|---|---|
| Backend | Go 1.23+, `chi` router, single static binary |
| DB | SQLite (`modernc.org/sqlite`, no CGO) + `sqlc` + FTS5 + embedded `golang-migrate` migrations; WAL mode, single writer goroutine |
| Docker | official `docker/docker/client` (unix socket, `tcp://` +TLS, `ssh://`, socket-proxy) |
| Domain expiry | RDAP (IANA bootstrap, key-free) + `x/net/publicsuffix` |
| Notifications | `containrrr/shoutrrr` (ntfy/Discord/Slack/webhook/SMTP) |
| Crypto | argon2id (admin password), NaCl secretbox (connector secrets at rest) |
| Frontend | Svelte 5 + Vite + Tailwind SPA, embedded via `embed.FS`; custom virtualized table component |
| Release | GoReleaser + GitHub Actions; multi-arch images (amd64/arm64/armv7) targeting <30MB |

**Budgets (CI-enforced):** cold start <2s · 100-container scan <10s · search <50ms · idle RSS <60MB · image <30MB.

## Repository layout

```
cmd/homedex/            # bootstrap: flags/env, embed web, start server + engine
internal/
  server/               # chi routes, sessions, SSE
  store/                # sqlc queries, migrations, FTS sync
  engine/               # scheduler, snapshot diff, change computation
  connectors/           # docker/ traefik/ caddy/ npm/ tlsprobe/ rdap/
  resolve/              # route → container resolution
  export/               # markdown/csv/json/context-pack + redaction
  notify/               # rules engine + shoutrrr
  auth/                 # argon2id, sessions, share tokens
web/                    # Svelte app → dist embedded
demo/                   # fake-lab compose + seeder (fixtures, e2e, public demo)
```

## Data model (summary)

`hosts` · `services` (natural-keyed, `first_seen`/`last_seen`, soft `gone` state) · `ports` (published vs internal) · `proxies` · `routes` (domain, upstream, `resolved_service_id`, confidence, `ok|broken|unknown`) · `certs` (probed `not_after`, chain validity) · `domains` (RDAP expiry) · `custom_fields` · `tags` · `scan_runs` · `changes` (added/removed/modified + JSON diff) · `connectors` (encrypted config) · `notification_rules` · `share_tokens` · `sessions`. FTS5 virtual table over names/images/notes/domains/tags, trigger-synced.

## Connector framework

```go
type Connector interface {
    Kind() string
    Validate(ctx context.Context, cfg Config) error
    Scan(ctx context.Context, cfg Config) (Snapshot, error)
}
```

Connectors return typed `Snapshot` sets with stable natural keys and **never touch the DB**. Per-connector ticker (default 15 min ± jitter), manual "Scan now", 60s timeout, errors surfaced verbatim in the UI. The diff engine upserts by natural key, marks absentees `gone` (hard-delete after N days), and emits `changes` rows for tracked fields only.

### Docker

`ContainerList(all)` + `ContainerInspect` (concurrency 8) + `Info`. Service name from `com.docker.compose.service` label, stack from `com.docker.compose.project`; ports from `NetworkSettings.Ports` + `HostConfig.PortBindings`; per-network IPs and aliases retained for route resolution. **`Config.Env` is never read.** Remote hosts via `tcp://`(+TLS), `ssh://`, or docker-socket-proxy (recommended, documented).

### Traefik / Caddy / NPM

- **Traefik:** `/api/http/routers|services|entrypoints`; parse `Host()`/`PathPrefix()` from rules; follow service → `loadBalancer.servers[].url`.
- **Caddy:** admin API `GET /config/`; recursive traversal of `apps.http.servers.*.routes[]` (incl. subroutes) collecting `match[].host[]` and `reverse_proxy` upstream `dial` targets.
- **NPM:** JWT via `POST /api/tokens`; `GET /api/nginx/proxy-hosts` (domains, forward host/port, locations) + `/api/nginx/certificates` (expiry).

### TLS prober & RDAP

Prober: `tls.Dial` (5s timeout, SNI, capture-even-if-invalid) → leaf expiry/issuer/SANs + separate chain verification; concurrency 10; daily + on-demand. RDAP: eTLD+1 via publicsuffix (skip `.local`/`.lan`/IPs), IANA bootstrap cached 7d, per-domain cache 24h, 1 req/s global, graceful degradation to "unknown".

## Route → container resolution

For each route upstream `(host, port)`:

1. **Container-network IP match** → confidence **high**
2. **Name match** (container name / compose service / network alias) → **high**
3. **Host-published port match** (upstream targets a Docker host address) → **medium**; if no container publishes it, link the host itself
4. **No match** → `status=broken` (surfaced red in the Routes view)

Re-resolved after every docker/proxy scan. Unit-tested against the fixture lab (IP match, alias match, published-port match, recreated container, dead route).

## Security model

- Read-only credentials everywhere; no write code paths toward connected systems
- Env vars never ingested; mount source paths only
- Connector secrets secretbox-encrypted; key = 0600 file in data dir or `HOMEDEX_SECRET`
- Sessions: HttpOnly SameSite=Lax cookie, CSRF header check on mutations, login rate-limited; `HOMEDEX_NO_AUTH` for trusted-proxy setups
- Share links: 128-bit tokens, hashed at rest, revocable, read-only scope
- Outbound network: only RDAP (opt-in) and user-configured notification targets; no telemetry, ever

## Testing

Connector unit tests against recorded API fixtures · resolution matrix tests · diff-engine idempotency (re-scan ⇒ zero changes) · Playwright e2e against the seeded fixture lab · CI-blocking redaction suite and golden-file exports · image/binary size and startup smoke gates.
