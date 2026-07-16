CREATE TABLE service_networks (
  service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  network_name TEXT NOT NULL, ip_address TEXT NOT NULL DEFAULT '', aliases TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY(service_id, network_name)
);
CREATE INDEX idx_service_networks_ip ON service_networks(ip_address);
CREATE UNIQUE INDEX idx_proxies_connector ON proxies(connector_id) WHERE connector_id IS NOT NULL;
