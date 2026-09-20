-- Tailscale devices are hosts of kind 'tailscale'. aliases holds the other
-- identifiers a route may use for the same host (second IP, DNS names);
-- reported_last_seen is the source's own last-contact time, kept out of the
-- change diff because it moves on every poll. SQLite cannot alter a CHECK, so
-- rebuild the table in place, preserving ids (services/ports/proxies reference
-- hosts by id). Search triggers index aliases; malformed JSON indexes nothing
-- rather than aborting the write.
DROP TRIGGER IF EXISTS hosts_search_insert;
DROP TRIGGER IF EXISTS hosts_search_update;
DROP TRIGGER IF EXISTS hosts_search_delete;

CREATE TABLE hosts_v8 (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL, name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('docker','proxmox-node','vm','lxc','manual','ssh','tailscale')),
  address TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '',
  parent_host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, notes TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  aliases TEXT NOT NULL DEFAULT '[]', reported_last_seen TEXT,
  UNIQUE(connector_id, natural_key)
);
INSERT INTO hosts_v8 (id, connector_id, natural_key, name, kind, address, os, arch, parent_host_id, notes, state, first_seen, last_seen, created_at, updated_at)
  SELECT id, connector_id, natural_key, name, kind, address, os, arch, parent_host_id, notes, state, first_seen, last_seen, created_at, updated_at FROM hosts;
DROP TABLE hosts;
ALTER TABLE hosts_v8 RENAME TO hosts;

CREATE TRIGGER hosts_search_insert AFTER INSERT ON hosts BEGIN
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('host',new.id,new.name,new.address||' '||COALESCE((SELECT group_concat(value,' ') FROM json_each(CASE WHEN json_valid(new.aliases) THEN new.aliases ELSE '[]' END)),'')||' '||new.notes);
END;
CREATE TRIGGER hosts_search_update AFTER UPDATE ON hosts BEGIN
  DELETE FROM search_index WHERE entity_type='host' AND entity_id=old.id;
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('host',new.id,new.name,new.address||' '||COALESCE((SELECT group_concat(value,' ') FROM json_each(CASE WHEN json_valid(new.aliases) THEN new.aliases ELSE '[]' END)),'')||' '||new.notes);
END;
CREATE TRIGGER hosts_search_delete AFTER DELETE ON hosts BEGIN
  DELETE FROM search_index WHERE entity_type='host' AND entity_id=old.id;
END;

-- proxies.kind CHECK predates the file-based nginx connector. Rebuild it the
-- same way, preserving ids (routes.proxy_id references proxies by id). DROP
-- TABLE removes idx_proxies_connector from 0002, so it is recreated here: it is
-- what stops a connector from owning two proxies rows.
CREATE TABLE proxies_v8 (
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('traefik','caddy','npm','nginx')),
  host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, endpoint TEXT NOT NULL,
  connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL, last_scan TEXT
);
INSERT INTO proxies_v8 (id, kind, host_id, endpoint, connector_id, last_scan)
  SELECT id, kind, host_id, endpoint, connector_id, last_scan FROM proxies;
DROP TABLE proxies;
ALTER TABLE proxies_v8 RENAME TO proxies;
CREATE UNIQUE INDEX idx_proxies_connector ON proxies(connector_id) WHERE connector_id IS NOT NULL;
