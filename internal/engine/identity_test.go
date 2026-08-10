package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

func identitySnapshot(serviceKey string) domain.Snapshot {
	return domain.Snapshot{
		Hosts:    []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{{Key: serviceKey, HostKey: "docker:nas", Name: "immich", Stack: "media", State: "running"}},
		Ports: []domain.Port{
			{ServiceKey: serviceKey, HostKey: "docker:nas", Number: 2283, ContainerPort: 3001, Protocol: "tcp", Published: true},
			{ServiceKey: serviceKey, HostKey: "docker:nas", Number: 9000, ContainerPort: 9000, Protocol: "tcp"},
		},
	}
}

// A container that is down when a scan runs gets marked gone. When it comes back
// the stable key finds that row again, so the id -- and every note and tag keyed
// to it -- survives the outage. Keying on the container ID produced a second row
// here and stranded the first one.
func TestApplyRevivesGoneServiceOnStableKey(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connectorID, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	applier := New(st, nil)

	key := "docker:nas:media-immich-1"
	if _, _, err = applier.Apply(ctx, connectorID, identitySnapshot(key)); err != nil {
		t.Fatal(err)
	}
	var originalID int64
	var firstSeen string
	if err = st.DB().QueryRow(`SELECT id,first_seen FROM services WHERE connector_id=?`, connectorID).Scan(&originalID, &firstSeen); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`UPDATE services SET notes='holds the family photos' WHERE id=?`, originalID); err != nil {
		t.Fatal(err)
	}

	// scanned while the container is stopped
	if _, _, err = applier.Apply(ctx, connectorID, domain.Snapshot{Hosts: identitySnapshot(key).Hosts}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE id=?`, originalID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "gone" {
		t.Fatalf("service state while stopped = %q, want gone", state)
	}

	// and back up again, with a new container ID behind the same name
	if _, _, err = applier.Apply(ctx, connectorID, identitySnapshot(key)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM services WHERE connector_id=?`, connectorID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("services rows = %d, want the original row reused", count)
	}
	var revivedState, notes, revivedFirstSeen string
	if err = st.DB().QueryRow(`SELECT state,notes,first_seen FROM services WHERE id=?`, originalID).Scan(&revivedState, &notes, &revivedFirstSeen); err != nil {
		t.Fatalf("original row gone: %v", err)
	}
	if revivedState != "running" {
		t.Fatalf("revived state = %q", revivedState)
	}
	if notes != "holds the family photos" {
		t.Fatalf("notes lost across the outage: %q", notes)
	}
	if revivedFirstSeen != firstSeen {
		t.Fatalf("first_seen reset: %q -> %q", firstSeen, revivedFirstSeen)
	}
}

// Upgrading to the container-name scheme moves every existing service key once.
// The service row is adopted by the existing recreate path; this covers the ports
// hanging off it, which would otherwise be deleted and reinserted under new ids.
func TestApplyAdoptsPortRowsWhenServiceKeyChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connectorID, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	applier := New(st, nil)

	// stored under the old ID-based key
	if _, _, err = applier.Apply(ctx, connectorID, identitySnapshot("container:aaa111")); err != nil {
		t.Fatal(err)
	}
	before := map[int]int64{}
	rows, err := st.DB().Query(`SELECT number,id FROM ports WHERE connector_id=?`, connectorID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var number int
		var id int64
		if err = rows.Scan(&number, &id); err != nil {
			t.Fatal(err)
		}
		before[number] = id
	}
	rows.Close()
	if len(before) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(before))
	}

	var changesBefore int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM changes`).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}

	// the next scan after upgrading reports the same containers under new keys
	if _, _, err = applier.Apply(ctx, connectorID, identitySnapshot("docker:nas:media-immich-1")); err != nil {
		t.Fatal(err)
	}

	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM ports WHERE connector_id=?`, connectorID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("ports rows = %d, want 2 carried across the re-key", count)
	}
	for number, want := range before {
		var got int64
		var key string
		if err = st.DB().QueryRow(`SELECT id,natural_key FROM ports WHERE connector_id=? AND number=?`, connectorID, number).Scan(&got, &key); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("port %d changed id %d -> %d, orphaning its metadata", number, want, got)
		}
		if key[:len("docker:nas:media-immich-1")] != "docker:nas:media-immich-1" {
			t.Fatalf("port %d kept the stale key %q", number, key)
		}
	}

	// A re-key is bookkeeping, not something that happened in the homelab, so it
	// must not report every port on the host as added or removed.
	var portChanges int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM changes WHERE entity_type='port' AND id > ?`, changesBefore).Scan(&portChanges); err != nil {
		t.Fatal(err)
	}
	if portChanges != 0 {
		t.Fatalf("re-key produced %d port change entries, want 0", portChanges)
	}
}

// The whole upgrade, as an existing install experiences it: one scan on the old
// ID-based keys, then one on the new name-based keys with nothing in the homelab
// having actually changed. Nothing moved, so the ledger must stay silent.
func TestUpgradeToStableKeysReportsNoChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connectorID, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	applier := New(st, nil)

	if _, _, err = applier.Apply(ctx, connectorID, identitySnapshot("container:aaa111")); err != nil {
		t.Fatal(err)
	}
	var serviceID int64
	if err = st.DB().QueryRow(`SELECT id FROM services WHERE connector_id=?`, connectorID).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}

	_, changes, err := applier.Apply(ctx, connectorID, identitySnapshot("docker:nas:media-immich-1"))
	if err != nil {
		t.Fatal(err)
	}
	if changes != 0 {
		t.Fatalf("upgrade scan reported %d changes, want 0 -- nothing in the homelab moved", changes)
	}

	var stillOriginal int64
	var key string
	if err = st.DB().QueryRow(`SELECT id,natural_key FROM services WHERE connector_id=?`, connectorID).Scan(&stillOriginal, &key); err != nil {
		t.Fatal(err)
	}
	if stillOriginal != serviceID {
		t.Fatalf("service row replaced: %d -> %d", serviceID, stillOriginal)
	}
	if key != "docker:nas:media-immich-1" {
		t.Fatalf("service kept the stale key %q", key)
	}

	// and the scan after that is a plain no-op
	if _, changes, err = applier.Apply(ctx, connectorID, identitySnapshot("docker:nas:media-immich-1")); err != nil || changes != 0 {
		t.Fatalf("steady-state rescan changes=%d err=%v", changes, err)
	}
}

// Silencing the re-key must not blind the ledger: an actual redeploy still moves
// the image or tag under the now-stable key, and that has to be reported.
func TestStableKeyStillReportsRealRedeploy(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connectorID, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	applier := New(st, nil)

	key := "docker:nas:media-immich-1"
	base := identitySnapshot(key)
	base.Services[0].Image = "ghcr.io/immich-app/immich-server"
	base.Services[0].Tag = "v1.0"
	if _, _, err = applier.Apply(ctx, connectorID, base); err != nil {
		t.Fatal(err)
	}

	bumped := identitySnapshot(key)
	bumped.Services[0].Image = "ghcr.io/immich-app/immich-server"
	bumped.Services[0].Tag = "v1.1"
	_, changes, err := applier.Apply(ctx, connectorID, bumped)
	if err != nil {
		t.Fatal(err)
	}
	if changes == 0 {
		t.Fatal("an image bump under a stable key went unreported")
	}
	var summary string
	if err = st.DB().QueryRow(`SELECT summary FROM changes WHERE entity_type='service' ORDER BY id DESC LIMIT 1`).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if summary == "" {
		t.Fatal("redeploy recorded no summary")
	}
	t.Logf("redeploy reported as: %s", summary)
}
