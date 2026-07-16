# Connector Configuration

Homedex v0.1 exposes connector CRUD/test/scan APIs. The visual connector editor is not yet the supported persistence path, so use `scripts/add-connector.sh` with JSON config files.

For the first connector:

```sh
./scripts/add-connector.sh --setup docker "Local Docker" \
  docs/examples/connectors/docker-socket-proxy.json
```

For later connectors, omit `--setup`; the script prompts for the existing admin password:

```sh
./scripts/add-connector.sh traefik "Gateway Traefik" \
  docs/examples/connectors/traefik.json
```

Set `HOMEDEX_URL` if Homedex is not at `http://127.0.0.1:7377`. `--no-auth` is available only for instances intentionally running with `HOMEDEX_NO_AUTH=true`. The API encrypts config before writing SQLite and performs an initial scan immediately. A failed scan returns an error while preserving connector status for diagnosis.

## Docker

Recommended Compose-network config:

```json
{
  "endpoint": "tcp://docker-socket-proxy:2375",
  "host_name": "docker-local",
  "host_address": "127.0.0.1"
}
```

Homedex calls Docker version, info, container list (including stopped containers), and container inspect. It maps Compose project/service labels, image details, state/health, restart policy, published/internal ports, and network IPs/aliases. It never reads `Config.Env`.

Supported endpoint forms:

- `unix:///var/run/docker.sock` — native/raw socket; discouraged in containers because `:ro` is not an API boundary.
- `tcp://host:2375` or `http://host:2375` — no transport authentication; restrict to a private network.
- `https://host:2376` with `tls_verify`, `ca_cert`, and optional `client_cert`/`client_key` paths — mount certificate files read-only into the Homedex container.
- `ssh://user@host` — supported by native binaries when an `ssh` executable and keys are available. The distroless Homedex image intentionally contains no SSH client, so this mode is not available in the stock container.

`host_address` should be the address reverse proxies use when they target a host-published port; it enables medium-confidence route resolution.

Read [DOCKER_SOCKET_PROXY.md](DOCKER_SOCKET_PROXY.md) before deviating from the Compose default.

## Traefik

Config keys:

```json
{
  "url": "http://traefik:8080",
  "username": "",
  "password": "",
  "header": "",
  "header_value": ""
}
```

Homedex GETs `/api/version`, `/api/entrypoints`, `/api/http/routers`, and `/api/http/services`. It parses `Host(...)` and `PathPrefix(...)`, then follows load-balancer server URLs.

Enable Traefik's API only on a private management entrypoint/network. Prefer authentication middleware or a private network over publishing an unauthenticated dashboard port. The optional basic-auth or header fields support deployments that already protect the API; the supplied header value is sensitive connector config.

## Caddy

Config:

```json
{ "url": "http://caddy:2019" }
```

Homedex issues `GET /config/` and recursively walks host/path matchers, nested subroutes, and `reverse_proxy` upstream dials.

Caddy's admin API is a management interface capable of changing configuration even though Homedex only sends GET. Keep it on a private network and use Caddy's admin access controls/network policy. Do not expose port `2019` publicly for Homedex.

## Nginx Proxy Manager (NPM)

Config:

```json
{
  "url": "http://npm:81",
  "email": "homedex-reader@example.invalid",
  "password": "replace-with-a-dedicated-account-password"
}
```

Homedex authenticates with `POST /api/tokens`, caches the returned JWT, refreshes it once on `401`, and GETs `/api/nginx/proxy-hosts` plus `/api/nginx/certificates`. It does not create or modify NPM objects.

Use a dedicated account and restrict the NPM API network path. NPM role granularity varies by version; verify the effective permissions in your installation rather than assuming the account is enforced read-only.

## TLS probe

TLS targets are explicit in v0.1; proxy routes do not automatically create the connector.

```json
{
  "targets": ["photos.example.net:443", "https://docs.example.net"],
  "timeout_seconds": 5
}
```

The connector performs TLS handshakes, records the leaf subject/SANs/issuer/expiry, and separately verifies the chain. It can retain observed certificate metadata even when verification fails. Every target causes outbound traffic from Homedex.

## RDAP

Domains are also explicit:

```json
{ "domains": ["example.net", "photos.example.net"] }
```

Homedex reduces names to registrable domains, skips IPs and non-registrable local suffixes, downloads the IANA RDAP bootstrap, and queries registry RDAP endpoints. Results are cached in process; failures degrade to unknown data. This is the only default public-internet metadata lookup, and it occurs only when an RDAP connector is configured.

## API lifecycle

Creating or updating an enabled connector triggers a scan. Enabled connector records are then scanned on their configured schedule. Existing data is retained when a connector fails, while `last_status` and `last_error` report the problem. Disabling a connector stops scheduled scans without deleting its inventory.
