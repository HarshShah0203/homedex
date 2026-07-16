DROP TRIGGER IF EXISTS services_search_insert;
DROP TRIGGER IF EXISTS services_search_update;
DROP TRIGGER IF EXISTS services_search_delete;
DROP TRIGGER IF EXISTS hosts_search_insert;
DROP TRIGGER IF EXISTS hosts_search_update;
DROP TRIGGER IF EXISTS hosts_search_delete;
DROP TRIGGER IF EXISTS routes_search_insert;
DROP TRIGGER IF EXISTS routes_search_update;
DROP TRIGGER IF EXISTS routes_search_delete;

CREATE TABLE hosts_scoped (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL, name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('docker','proxmox-node','vm','lxc','manual')),
  address TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', arch TEXT NOT NULL DEFAULT '',
  parent_host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, notes TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(connector_id, natural_key)
);
CREATE TABLE services_scoped (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL, name TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'container',
  stack TEXT NOT NULL DEFAULT '', image TEXT NOT NULL DEFAULT '', tag TEXT NOT NULL DEFAULT '', digest TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'unknown', first_seen TEXT NOT NULL, last_seen TEXT NOT NULL,
  restart_policy TEXT NOT NULL DEFAULT '', raw_labels TEXT NOT NULL DEFAULT '{}', notes TEXT NOT NULL DEFAULT '',
  natural_key TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  health TEXT NOT NULL DEFAULT '',
  UNIQUE(connector_id, natural_key)
);
CREATE TABLE ports_scoped (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  host_id INTEGER REFERENCES hosts(id) ON DELETE CASCADE, number INTEGER NOT NULL CHECK(number BETWEEN 1 AND 65535),
  protocol TEXT NOT NULL DEFAULT 'tcp', published INTEGER NOT NULL DEFAULT 0 CHECK(published IN (0,1)),
  host_ip TEXT NOT NULL DEFAULT '', container_port INTEGER NOT NULL CHECK(container_port BETWEEN 1 AND 65535),
  source TEXT NOT NULL DEFAULT '', natural_key TEXT NOT NULL,
  UNIQUE(connector_id, natural_key)
);
CREATE TABLE certs_scoped (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL, subject TEXT NOT NULL, sans TEXT NOT NULL DEFAULT '[]', issuer TEXT NOT NULL DEFAULT '',
  not_after TEXT, chain_valid INTEGER NOT NULL DEFAULT 0 CHECK(chain_valid IN (0,1)), source TEXT NOT NULL DEFAULT '',
  endpoint TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(connector_id, natural_key), UNIQUE(connector_id, endpoint)
);
CREATE TABLE routes_scoped (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  proxy_id INTEGER REFERENCES proxies(id) ON DELETE CASCADE, domain TEXT NOT NULL, path_prefix TEXT NOT NULL DEFAULT '',
  upstream_host TEXT NOT NULL DEFAULT '', upstream_port INTEGER CHECK(upstream_port BETWEEN 1 AND 65535),
  resolved_service_id INTEGER REFERENCES services(id) ON DELETE SET NULL,
  resolve_confidence TEXT NOT NULL DEFAULT 'none' CHECK(resolve_confidence IN ('high','medium','none')),
  tls INTEGER NOT NULL DEFAULT 0 CHECK(tls IN (0,1)), cert_id INTEGER REFERENCES certs(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'unknown' CHECK(status IN ('ok','broken','unknown')),
  natural_key TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(connector_id, natural_key)
);
CREATE TABLE domains_scoped (
  id INTEGER PRIMARY KEY, connector_id INTEGER REFERENCES connectors(id) ON DELETE SET NULL,
  natural_key TEXT NOT NULL, name TEXT NOT NULL, registrar TEXT NOT NULL DEFAULT '', expires_at TEXT,
  nameservers TEXT NOT NULL DEFAULT '[]', source TEXT NOT NULL DEFAULT '', last_checked TEXT,
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(connector_id, natural_key), UNIQUE(connector_id, name)
);

INSERT INTO hosts_scoped SELECT id,connector_id,natural_key,name,kind,address,os,arch,parent_host_id,notes,state,first_seen,last_seen,created_at,updated_at FROM hosts;
INSERT INTO services_scoped SELECT id,connector_id,host_id,name,kind,stack,image,tag,digest,state,first_seen,last_seen,restart_policy,raw_labels,notes,natural_key,created_at,updated_at,health FROM services;
INSERT INTO ports_scoped SELECT id,connector_id,service_id,host_id,number,protocol,published,host_ip,container_port,source,natural_key FROM ports;
INSERT INTO certs_scoped SELECT id,connector_id,natural_key,subject,sans,issuer,not_after,chain_valid,source,endpoint,state,first_seen,last_seen,created_at,updated_at FROM certs;
INSERT INTO routes_scoped SELECT id,connector_id,proxy_id,domain,path_prefix,upstream_host,upstream_port,resolved_service_id,resolve_confidence,tls,cert_id,status,natural_key,state,first_seen,last_seen,created_at,updated_at FROM routes;
INSERT INTO domains_scoped SELECT id,connector_id,natural_key,name,registrar,expires_at,nameservers,source,last_checked,state,first_seen,last_seen,created_at,updated_at FROM domains;

DROP TABLE routes;
DROP TABLE ports;
DROP TABLE services;
DROP TABLE certs;
DROP TABLE domains;
DROP TABLE hosts;

ALTER TABLE hosts_scoped RENAME TO hosts;
ALTER TABLE services_scoped RENAME TO services;
ALTER TABLE ports_scoped RENAME TO ports;
ALTER TABLE certs_scoped RENAME TO certs;
ALTER TABLE routes_scoped RENAME TO routes;
ALTER TABLE domains_scoped RENAME TO domains;

CREATE INDEX idx_services_host ON services(host_id);
CREATE INDEX idx_services_connector ON services(connector_id);
CREATE INDEX idx_ports_number ON ports(number, protocol);
CREATE INDEX idx_routes_domain ON routes(domain);

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
