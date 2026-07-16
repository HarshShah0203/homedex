ALTER TABLE share_tokens ADD COLUMN name TEXT NOT NULL DEFAULT '';

ALTER TABLE notification_rules ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_rules ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_rules ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';

CREATE TABLE entity_notes (
  entity_type TEXT NOT NULL,
  entity_id INTEGER NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(entity_type, entity_id)
);

CREATE TABLE manual_expiries (
  id INTEGER PRIMARY KEY,
  natural_key TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'renewal' CHECK(kind IN ('renewal','warranty','subscription','other')),
  authority TEXT NOT NULL DEFAULT '',
  expires_at TEXT,
  source TEXT NOT NULL DEFAULT 'manual',
  state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','gone')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE notification_deliveries (
  id INTEGER PRIMARY KEY,
  rule_id INTEGER NOT NULL REFERENCES notification_rules(id) ON DELETE CASCADE,
  dedupe_key TEXT NOT NULL,
  delivered_at TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  UNIQUE(rule_id, dedupe_key)
);

CREATE INDEX idx_entity_tags_entity ON entity_tags(entity_type, entity_id);
CREATE INDEX idx_custom_fields_entity ON custom_fields(entity_type, entity_id);
CREATE INDEX idx_manual_expiries_date ON manual_expiries(expires_at);
