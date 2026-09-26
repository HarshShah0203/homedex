-- New host kinds for the Unraid, TrueNAS, DNS (Pi-hole, AdGuard Home) and
-- Kubernetes connectors: 'unraid' and 'truenas' for the NAS itself, 'dns' for
-- a DNS view of one address (the names a local resolver answers with it, linked
-- to the machine at that address rather than counted as a machine), and
-- 'k8s-node' for a cluster node. SQLite cannot alter a CHECK, so rebuild the
-- table in place exactly as 0008 does, preserving ids (services, ports, proxies
-- and parent_host_id reference hosts by id) and, unlike 0008, copying aliases,
-- reported_last_seen and 0010's power_state, which exist by now.
DROP TRIGGER IF EXISTS hosts_search_insert;
DROP TRIGGER IF EXISTS hosts_search_update;
DROP TRIGGER IF EXISTS hosts_search_delete;

CREATE TABLE hosts_v11 (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL, name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('docker','proxmox-node','vm','lxc','manual','ssh','tailscale','unraid','truenas','dns','k8s-node')),
  address TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '',
  parent_host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, notes TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  aliases TEXT NOT NULL DEFAULT '[]', reported_last_seen TEXT,
  power_state TEXT NOT NULL DEFAULT '',
  UNIQUE(connector_id, natural_key)
);
INSERT INTO hosts_v11 (id, connector_id, natural_key, name, kind, address, os, arch, parent_host_id, notes, state, first_seen, last_seen, created_at, updated_at, aliases, reported_last_seen, power_state)
  SELECT id, connector_id, natural_key, name, kind, address, os, arch, parent_host_id, notes, state, first_seen, last_seen, created_at, updated_at, aliases, reported_last_seen, power_state FROM hosts;
DROP TABLE hosts;
ALTER TABLE hosts_v11 RENAME TO hosts;

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
