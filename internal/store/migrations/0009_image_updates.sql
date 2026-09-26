-- repo_digests holds the registry digests Docker recorded for the image a
-- container runs (JSON array of "repo@sha256:..."). It feeds update checks and
-- is never diffed, so the first scan after upgrading files no changes.
ALTER TABLE services ADD COLUMN repo_digests TEXT NOT NULL DEFAULT '[]';

-- One anonymous registry lookup per image reference, exactly as containers
-- name it. Per-container status is derived from this row and the container's
-- repo_digests when the API is read, so a recreated container is up to date
-- as soon as Docker is rescanned. notified_digest is the remote digest the
-- change feed last reported as an available update: one entry per new digest.
-- A row whose reference no container runs any more is retired (retired_at),
-- not deleted, so the reference coming back does not report the same digest
-- again; retired rows are purged with the gone-entity retention. The rows are
-- derived from the source that checked them and go when it is deleted.
CREATE TABLE image_updates (
  id INTEGER PRIMARY KEY,
  connector_id INTEGER NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
  image_ref TEXT NOT NULL UNIQUE,
  lookup TEXT NOT NULL DEFAULT 'unknown' CHECK(lookup IN ('resolved','pinned','unknown')),
  remote_digest TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  checked_at TEXT NOT NULL,
  notified_digest TEXT NOT NULL DEFAULT '',
  retired_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_image_updates_connector ON image_updates(connector_id);
