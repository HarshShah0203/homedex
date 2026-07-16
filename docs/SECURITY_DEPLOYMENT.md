# Deployment Security

Read [SECURITY.md](../SECURITY.md) and begin with [the Docker socket proxy](DOCKER_SOCKET_PROXY.md). A read-only raw-socket bind alone is not a complete boundary.

## Default Compose protections

The repository stack:

- gives the raw Docker socket only to `docker-socket-proxy`;
- denies POST and lifecycle operations at the socket proxy;
- puts the proxy on an internal network with no host-published port;
- runs Homedex and the proxy with read-only root filesystems;
- drops all Linux capabilities and sets `no-new-privileges`;
- runs Homedex as distroless UID/GID `65532`;
- keeps `/data` in a dedicated writable volume;
- binds the UI to `127.0.0.1:7377` by default.

Run `./scripts/check-compose-security.sh` after editing Compose. The check validates the rendered configuration rather than only grepping YAML.

These are defense-in-depth controls, not proof against Docker daemon, kernel, dependency, or application vulnerabilities.

## Exposing the UI through a reverse proxy

Homedex serves HTTP. Terminate TLS at your reverse proxy and keep direct port `7377` reachable only by that proxy. Set:

```yaml
environment:
  HOMEDEX_SECURE_COOKIES: "true"
```

Do not publish `7377` on all interfaces unless a host firewall enforces the intended source addresses. If your proxy runs in the same Compose project, prefer a shared private network and no Homedex `ports:` entry.

Homedex does not currently enforce an external base URL or proxy identity. Configure host allowlists, request-size limits, TLS policy, and authentication at the proxy as appropriate.

## Authentication modes

Normal mode uses the local admin password, session cookie, CSRF token, and login throttle.

`HOMEDEX_NO_AUTH=true` bypasses application authentication and CSRF. Use it only when:

1. a trusted upstream authenticates every request;
2. clients cannot bypass that upstream and reach Homedex directly;
3. the upstream strips untrusted identity headers; and
4. the access policy also protects API and SSE routes.

The demo uses no-auth because it binds only to loopback and contains fabricated data. Do not copy that setting into a real internet-facing deployment.

## Connector network policy

Allow Homedex egress only to endpoints it needs:

- internal socket proxies;
- configured Traefik, Caddy, and NPM APIs;
- configured Docker SSH hosts on TCP 22 when that connector mode is used;
- configured TLS targets;
- IANA and registry RDAP endpoints if RDAP is enabled.

Do not expose Docker TCP 2375, Caddy 2019, Traefik's unauthenticated dashboard/API, or NPM's admin API to the public internet for Homedex. Use internal networks, firewall allowlists, private overlays, or mTLS as supported by the upstream.

For Docker-over-SSH, mount the dedicated key and verified `known_hosts` file read-only at `/home/nonroot/.ssh`; never disable host-key verification. The corresponding remote account can reach the Docker daemon and is therefore privileged even though Homedex itself issues only inventory calls. See [the connector guide](CONNECTORS.md#docker-over-ssh-in-the-stock-image).

## Files and backups

Protect `/data` as sensitive infrastructure data. The key file encrypts connector config, but SQLite inventory and many metadata fields are not generally encrypted at rest. Use encrypted host storage and encrypted backups when required.

Back up the database and its key together for recoverability, but control access to both. See [BACKUP_AND_DATA.md](BACKUP_AND_DATA.md).

## Labels and logs

Homedex never reads container environment variables. It does retain Docker labels because Compose metadata and route discovery depend on them. Do not put credentials in Docker labels; if another tool already does, treat Homedex database/UI access accordingly.

Connector error strings are surfaced for diagnosis and can contain endpoint details. Avoid upstream systems that include credentials in URLs or error messages.
