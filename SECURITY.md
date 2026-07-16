# Security Policy

## Start with a Docker socket proxy

Run Homedex behind a filtering socket proxy, as shown in the repository's `docker-compose.yml`. The proxy receives the raw socket, allows only the API sections Homedex reads, and sets `POST=0` so mutating Docker methods are rejected. Homedex itself never receives the raw socket.

Mounting `/var/run/docker.sock` with `:ro` **does not make the Docker API read-only**. It protects the socket filesystem entry from writes, but a client can still ask the Docker daemon to perform privileged operations through that socket. A raw socket remains effectively root-equivalent access to the Docker host. See [docs/DOCKER_SOCKET_PROXY.md](docs/DOCKER_SOCKET_PROXY.md).

The socket proxy narrows the API surface; it does not make the Docker daemon or container isolation infallible. Keep it on an internal network, do not publish port 2375, pin and update the proxy image deliberately, and protect the host as privileged infrastructure.

## Current security properties

- Docker discovery calls only version, info, container list, and container inspect operations. There are no container lifecycle or deployment calls.
- Container environment variables are absent from the snapshot model and are never read. Docker labels are stored as observed metadata and can themselves contain secrets; avoid secret-bearing labels.
- Traefik and Caddy connectors issue GET requests. Nginx Proxy Manager uses its token-authentication POST, then GETs proxy hosts and certificates; it does not edit NPM configuration.
- Connector configuration is authenticated-encrypted with NaCl secretbox before SQLite persistence. The key is either `/data/instance.key` (created mode `0600`) or the externally supplied `HOMEDEX_SECRET`.
- Export sanitization masks secret-like label keys/values and supports domain/external-IP masking. Read-only shares always omit private notes, custom fields, and labels; redaction and share-scope tests block CI.
- Admin passwords are stored as Argon2id hashes. Browser sessions use an HttpOnly, SameSite=Lax cookie; state-changing authenticated API requests require the session CSRF token. Login attempts are rate-limited.
- The supplied runtime image is distroless and non-root. Compose uses a read-only root filesystem, drops all Linux capabilities, sets `no-new-privileges`, and leaves only `/data` writable.
- Homedex has no telemetry or update checker. Network egress is caused only by connector endpoints, explicit TLS probe targets, and RDAP bootstrap/domain queries configured by the operator.

## Operator responsibilities and limitations

- Homedex contains a detailed infrastructure inventory. Authenticate it, terminate HTTPS at a trusted reverse proxy, set `HOMEDEX_SECURE_COOKIES=true`, and do not expose it directly to the internet.
- `HOMEDEX_NO_AUTH=true` removes application authentication. Use it only when a trusted upstream enforces access and direct access to Homedex is blocked. The local fake-lab Compose file uses it only with a loopback bind and fabricated data.
- Connector credentials have the privileges granted by the upstream system. Use dedicated least-privilege accounts and network ACLs even though Homedex's code path is retrieval-only.
- Caddy's admin endpoint is a powerful management interface. Homedex only reads `/config/`, but the endpoint itself must remain isolated from untrusted clients.
- Nginx Proxy Manager versions differ in account/role granularity. A dedicated account is still recommended, but do not call it read-only unless your NPM deployment actually enforces that role.
- Notification channel URLs can contain delivery credentials. API list responses expose only channel kinds, and destination URLs are authenticated-encrypted in SQLite with the instance SecretBox. Protect `/data`, the matching key, and backups; changing or losing the key makes encrypted connector and notification credentials unreadable.
- Raw Docker labels, connector errors, database backups, and the infrastructure graph may be sensitive. Limit filesystem and UI access accordingly.
- Homedex serves plain HTTP. TLS, trusted proxy configuration, host firewalling, backup encryption, and operating-system patching remain deployment responsibilities.

Additional hardening and network examples are in [docs/SECURITY_DEPLOYMENT.md](docs/SECURITY_DEPLOYMENT.md).

## Supported versions

Until the first stable release, security fixes are applied to the current development line. After tagged releases begin, this section will list supported release branches. Do not infer support for an old image solely because its tag remains downloadable.

## Reporting a vulnerability

Use GitHub's private **Security Advisories → Report a vulnerability** flow for this repository. Do not open a public issue containing exploit details, tokens, inventory data, or affected hostnames.

Include the affected commit/tag, deployment mode, reproduction steps, impact, and any suggested mitigation. The maintainer will acknowledge and triage the report privately; no fixed response-time SLA is promised at this stage.
