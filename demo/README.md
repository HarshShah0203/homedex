# Homedex Fake Lab

This fixture exercises the real migrations, reconciliation engine, route resolver, API, and embedded UI without contacting Docker, a reverse proxy, TLS endpoints, or RDAP.

All inventory is deterministic and fabricated under `.example` names:

- 3 Docker hosts
- 12 services
- 16 TCP/UDP port allocations
- 10 TLS routes
- 4 certificates and 1 domain
- 8 high-confidence alias routes
- 1 medium-confidence published-host-port route
- 1 intentionally broken route

The fake connector is disabled after seeding so the scheduler does not contact its placeholder endpoint.

## Compose

```sh
docker compose -f demo/compose.yml up -d --build
curl -fsS http://127.0.0.1:7377/api/health
```

Open <http://127.0.0.1:7377>. The demo disables application auth, but it binds to loopback and contains no real lab data. Do not expose this deployment as a public service.

Set `HOMEDEX_DEMO_PORT` to use another loopback port, for example `HOMEDEX_DEMO_PORT=17379 docker compose -f demo/compose.yml up -d --build`.

Reset it by deleting only the demo volume and recreating the stack:

```sh
docker compose -f demo/compose.yml down -v
docker compose -f demo/compose.yml up -d --build
```

## Native binaries

```sh
go run ./demo/seed --data-dir ./data-demo --reset
HOMEDEX_DATA_DIR=./data-demo HOMEDEX_NO_AUTH=true go run ./cmd/homedex
```

The UI is then available at <http://127.0.0.1:7377> if `HOMEDEX_LISTEN` is left at its default. Remove `./data-demo` when finished.

## Smoke test

After building `./homedex`:

```sh
./scripts/smoke-fake-lab.sh ./homedex
```

The script creates an isolated temporary seed, starts the application, verifies API counts and resolution outcomes, then cleans up.
