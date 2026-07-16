# Security Policy

Homedex holds a map of your infrastructure, so its security posture is deliberately boring:

- **Read-only everywhere.** Connectors use read-only credentials/scopes; no code path mutates a connected system. For Docker, we recommend and document [docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy) with only `CONTAINERS`, `INFO`, `NETWORKS`, `IMAGES`, `VERSION` enabled.
- **No secret ingestion.** Container environment variables are never read from the Docker API — not stored-then-redacted, never ingested at all.
- **No telemetry.** No usage stats, no phone-home. The update check reads GitHub releases and is off by default. Homedex works fully air-gapped.
- **Encrypted at rest.** Connector credentials are encrypted (NaCl secretbox) in the SQLite database.
- **Redaction-tested exports.** The "Copy my lab" export runs a redaction pass that is enforced by CI-blocking tests.

## Reporting a vulnerability

Please report vulnerabilities privately via **GitHub Security Advisories** ("Report a vulnerability" on the Security tab of this repo). Please do not open public issues for security reports. You'll get a response within 72 hours.
