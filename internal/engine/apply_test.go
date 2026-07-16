package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestApplyIsIdempotentAndMarksMissingEntitiesGone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connectorID, err := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snapshot := domain.Snapshot{Hosts: []domain.Host{{Key: "host:nas", Name: "nas", Kind: "docker", Address: "10.0.0.2"}}, Services: []domain.Service{{Key: "container:abc", HostKey: "host:nas", Name: "jellyfin", Stack: "media", Image: "jellyfin/jellyfin", Tag: "latest", State: "running"}}, Ports: []domain.Port{{ServiceKey: "container:abc", HostKey: "host:nas", Number: 8096, ContainerPort: 8096, Protocol: "tcp", Published: true, HostIP: "0.0.0.0", Source: "docker"}}}
	_, first, err := a.Apply(ctx, connectorID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if first != 3 {
		t.Fatalf("first scan changes=%d, want 3", first)
	}
	_, second, err := a.Apply(ctx, connectorID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Fatalf("idempotent scan changes=%d, want 0", second)
	}
	_, third, err := a.Apply(ctx, connectorID, domain.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if third != 3 {
		t.Fatalf("empty scan changes=%d, want 3", third)
	}
	var state string
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE natural_key='container:abc'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "gone" {
		t.Fatalf("service state=%q, want gone", state)
	}
	var lastSeen string
	if err = st.DB().QueryRow(`SELECT last_seen FROM services WHERE natural_key='container:abc'`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if lastSeen == "" {
		t.Fatal("last_seen was not retained")
	}
	var changes int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM changes`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 6 {
		t.Fatalf("change rows=%d, want 6", changes)
	}
}

func TestApplyTracksOnlyMeaningfulServiceChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snap := domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app", Image: "app", Tag: "v1", State: "running", RawLabels: map[string]string{"cosmetic": "one"}}}}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	snap.Services[0].RawLabels["cosmetic"] = "two"
	_, count, err := a.Apply(ctx, id, snap)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("untracked label generated %d changes", count)
	}
	snap.Services[0].Tag = "v2"
	_, count, err = a.Apply(ctx, id, snap)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("tag update generated %d changes, want 1", count)
	}
}

func TestPurgeGoneHonorsRetention(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	a.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, _, err = a.Apply(ctx, id, domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.Apply(ctx, id, domain.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) }
	if err = a.PurgeGone(ctx, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM services WHERE natural_key='svc'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("gone service count=%d, want 0", count)
	}
}

func TestApplyTreatsSameComposeIdentityAsRecreated(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	a := New(st, nil)
	host := domain.Host{Key: "host", Name: "nas", Kind: "docker"}
	old := domain.Snapshot{Hosts: []domain.Host{host}, Services: []domain.Service{{Key: "container:old", HostKey: "host", Name: "app", Stack: "stack", State: "running"}}}
	if _, _, err = a.Apply(ctx, id, old); err != nil {
		t.Fatal(err)
	}
	old.Services[0].Key = "container:new"
	_, changes, err := a.Apply(ctx, id, old)
	if err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("changes=%d, want one recreated change", changes)
	}
	var count int
	var key string
	if err = st.DB().QueryRow(`SELECT COUNT(*),MAX(natural_key) FROM services WHERE name='app'`).Scan(&count, &key); err != nil {
		t.Fatal(err)
	}
	if count != 1 || key != "container:new" {
		t.Fatalf("count=%d key=%q", count, key)
	}
}
func TestApplyDoesNotCollapseComposeReplicas(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	id, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	snap := domain.Snapshot{Services: []domain.Service{{Key: "one", Name: "web", Stack: "app"}, {Key: "two", Name: "web", Stack: "app"}}}
	if _, _, e = New(st, nil).Apply(ctx, id, snap); e != nil {
		t.Fatal(e)
	}
	var count int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM services WHERE name='web'`).Scan(&count)
	if count != 2 {
		t.Fatalf("replica count=%d", count)
	}
}
