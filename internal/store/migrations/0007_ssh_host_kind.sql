-- The SSH collector records hosts with kind 'ssh'; the hosts kind CHECK
-- predates it. SQLite cannot alter a CHECK, so rebuild the table in place,
-- preserving ids (services/ports/routes reference hosts by id).
DROP TRIGGER IF EXISTS hosts_search_insert;
DROP TRIGGER IF EXISTS hosts_search_update;
DROP TRIGGER IF EXISTS hosts_search_delete;

CREATE TABLE hosts_v7 (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL, name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('docker','proxmox-node','vm','lxc','manual','ssh')),
  address TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '',
  parent_host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, notes TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(connector_id, natural_key)
);
INSERT INTO hosts_v7 (id, connector_id, natural_key, name, kind, address, os, arch, parent_host_id, notes, state, first_seen, last_seen, created_at, updated_at)
  SELECT id, connector_id, natural_key, name, kind, address, os, arch, parent_host_id, notes, state, first_seen, last_seen, created_at, updated_at FROM hosts;
DROP TABLE hosts;
ALTER TABLE hosts_v7 RENAME TO hosts;

CREATE TRIGGER hosts_search_insert AFTER INSERT ON hosts BEGIN
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('host',new.id,new.name,new.address||' '||new.notes);
END;
CREATE TRIGGER hosts_search_update AFTER UPDATE ON hosts BEGIN
  DELETE FROM search_index WHERE entity_type='host' AND entity_id=old.id;
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('host',new.id,new.name,new.address||' '||new.notes);
END;
CREATE TRIGGER hosts_search_delete AFTER DELETE ON hosts BEGIN
  DELETE FROM search_index WHERE entity_type='host' AND entity_id=old.id;
END;
