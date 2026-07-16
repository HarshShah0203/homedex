<div align="center">

# 📇 Homedex

**The missing inventory for your homelab.**

Point it at your Docker hosts and your reverse proxy. Get a living, searchable inventory of your entire homelab — every service, port, route, and expiry date.

*One read-only container · No agents · No telemetry · Your data never leaves your network · MIT*

`🚧 Status: pre-alpha — building in public. Star/watch to follow along.`

</div>

---

## Why

Every homelab reaches the same point: dozens of containers across a few machines, a reverse proxy handing out domains, and **nobody remembers what runs where**. The current options are a spreadsheet that's stale within a week, a wiki you'll never update, or NetBox — which is excellent, enterprise-grade, and 100% manual data entry.

Homedex takes the opposite approach: **documentation that writes itself**, discovered from what's actually running.

## What it does

Connect Homedex (read-only) to your Docker socket(s) and reverse proxy. Within a minute you get:

| View | What you see |
|---|---|
| **Services** | Every app: host, stack, image:tag, state, ports, URL, first/last seen, notes, tags |
| **Ports** | The global port matrix across all hosts — published vs internal, conflicts, next free port |
| **Routes** | The full chain: `photos.example.com → proxy → immich @ nas:2283 → container` — with broken routes flagged |
| **Expiry** | Every TLS cert (live-probed) and domain (RDAP), sorted by days remaining, with ntfy/Discord/webhook reminders |
| **Changes** | A diff feed per scan: new containers, changed ports, renewed certs, things that disappeared |
| **Search** | Global cmd-K across services, ports, domains, IPs, notes, tags |
| **Copy my lab** | One-click, secret-free markdown export of your whole lab — paste it into your AI assistant when troubleshooting |

Everything discovered can be enriched by hand: markdown notes, tags, custom fields (warranty dates, locations), and manual entries for the things that can't be discovered (router, printer, that one VPS).

## What it deliberately does NOT do

Homedex is a **ledger**, and only a ledger:

- ❌ No monitoring or uptime checks → use [Uptime Kuma](https://github.com/louislam/uptime-kuma) / [beszel](https://github.com/henrygd/beszel)
- ❌ No network diagrams → use [Scanopy](https://github.com/scanopy/scanopy)
- ❌ No container management — Homedex is **read-only, forever**. It never gets write access to anything.

## Planned sources

Docker (local socket, remote hosts, [socket-proxy](https://github.com/Tecnativa/docker-socket-proxy) recommended) · Traefik · Caddy · Nginx Proxy Manager · TLS probing · RDAP domain expiry — then Proxmox VE, image-update awareness, opt-in network sweep, SSH facts, Tailscale, unRAID, TrueNAS, and more. The roadmap order is community-voted.

## Quickstart

> Coming with `v0.1.0` — a single `docker run` / compose block. Watch the repo for the release.

## Architecture

Single Go binary, SQLite, embedded Svelte UI, one port. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full technical design (schema, connector framework, route-resolution algorithm, security model).

## Privacy & security posture

- Read-only credentials everywhere; there is no code path that mutates a connected system
- No telemetry, no phone-home, update check off by default, works fully air-gapped
- Container env vars are **never ingested** — not redacted later, never read at all
- See [SECURITY.md](SECURITY.md)

## Contributing

Connectors are designed to be added in ~200 lines — see [CONTRIBUTING.md](CONTRIBUTING.md). Issues labeled `good-first-issue` are genuinely that.

## License

[MIT](LICENSE)
