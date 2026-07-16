CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);

CREATE TABLE connectors (
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL,
  config_encrypted BLOB NOT NULL DEFAULT X'', enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  schedule_minutes INTEGER NOT NULL DEFAULT 15 CHECK(schedule_minutes > 0),
  last_status TEXT NOT NULL DEFAULT 'never', last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE hosts (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('docker','proxmox-node','vm','lxc','manual')),
  address TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '',
  parent_host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, notes TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE services (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, name TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'container',
  stack TEXT NOT NULL DEFAULT '', image TEXT NOT NULL DEFAULT '', tag TEXT NOT NULL DEFAULT '', digest TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'unknown', first_seen TEXT NOT NULL, last_seen TEXT NOT NULL,
  restart_policy TEXT NOT NULL DEFAULT '', raw_labels TEXT NOT NULL DEFAULT '{}', notes TEXT NOT NULL DEFAULT '',
  natural_key TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE ports (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  host_id INTEGER REFERENCES hosts(id) ON DELETE CASCADE, number INTEGER NOT NULL CHECK(number BETWEEN 1 AND 65535),
  protocol TEXT NOT NULL DEFAULT 'tcp', published INTEGER NOT NULL DEFAULT 0 CHECK(published IN (0,1)),
  host_ip TEXT NOT NULL DEFAULT '', container_port INTEGER NOT NULL CHECK(container_port BETWEEN 1 AND 65535),
  source TEXT NOT NULL DEFAULT '', natural_key TEXT NOT NULL UNIQUE
);
CREATE TABLE proxies (
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('traefik','caddy','npm')),
  host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, endpoint TEXT NOT NULL,
  connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL, last_scan TEXT
);
CREATE TABLE certs (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL UNIQUE, subject TEXT NOT NULL, sans TEXT NOT NULL DEFAULT '[]', issuer TEXT NOT NULL DEFAULT '',
  not_after TEXT, chain_valid INTEGER NOT NULL DEFAULT 0 CHECK(chain_valid IN (0,1)), source TEXT NOT NULL DEFAULT '',
  endpoint TEXT NOT NULL UNIQUE, state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE routes (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  proxy_id INTEGER REFERENCES proxies(id) ON DELETE CASCADE, domain TEXT NOT NULL, path_prefix TEXT NOT NULL DEFAULT '',
  upstream_host TEXT NOT NULL DEFAULT '', upstream_port INTEGER CHECK(upstream_port BETWEEN 1 AND 65535),
  resolved_service_id INTEGER REFERENCES services(id) ON DELETE SET NULL,
  resolve_confidence TEXT NOT NULL DEFAULT 'none' CHECK(resolve_confidence IN ('high','medium','none')),
  tls INTEGER NOT NULL DEFAULT 0 CHECK(tls IN (0,1)), cert_id INTEGER REFERENCES certs(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'unknown' CHECK(status IN ('ok','broken','unknown')),
  natural_key TEXT NOT NULL UNIQUE, state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE domains (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL UNIQUE, name TEXT NOT NULL UNIQUE, registrar TEXT NOT NULL DEFAULT '', expires_at TEXT,
  nameservers TEXT NOT NULL DEFAULT '[]', source TEXT NOT NULL DEFAULT '', last_checked TEXT,
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE custom_fields (
  id INTEGER PRIMARY KEY, entity_type TEXT NOT NULL, entity_id INTEGER NOT NULL, key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('text','date','url','number')), value TEXT NOT NULL,
  UNIQUE(entity_type, entity_id, key)
);
CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, color TEXT NOT NULL DEFAULT '');
CREATE TABLE entity_tags (
  tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE, entity_type TEXT NOT NULL, entity_id INTEGER NOT NULL,
  PRIMARY KEY(tag_id, entity_type, entity_id)
);
CREATE TABLE scan_runs (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  started_at TEXT NOT NULL, finished_at TEXT, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', stats TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE changes (
  id INTEGER PRIMARY KEY, scan_run_id INTEGER NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
  entity_type TEXT NOT NULL, entity_id INTEGER NOT NULL,
  change_kind TEXT NOT NULL CHECK(change_kind IN ('added','removed','modified')),
  summary TEXT NOT NULL, diff TEXT NOT NULL DEFAULT '{}', seen INTEGER NOT NULL DEFAULT 0 CHECK(seen IN (0,1)),
  note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE notification_rules (
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('expiry','change')), threshold_days INTEGER,
  filters TEXT NOT NULL DEFAULT '{}', channels TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN(0,1))
);
CREATE TABLE share_tokens (
  id INTEGER PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, expires_at TEXT,
  revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN(0,1))
);
CREATE TABLE sessions (
  id INTEGER PRIMARY KEY, user_ref TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, csrf_hash TEXT NOT NULL,
  created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE VIRTUAL TABLE search_index USING fts5(entity_type UNINDEXED, entity_id UNINDEXED, title, body);
CREATE TRIGGER services_search_insert AFTER INSERT ON services BEGIN
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('service',new.id,new.name,new.stack||' '||new.image||' '||new.notes);
END;
CREATE TRIGGER services_search_update AFTER UPDATE ON services BEGIN
  DELETE FROM search_index WHERE entity_type='service' AND entity_id=old.id;
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('service',new.id,new.name,new.stack||' '||new.image||' '||new.notes);
END;
CREATE TRIGGER services_search_delete AFTER DELETE ON services BEGIN
  DELETE FROM search_index WHERE entity_type='service' AND entity_id=old.id;
END;
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
CREATE TRIGGER routes_search_insert AFTER INSERT ON routes BEGIN
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('route',new.id,new.domain,new.path_prefix||' '||new.upstream_host);
END;
CREATE TRIGGER routes_search_update AFTER UPDATE ON routes BEGIN
  DELETE FROM search_index WHERE entity_type='route' AND entity_id=old.id;
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('route',new.id,new.domain,new.path_prefix||' '||new.upstream_host);
END;
CREATE TRIGGER routes_search_delete AFTER DELETE ON routes BEGIN
  DELETE FROM search_index WHERE entity_type='route' AND entity_id=old.id;
END;
CREATE TRIGGER tags_search_insert AFTER INSERT ON tags BEGIN
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('tag',new.id,new.name,new.color);
END;
CREATE TRIGGER tags_search_update AFTER UPDATE ON tags BEGIN
  DELETE FROM search_index WHERE entity_type='tag' AND entity_id=old.id;
  INSERT INTO search_index(entity_type,entity_id,title,body) VALUES('tag',new.id,new.name,new.color);
END;
CREATE TRIGGER tags_search_delete AFTER DELETE ON tags BEGIN
  DELETE FROM search_index WHERE entity_type='tag' AND entity_id=old.id;
END;

CREATE INDEX idx_services_host ON services(host_id);
CREATE INDEX idx_services_connector ON services(connector_id);
CREATE INDEX idx_ports_number ON ports(number, protocol);
CREATE INDEX idx_routes_domain ON routes(domain);
CREATE INDEX idx_changes_created ON changes(created_at DESC);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);
