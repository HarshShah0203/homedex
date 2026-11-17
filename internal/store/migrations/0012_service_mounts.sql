CREATE TABLE service_mounts (
    id INTEGER PRIMARY KEY,
    service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    type TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    destination TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    rw INTEGER NOT NULL DEFAULT 0 CHECK(rw IN (0,1)),
    propagation TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_service_mounts_service_id
    ON service_mounts(service_id);
