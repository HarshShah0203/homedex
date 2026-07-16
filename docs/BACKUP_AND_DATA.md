# Backup, Restore, and Data Ownership

Homedex keeps operational state in its data directory:

```text
/data/homedex.db      SQLite database
/data/homedex.db-wal  SQLite WAL, present while active
/data/homedex.db-shm  SQLite shared-memory file, present while active
/data/instance.key    32-byte secretbox key, unless HOMEDEX_SECRET is used
```

The default Compose stack stores this directory in the `homedex_homedex-data` named volume. A bind mount may be used instead if your backup system works on host paths.

## What must be backed up

Back up the entire data directory, including `instance.key`. The database contains inventory, password/session/share-token hashes, changes, encrypted connector configs, and notification rules whose destination URLs are encrypted because they may embed delivery credentials. Treat the database as sensitive. Without the matching key, encrypted connector configs and notification destinations cannot be recovered.

If `HOMEDEX_SECRET` supplies the key, it is not written to `/data`; back it up separately in your secret manager. `HOMEDEX_SECRET` is base64 for exactly 32 bytes. Do not store it in the same unencrypted archive as the database if that defeats your threat model.

## Consistent backup with the default volume

The simplest reliable procedure stops Homedex long enough to checkpoint/close SQLite, while leaving discovered infrastructure untouched:

```sh
mkdir -p backups
docker compose stop homedex
docker run --rm \
  -v homedex_homedex-data:/data:ro \
  -v "$PWD/backups:/backup" \
  alpine:3.22 \
  tar -C /data -czf /backup/homedex-data.tar.gz .
docker compose start homedex
```

Encrypt and move the archive according to your normal backup policy. Test restores periodically. Copying only `homedex.db` while Homedex is running in WAL mode can omit committed data still present in the WAL.

Homedex v0.1 does not include a built-in backup scheduler. Markdown, JSON, CSV, and context exports are portable inventory views, not backups: they do not preserve the instance key, admin/session state, connector schedules, share links, notification rules, or every database relationship.

## Restore

Restore into a stopped instance. The following replaces the named volume's contents and is destructive:

```sh
docker compose down
docker volume create homedex_homedex-data
docker run --rm \
  -v homedex_homedex-data:/data \
  -v "$PWD/backups:/backup:ro" \
  alpine:3.22 sh -ec \
  'rm -rf /data/* /data/.[!.]* /data/..?* 2>/dev/null || true; tar -C /data -xzf /backup/homedex-data.tar.gz'
docker compose up -d
curl -fsS http://127.0.0.1:7377/api/health
```

If the backup used `HOMEDEX_SECRET`, restore that exact secret before startup. Homedex validates encrypted notification destinations at startup and fails closed when the restored key is wrong rather than overwriting or disabling rules. In-place `HOMEDEX_SECRET` rotation is not supported in v0.1; retain the current key until a coordinated connector-and-notification re-encryption workflow is available. If file ownership was changed by another backup tool, ensure UID/GID `65532` (the distroless non-root user) can read and write the restored volume.

## Upgrades and rollback

1. Back up `/data` before replacing a binary/image.
2. Read release notes for migrations or behavior changes.
3. Pin a release tag instead of relying on a moving tag.
4. Start the new version and check `/api/health`, connector status, and representative inventory counts.

Migrations apply automatically and are forward-only. Rolling the binary back after a migration is not guaranteed. Restore the pre-upgrade data backup when rolling back.

## Retention and deletion

Observed hosts/services/routes/certificates/domains may be marked `gone` so the change history remains useful. The engine has retention primitives, but v0.1 does not expose a complete operator-facing retention workflow. Deleting the data volume deletes the local inventory and generated instance key:

```sh
docker compose down -v
```

This does not modify any connected Docker host or reverse proxy.

## Demo data

The fake lab has its own `homedex-demo_homedex-demo-data` volume. It contains fabricated data only and is reset by recreating the seed service. It is not a backup source for a real installation.
