# Deployment Security

Read [SECURITY.md](../SECURITY.md) and begin with [the Docker socket proxy](DOCKER_SOCKET_PROXY.md). A read-only raw-socket bind alone is not a complete boundary.

## Default Compose protections

The repository stack:

- pulls the published `ghcr.io/harshshah0203/homedex` image (`HOMEDEX_VERSION` pins a release), so the file works without a checkout;
- gives the raw Docker socket only to `docker-socket-proxy`;
- denies POST and lifecycle operations at the socket proxy;
- puts the proxy on an internal network with no host-published port;
- runs Homedex and the proxy with read-only root filesystems;
- drops all Linux capabilities and sets `no-new-privileges`;
- runs Homedex as distroless UID/GID `65532`;
- keeps `/data` in a dedicated writable volume;
- binds the UI to `127.0.0.1:7377` by default (`HOMEDEX_BIND` changes the address; see below before widening it).

Run `./scripts/check-compose-security.sh` after editing Compose. The check validates the rendered configuration rather than only grepping YAML, for both the default file and the `docker-compose.build.yml` source-build override.

These are defense-in-depth controls, not proof against Docker daemon, kernel, dependency, or application vulnerabilities.

## Exposing the UI through a reverse proxy

Homedex serves HTTP. Terminate TLS at your reverse proxy and keep direct port `7377` reachable only by that proxy. Set:

```yaml
environment:
  HOMEDEX_SECURE_COOKIES: "true"
```

Do not publish `7377` on all interfaces unless a host firewall enforces the intended source addresses. If your proxy runs in the same Compose project, prefer a shared private network and no Homedex `ports:` entry.

Homedex does not currently enforce an external base URL or proxy identity. Configure host allowlists, request-size limits, TLS policy, and authentication at the proxy as appropriate.

## Setting the admin password at startup

Until an admin password exists, `POST /api/setup` accepts the first password anyone sends, so whoever reaches a fresh instance first owns it. On loopback that is only you. Once the UI is published on a LAN, and every homelab app store (Portainer templates, TrueNAS, CasaOS, Cosmos, Unraid) publishes it, set the password before the first start instead:

| Variable | Meaning |
|---|---|
| `HOMEDEX_ADMIN_PASSWORD` | The admin password itself. |
| `HOMEDEX_ADMIN_PASSWORD_FILE` | Path to a file holding it, such as a mounted Docker or Compose secret. Exactly one trailing newline (LF or CRLF) is removed; any other whitespace is part of the password. The file may hold at most 4096 bytes. |

Setting both refuses to start. At startup, when no admin exists yet, Homedex validates the password exactly as the setup wizard does (12 or more characters) and stores only its Argon2id hash. A shorter password stops startup with an error that names the variable, never the value. The log records only that the admin password was set from the environment and which variable supplied it. The setup wizard then treats the instance as configured: it asks you to sign in and continues with your first source.

When an admin already exists, the variable is ignored, one log line says so, and the stored password is never replaced; remove the variable after the first start if you like. Homedex also removes both variables from its own process environment once it has read them, so child processes such as the ssh client for `ssh://` Docker sources never inherit them.

A plain environment variable is visible to anyone who can run `docker inspect` on the container. To keep it out of the container config, use a secret file. With Compose, add an override beside `docker-compose.yml`:

```yaml
# docker-compose.override.yml
services:
  homedex:
    environment:
      HOMEDEX_ADMIN_PASSWORD_FILE: /run/secrets/homedex_admin_password
    secrets:
      - homedex_admin_password
secrets:
  homedex_admin_password:
    file: ./admin_password.txt
```

Outside Swarm, Compose bind-mounts the file with its host ownership, and Homedex runs as UID `65532`, so make it readable by that user only: `sudo chown 65532:65532 admin_password.txt` and `chmod 0400 admin_password.txt`.

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
- IANA and registry RDAP endpoints if RDAP is enabled;
- `api.tailscale.com` over HTTPS if the Tailscale connector is enabled;
- the configured Proxmox VE node over HTTPS on TCP 8006 if the Proxmox connector is enabled. Homedex talks only to that one node; the node itself relays requests about guests on other cluster nodes, so no other node needs to be reachable from Homedex;
- HTTPS to the registries your containers' images come from, and the token services they name, if an image update source is added. For Docker Hub that is `registry-1.docker.io` and `auth.docker.io`; for ghcr.io and lscr.io, `ghcr.io` and `lscr.io`; for quay.io, `quay.io`. The requests are anonymous manifest lookups (no layers are downloaded), and a registry you do not allow simply reads as unknown. Docker Hub is contacted only when a container uses it, and testing the source passes as long as one of your containers' registries answers.

The nginx connector needs no egress at all, only a read-only mount of the nginx configuration.

Do not expose Docker TCP 2375, Caddy 2019, Traefik's unauthenticated dashboard/API, NPM's admin API, or the Proxmox VE API on 8006 to the public internet for Homedex. Use internal networks, firewall allowlists, private overlays, or mTLS as supported by the upstream.

For Docker-over-SSH, mount the dedicated key and verified `known_hosts` file read-only at `/home/nonroot/.ssh`; never disable host-key verification. The corresponding remote account can reach the Docker daemon and is therefore privileged even though Homedex itself issues only inventory calls. See [the connector guide](CONNECTORS.md#docker-over-ssh-in-the-stock-image).

For Proxmox VE, give Homedex a privilege-separated API token of a dedicated user holding only PVEAuditor, or the tighter custom roles in [the connector guide](CONNECTORS.md#proxmox-ve), never a `root@pam` token or one without privilege separation. Never grant `VM.Monitor` on PVE 8: it also allows running commands, reading and writing files and changing passwords inside every guest. Pin the node's certificate fingerprint or the cluster CA rather than trusting whatever certificate answers.

## Files and backups

Protect `/data` as sensitive infrastructure data. The key file encrypts connector config, but SQLite inventory and many metadata fields are not generally encrypted at rest. Use encrypted host storage and encrypted backups when required.

Back up the database and its key together for recoverability, but control access to both. See [BACKUP_AND_DATA.md](BACKUP_AND_DATA.md).

## Labels and logs

Homedex never reads container environment variables. It does retain Docker labels because Compose metadata and route discovery depend on them. Do not put credentials in Docker labels; if another tool already does, treat Homedex database/UI access accordingly.

Connector error strings are surfaced for diagnosis and can contain endpoint details. Avoid upstream systems that include credentials in URLs or error messages.
