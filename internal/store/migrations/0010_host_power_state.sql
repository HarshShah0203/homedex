-- power_state is a host's own run state as its source reports it (a Proxmox
-- guest's "running" or "stopped", a node's "online"), kept apart from state,
-- which is the inventory lifecycle (active or gone): a stopped VM is still in
-- the inventory. It is diffed, so a guest that stops or starts files a change.
-- Every existing host has none, so upgrading files nothing. SQLite adds a NOT
-- NULL column with a constant default in place, keeping ids, every other
-- column and the search triggers, which do not read it.
ALTER TABLE hosts ADD COLUMN power_state TEXT NOT NULL DEFAULT '';
