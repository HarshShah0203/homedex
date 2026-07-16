-- The legacy channels column is retained for a one-time application migration:
-- notification manager startup encrypts each JSON value with the instance
-- SecretBox, stores it here, and replaces the legacy value with an empty array.
ALTER TABLE notification_rules ADD COLUMN channels_encrypted BLOB NOT NULL DEFAULT X'';
