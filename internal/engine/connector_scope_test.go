package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestApplyIsolatesMatchingNaturalKeysAcrossDockerConnectors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	firstConnector, err := st.CreateConnector(ctx, "docker", "Primary Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	secondConnector, err := st.CreateConnector(ctx, "docker", "Secondary Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.Snapshot{
		Hosts:    []domain.Host{{Key: "host", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{{Key: "container", HostKey: "host", Name: "app", State: "running"}},
		Ports:    []domain.Port{{ServiceKey: "container", HostKey: "host", Number: 8080, ContainerPort: 80, Protocol: "tcp", Published: true}},
	}
	applier := New(st, nil)
	if _, _, err = applier.Apply(ctx, firstConnector, snapshot); err != nil {
		t.Fatalf("first connector scan: %v", err)
	}
	if _, _, err = applier.Apply(ctx, secondConnector, snapshot); err != nil {
		t.Fatalf("second connector scan with matching keys: %v", err)
	}
	for table, want := range map[string]int{"hosts": 2, "services": 2, "ports": 2} {
		var count int
		if err = st.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count=%d, want %d", table, count, want)
		}
	}
	var firstServiceID, secondServiceID int64
	if err = st.DB().QueryRow(`SELECT id FROM services WHERE connector_id=? AND natural_key='container'`, firstConnector).Scan(&firstServiceID); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT id FROM services WHERE connector_id=? AND natural_key='container'`, secondConnector).Scan(&secondServiceID); err != nil {
		t.Fatal(err)
	}
	if firstServiceID == secondServiceID {
		t.Fatal("connectors shared one service observation")
	}
	if _, changes, err := applier.Apply(ctx, secondConnector, snapshot); err != nil || changes != 0 {
		t.Fatalf("second connector idempotent rescan changes=%d error=%v", changes, err)
	}
	if _, _, err = applier.Apply(ctx, firstConnector, domain.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	var firstState, secondState string
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE connector_id=? AND natural_key='container'`, firstConnector).Scan(&firstState); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE connector_id=? AND natural_key='container'`, secondConnector).Scan(&secondState); err != nil {
		t.Fatal(err)
	}
	if firstState != "gone" || secondState != "running" {
		t.Fatalf("cross-connector state leaked: first=%q second=%q", firstState, secondState)
	}
}
