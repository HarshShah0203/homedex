package engine

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestSchedulerPurgesGoneRecordsWithoutSuccessfulScan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := auth.NewSecretBox(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	configs := store.NewConnectorConfigs(st, box)
	connectorID, err := configs.Create(ctx, "fixture", "Disabled fixture", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`UPDATE connectors SET enabled=0 WHERE id=?`, connectorID); err != nil {
		t.Fatal(err)
	}
	applier := New(st, nil)
	applier.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, _, err = applier.Apply(ctx, connectorID, domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, connectorID, domain.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	applier.now = func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) }
	runner := &Runner{Store: st, Configs: configs, Applier: applier}
	scheduler := NewScheduler(runner)
	scheduler.now = applier.now
	scheduler.tick(ctx)
	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM services WHERE connector_id=? AND natural_key='svc'`, connectorID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("scheduler left %d expired gone records", count)
	}
	var scans int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM scan_runs WHERE connector_id=? AND started_at>?`, connectorID, "2026-01-01T00:00:00Z").Scan(&scans); err != nil {
		t.Fatal(err)
	}
	if scans != 0 {
		t.Fatalf("disabled connector unexpectedly scanned %d times", scans)
	}
}
