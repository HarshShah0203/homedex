# Connector Configuration

Add sources in the UI under **Sources**, or script them with `scripts/add-connector.sh` and the JSON config files below.

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

`HOMEDEX_URL` tells this script where Homedex is (default `http://127.0.0.1:7377`); it is not a server setting. To reach the UI from other machines, see `HOMEDEX_BIND` in the README quickstart. `--no-auth` is available only for instances intentionally running with `HOMEDEX_NO_AUTH=true`. The API encrypts config before writing SQLite and performs an initial scan immediately. A failed scan returns an error while preserving connector status for diagnosis.

## Docker

Recommended Compose-network config:

```json
{
  "endpoint": "tcp://docker-socket-proxy:2375",
  "host_name": "docker-local",
  "host_address": "127.0.0.1"
}
```

Homedex calls Docker version, info, container list (including stopped containers), container inspect, and image list. It maps Compose project/service labels, image details, state/health, restart policy, published/internal ports, and network IPs/aliases. The image list supplies only the registry digests each image was pulled as (`RepoDigests`), which [image update checks](#image-updates-registry) compare with the registry; it carries no image configuration. If the socket proxy refuses it (`IMAGES=0`), the scan still succeeds and update status reads unknown. It never reads `Config.Env`.

Supported endpoint forms:

- `unix:///var/run/docker.sock` — native/raw socket; discouraged in containers because `:ro` is not an API boundary.
- `tcp://host:2375` or `http://host:2375` — no transport authentication; restrict to a private network.
- `https://host:2376` with `tls_verify`, `ca_cert`, and optional `client_cert`/`client_key` paths — mount certificate files read-only into the Homedex container.
- `ssh://user@host` — uses the system OpenSSH client. Native binaries use the operator's normal SSH setup; the stock container includes a minimal `ssh`/`ssh-keyscan` runtime and reads `/home/nonroot/.ssh`.

`host_address` should be the address reverse proxies use when they target a host-published port; it enables medium-confidence route resolution.

Read [DOCKER_SOCKET_PROXY.md](DOCKER_SOCKET_PROXY.md) before deviating from the Compose default.

### Podman

Podman serves a Docker-compatible API, so add it as a `docker` source. Enable the socket (`sudo systemctl enable --now podman.socket`, or `systemctl --user enable --now podman.socket` for rootless) and set `endpoint` to `unix:///run/podman/podman.sock` (rootful) or `unix:///run/user/<uid>/podman/podman.sock` (rootless). Tested against Podman 5.8: host, containers, images, Compose labels, and published ports are discovered the same way as with Docker.

### Docker over SSH in the stock image

Prepare a dedicated directory on the Docker host running Homedex. Generate the key outside the container, authorize only that public key on the remote account, and verify the server fingerprint out of band before accepting it:

```sh
sudo install -d -m 0700 -o 65532 -g 65532 /opt/homedex-ssh
sudo install -m 0600 -o 65532 -g 65532 ./id_ed25519 /opt/homedex-ssh/id_ed25519
ssh-keyscan -H docker-host.example.net > /var/tmp/homedex-known-hosts
ssh-keygen -lf /var/tmp/homedex-known-hosts   # compare with the remote host's trusted fingerprint
sudo install -m 0600 -o 65532 -g 65532 /var/tmp/homedex-known-hosts /opt/homedex-ssh/known_hosts
```

Do not populate `known_hosts` by disabling `StrictHostKeyChecking`, and do not put a private key in connector JSON or the database. Mount the directory read-only into the Homedex service:

```yaml
services:
  homedex:
    volumes:
      - homedex-data:/data
      - /opt/homedex-ssh:/home/nonroot/.ssh:ro
```

Then use an endpoint such as `ssh://homedex@docker-host.example.net`. OpenSSH enforces private-key permissions, so the files must be readable by container UID/GID `65532` but not group/world-readable. The remote account must be able to run `docker system dial-stdio`; membership in the remote `docker` group is effectively root-equivalent access to that host. Restrict the account/key at the SSH server where practical and allow Homedex egress only to the intended host on TCP 22.

## SSH host (agentless)

For hosts where exposing the Docker API is not an option, and for bare hosts that run no Docker at all. Homedex connects with a dedicated read-only account and records what it observes: `uname`/`hostname` facts, container facts via `docker ps` when the account can run it, and listening sockets via `ss` either way. Listeners bound to loopback are recorded as internal; everything else as published. Ports already explained by Docker (including `docker-proxy` listeners) are not double-counted.

Configuration:

- **SSH host** — `host` or `host:port` (defaults to 22)
- **User** — a dedicated account; it needs no shell profile, sudo, or docker group membership (without docker access you still get host + port facts)
- **Private key** — PEM-encoded; key auth only, passwords are deliberately unsupported
- **Key passphrase** — optional
- **Host key fingerprint** — required pin. Leave it empty and press *Test connection* once: the error shows the fingerprint the server presented; paste it to pin. Homedex refuses to talk to a host whose key does not match the pin.

The account's credentials are sealed with the instance key like every connector secret. Commands run with a 10 second timeout each and the connector never writes anything over the session. `docker ps` reports no registry digests, so [image update checks](#image-updates-registry) skip containers found this way; add the host as a Docker source over SSH to cover them.

Troubleshooting:

- **"accepted the connection but rejected this key"** — the transport and host key were fine, the server refused the key. The error prints the exact fingerprint and `authorized_keys` line Homedex offered: append that line to `~/.ssh/authorized_keys` for the user you configured, or correct the user name. Note this is per-account: a key that works for your login is not automatically authorized for the read-only account.
- **"this key is passphrase protected"** — fill the key passphrase field.
- **"that is a public key"** — paste the private key file (`~/.ssh/id_ed25519`), not `id_ed25519.pub`.
- Keys pasted with surrounding blank lines or editor indentation are handled; a stray value autofilled into the passphrase field by a browser is ignored for unencrypted keys.

## Tailscale

Reads your tailnet's device list from the Tailscale API. Each device becomes a host of kind `tailscale`: its name is the OS hostname, its address the tailnet IPv4, and its aliases the IPv6 address, the MagicDNS name and the MagicDNS short name. The device's own "last seen on tailnet" time is shown on the Hosts page; it changes on every poll, so it never produces a change-feed entry. Devices removed from the tailnet go gone like any other host.

Only the default device fields are requested. Node keys, machine keys, owners' email addresses and connectivity endpoints are never read.

**Routes over the tailnet.** A proxy upstream written as a tailnet IP, MagicDNS name or short name resolves, at medium confidence, to the service publishing that port on the same machine as seen by the Docker or SSH connector. A device is linked to a Docker, SSH or manual host whose address is one of the device's tailnet addresses or names; failing that, to the one such host with the same short hostname. Each machine can be claimed by only one device. If two hosts or two devices match, Homedex does not guess: give the machines distinct names, set the host's address to its tailnet IP, or remove stale devices from the tailnet. A proxy whose URL is a tailnet name is linked to that machine on its next scan.

Recommended config, an OAuth client:

```json
{
  "tailnet": "-",
  "oauth_client_id": "replace-with-oauth-client-id",
  "oauth_client_secret": "replace-with-oauth-client-secret"
}
```

Create it in the Tailscale admin console under **Settings → Trust credentials (OAuth clients)** with a single scope, **Devices → Core → Read**. Homedex exchanges it for a short-lived token at `POST /api/v2/oauth/token` (authentication only, not a write), requesting `devices:core:read` every time, and holds the token in memory.

The simple alternative is an API access token:

```json
{ "tailnet": "-", "api_key": "replace-with-api-access-token" }
```

An access token acts with every permission of the user who created it and expires within 90 days, so prefer the OAuth client.

`tailnet` `"-"` means the credential's own tailnet. Secrets are sealed with the instance key like every connector secret, never returned by the API, and never included in exports or error messages. Redirects are not followed, and `base_url` (for tests only) must be HTTPS unless it points at a loopback address. The only egress needed is HTTPS to `api.tailscale.com`.

Troubleshooting:

- **401** — the client secret or token is wrong, revoked or expired.
- **403** — the OAuth client is missing the Devices → Core → Read scope.
- **404** — the tailnet name is wrong; use `-`.
- **"that is an OAuth client secret"** or **"that is an auth key"** — the value was pasted into the wrong field; auth keys (`tskey-auth-…`) add devices and cannot read the API.

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

Enable Traefik's API only on a private management entrypoint/network. Prefer authentication middleware or a private network over publishing an unauthenticated dashboard port. The optional basic-auth or header fields support deployments that already protect the API; the supplied header value is sensitive connector config. When either is set, Homedex does not follow a redirect, so a credential is never resent to another host or over plain HTTP: point `url` straight at the API, or the scan fails with "Traefik API returned 301 Moved Permanently; Homedex does not follow a redirect with credentials, check the URL".

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

Homedex authenticates with `POST /api/tokens`, caches the returned JWT, refreshes it once on `401`, and GETs `/api/nginx/proxy-hosts` plus `/api/nginx/certificates`. It does not create or modify NPM objects. Those GETs carry the JWT, so they never follow a redirect: point `url` straight at the NPM API.

Use a dedicated account and restrict the NPM API network path. NPM role granularity varies by version; verify the effective permissions in your installation rather than assuming the account is enforced read-only.

## nginx (config files)

Plain nginx and linuxserver SWAG have no admin API, so Homedex reads their configuration files from a read-only mount. nginx itself does not need to be reachable, and the connector makes no network connections.

Config:

```json
{
  "path": "/etc/nginx",
  "path_map": [],
  "host": "",
  "base_domain": ""
}
```

- **`path`** (required) — where the config is mounted inside the Homedex container: the main `nginx.conf`, or a directory. For a directory Homedex reads `nginx.conf` if present, otherwise every `*.conf`, otherwise every regular file (so a bare `sites-enabled` works).
- **`path_map`** — `NGINX_PATH=HOMEDEX_PATH` entries for when the files are mounted somewhere other than where nginx sees them. Absolute `include` paths and absolute symlink targets are rewritten through the longest matching prefix.
- **`host`** — the name or address of the machine nginx runs on. Optional; it links the proxy to that host so `localhost` and published-port upstreams resolve on the right machine.
- **`base_domain`** — optional; names catch-all server blocks and completes SWAG-style `name.*` server names.

Mount the config read-only. Examples:

```yaml
# Debian/Ubuntu nginx, same paths inside the container
services:
  homedex:
    volumes:
      - /etc/nginx:/etc/nginx:ro
# config: {"path": "/etc/nginx"}

# SWAG: mount only the nginx directory
      - ./swag/config/nginx:/config/nginx:ro
# config: {"path": "/config/nginx", "host": "swag", "base_domain": "example.com"}

# Mounted elsewhere: map nginx's own paths onto the mount
      - /etc/nginx:/nginx:ro
# config: {"path": "/nginx", "path_map": ["/etc/nginx=/nginx"]}
```

What it reads: the entry config and anything reached through `include` (globs expanded in sorted order, relative paths resolved against the main config's directory). Symlinks such as `sites-enabled` are followed only when they resolve inside a mounted path. It never opens certificates, keys, htpasswd files or logs, and it never stores raw configuration.

What it extracts, per `server` block and `location`:

- `server_name` values, wildcards kept. Regex, IP and variable names are dropped, and a server left with no usable name is skipped. Only an explicit `server_name _` counts as the catch-all, listed under `base_domain` when one is set.
- TLS from `listen ... ssl` / `quic` or legacy `ssl on`.
- `proxy_pass` upstreams, with `set $var ...` variables substituted (server scope, then location scope), as SWAG proxy-confs use them. `upstream {}` groups produce one route per member. `unix:` sockets are skipped. An upstream that still contains an unresolved variable is shown verbatim and marked broken rather than hidden.
- A location that reaches the same upstream as a shorter prefix in the same server block is folded into it (SWAG's `/radarr/api` under `/radarr`), so the register lists each domain-to-container path once. Regex, named and internal locations appear only when they reach an upstream no plain location does.

A parse error fails the scan and keeps the previous inventory. Reads and output are bounded (file size, file count, total bytes, include depth, names per server, upstream members, expanded variables and total routes), include cycles are detected, and anything resolving outside the mounted paths is skipped. Only regular files are opened, without following a final symlink swapped in after the path check.

Troubleshooting:

- **permission denied** — the files must be readable by the Homedex user (UID 65532).
- **no server blocks found** — the includes probably point at paths that are not mounted; mount the config at the same paths nginx uses, or add a `path_map` entry.

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

Homedex reduces names to registrable domains, skips IPs and non-registrable local suffixes, downloads the IANA RDAP bootstrap, and queries registry RDAP endpoints. Results are cached in process; failures degrade to unknown data. RDAP and image update checks are the only public-internet metadata lookups, and each occurs only when its source is configured.

## Image updates (registry)

Shows which running containers have a newer image published for the tag they run, like Diun or What's Up Docker but read-only and inside the inventory. It is opt-in: add an **Image updates (registries)** source, which checks once a day by default.

```json
{ "exclude": ["registry.lab.example/", "ghcr.io/my-org/"], "timeout_seconds": 10 }
```

Both keys are optional. `exclude` skips every image whose reference starts with one of the prefixes, as the container names it (`nginx:`) or fully qualified (`docker.io/library/nginx`). `timeout_seconds` (1 to 60, default 10) bounds each request.

**What it compares.** Before each check Homedex hands the source the distinct `image:tag` references of the containers a Docker source found (Podman included; stopped containers included, gone ones not). Containers an [SSH host](#ssh-host-agentless) source lists are not checked and show no status: `docker ps` over SSH reports no registry digests, so there would be nothing to compare them with. For each tag it asks the image's registry which digest the tag points at now, and compares that with the registry digest Docker recorded when the image the container runs was pulled (`RepoDigests`, from the Docker source's image list). For a multi-arch image both are the digest of the index (manifest list), for a single-arch image both are the manifest digest, so the comparison is exact. It detects a tag that moved to a new build (`nginx:1.27` rebuilt, `latest` re-pushed). It does not suggest newer version tags: a container on `v1.10.0` is up to date as long as `v1.10.0` itself has not been re-pushed.

The services API reports one status per container, and the Services ledger badges the ones with an update and can filter to them:

| Status | Meaning |
|---|---|
| `update_available` | The tag now points at a digest the container is not running |
| `up_to_date` | The container runs what the tag points at |
| `pinned` | The container names its image by digest (`image@sha256:...`); there is nothing to look up |
| `unknown` | Homedex cannot tell, and says why instead of guessing (below) |

A container shows no status until the source has checked its tag. Status follows the latest Docker scan, so a container recreated on the new image reads up to date on the next Docker scan without waiting for the next registry check. A tag that no container runs any more, or that you now skip, stops showing a status at the next check, and deleting the source removes every status it reported.

**What unknown means.** Homedex could not compare, and says why instead of guessing: Docker recorded no registry digest for the running image (built locally, loaded from a file, or a Docker source whose socket proxy refuses the image list, `IMAGES=0`); the digests Docker recorded belong to a different repository (a locally re-tagged image); the container names an image ID rather than a tag; the registry refused anonymous access, which is how private images read (they are not checked in this version, and no credentials can be configured) and also how Docker Hub answers for a name that does not exist there, such as a locally built image; the tag no longer exists; the registry is plain HTTP; or the registry sent a malformed or missing `Docker-Content-Digest` header. The reason is shown in the image cell's tooltip and returned as `update_reason`.

**Local builds.** With Docker's classic image store a locally built image has no registry digest, so it reads unknown. Docker's containerd image store (the default in Docker Desktop and on new Engine installs) records one for local builds too: the build's own digest. Homedex treats images Docker Compose built (they carry its `com.docker.compose.project` label) as having no registry digest, so a Compose `build:` service reads unknown under either store. An image built with plain `docker build` under the containerd store cannot be told apart from a pulled one: if you tag it with a name that is also published, for example `ghcr.io/me/app:latest` before you push it, it reads as `update_available`. Skip such images with `exclude`.

**Change feed.** The first time a registry reports a digest that some container is behind, Homedex files one change-feed entry for that `image:tag` ("Update available for traefik/whoami:v1.10.0"), with the running and published digests and how many containers are behind. The same digest never files a second entry, however many scans or containers see it; the next new build does. That holds when a container is removed and later re-created on the same old image, or a tag is skipped and then checked again: the digest last reported for a tag is kept for `HOMEDEX_GONE_RETENTION_DAYS` (30 by default) after the last container running it goes. Deleting the source forgets it, so a source added again reports each current update once more. Entries have entity type `image` and change kind `modified`, so existing change notification rules alert on them, and a rule with `"entity_types": ["image"]` alerts on nothing else. Digests are otherwise kept out of the change feed: filling in recorded digests after upgrading, or a scan that finds nothing new, files nothing.

**What is contacted, and how.** Only the registries named in the containers' image references, plus the token service a registry names in its challenge:

1. `HEAD https://<registry>/v2/<name>/manifests/<tag>` with an `Accept` header for the OCI index, Docker manifest list, and single Docker and OCI manifests. Docker Hub is contacted at `registry-1.docker.io`.
2. If the registry answers `401` with a `Bearer` challenge (Docker Hub, ghcr.io, lscr.io and most others), an anonymous `GET` of the token service it names (`auth.docker.io`, `ghcr.io/token`, ...) for `repository:<name>:pull` only, then the `HEAD` again with that token. quay.io answers without a token.
3. Only if a registry refuses `HEAD` (`405`/`501`) or answers it without a digest, a `GET` of the same manifest, of which only the headers are used. The digest always comes from the `Docker-Content-Digest` header; Homedex never hashes a manifest body, and never requests layers or blobs.

Every request is anonymous and read-only: no credentials are configured, stored, or sent, and a token only ever goes back to the registry that asked for it. Registries are contacted over HTTPS only, so a plain-HTTP private registry is unknown. Testing the source, before or after it is saved, sends one anonymous `GET /v2/` to each of up to three registries your containers use (Docker Hub first when any container uses it, then the registries serving the most tags; Docker Hub alone when no containers are known yet), and passes when any of them answers, the same rule a scan uses. A registry none of your containers use is not contacted.

**Rate limits.** Docker Hub does not count manifest `HEAD` requests against its pull rate limit, and Homedex uses `HEAD` for Docker Hub. Each scan still looks up each distinct tag once (two spellings of one image share the lookup), runs at most 4 lookups at a time, checks at most 200 tags and sends at most 808 requests. A registry that answers `429` is not contacted again for the rest of that scan, and neither is one whose connection has failed twice (refused, dropped by a firewall, timed out, or no DNS answer) without it answering anything in that scan, so a blocked registry costs about one timeout per concurrent lookup, not one per tag. Lookups are spread across registries, so one slow registry does not hold up the others. A rate limit, outage, or timeout keeps the previous answer and its check time, with the failure noted, rather than turning a known status into unknown. If not one registry answers, whether every request failed or the scan's time ran out first, the scan fails and the source shows the error, so a blocked egress path is visible rather than a quiet success.

Allow HTTPS egress to the registries your images come from (for Docker Hub: `registry-1.docker.io` and `auth.docker.io`) if you run a restrictive egress policy.

## API lifecycle

Creating or updating an enabled connector triggers a scan. Enabled connector records are then scanned on their configured schedule. Existing data is retained when a connector fails, while `last_status` and `last_error` report the problem. Disabling a connector stops scheduled scans without deleting its inventory.
