# Docker Socket Proxy

## Use the proxy first

The supported default is:

```text
Homedex --TCP/internal network--> docker-socket-proxy --Unix socket--> Docker daemon
```

Only the proxy container receives `/var/run/docker.sock`. It exposes the Docker API to Homedex on an internal Compose network and denies mutating HTTP methods.

The repository pins `tecnativa/docker-socket-proxy:v0.4.2` and explicitly sets:

```yaml
environment:
  CONTAINERS: 1
  IMAGES: 1
  INFO: 1
  NETWORKS: 1
  VERSION: 1
  POST: 0
  ALLOW_START: 0
  ALLOW_STOP: 0
  ALLOW_RESTARTS: 0
```

`POST=0` is the important API control. Section variables restrict which GET/HEAD paths are available. The lifecycle flags are explicitly disabled as defense-in-depth and to make the intended policy reviewable.

## A read-only socket bind is not enough

This mount remains security-sensitive:

```yaml
- /var/run/docker.sock:/var/run/docker.sock:ro
```

The `:ro` flag stops a container from changing the socket filesystem entry. It does **not** transform the Docker API into a read-only API. A process with a direct connection can still submit daemon operations. Never describe a raw read-only bind as a complete security boundary.

The proxy itself still has raw-socket access, so:

- keep it on an `internal: true` Docker network;
- do not publish its port to the host;
- drop capabilities and use a read-only root filesystem;
- pin a reviewed release and update intentionally;
- treat a proxy compromise as a potential Docker-host compromise.

## Verify the deployed policy

The repository check renders Compose and asserts the effective settings:

```sh
./scripts/check-compose-security.sh
```

From inside the Homedex network, GET access should work and mutation attempts should be denied by the proxy. Do not test a mutation against a host containing valuable workloads. Use an isolated disposable Docker daemon if you need an end-to-end policy test.

## Multiple Docker hosts

Run one socket proxy on each Docker host. Bind its listener only to a private management network and firewall it so only the Homedex host can connect. Plain TCP (`2375`) has no transport authentication; it is appropriate only inside a strongly isolated network. Prefer a private overlay/VPN or Docker TLS when traffic crosses hosts.

Add one Homedex Docker connector per proxy with a distinct `host_name` and `host_address`. See [CONNECTORS.md](CONNECTORS.md).

## Raw-socket fallback

If the proxy cannot be used, Homedex's Docker connector accepts `unix:///var/run/docker.sock`, but the container must then receive the raw socket. This materially weakens isolation even when the bind is marked `:ro`. Restrict access to Homedex, keep the container image trusted, and document the accepted risk. The project quickstart intentionally does not use this fallback.
