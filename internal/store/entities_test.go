package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestEntityMetadataSurvivesAndManualEntryLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewEntityManager(st)
	detail, err := m.CreateManual(ctx, ManualEntityInput{
		EntityType: "host", Name: "printer", Address: "10.0.0.50", Notes: "Office shelf",
		Tags:         []TagInput{{Name: "hardware", Color: "#abc123"}},
		CustomFields: []CustomFieldInput{{Key: "serial", Kind: "text", Value: "P-123"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := detail.Entity["id"].(int64)
	if detail.Notes != "Office shelf" || len(detail.Tags) != 1 || len(detail.CustomFields) != 1 {
		t.Fatalf("unexpected manual detail: %#v", detail)
	}
	notes := "Moved to rack"
	if err = m.Patch(ctx, "hosts", id, EntityPatch{Notes: &notes, Tags: []TagInput{{Name: "network"}}, CustomFields: []CustomFieldInput{}}); err != nil {
		t.Fatal(err)
	}
	detail, err = m.Detail(ctx, "host", id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Notes != notes || len(detail.Tags) != 1 || detail.Tags[0].Name != "network" || len(detail.CustomFields) != 0 {
		t.Fatalf("metadata patch did not replace values: %#v", detail)
	}
	if err = m.DeleteManual(ctx, "host", id); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Detail(ctx, "host", id); err != sql.ErrNoRows {
		t.Fatalf("deleted manual host lookup error=%v", err)
	}
}

func TestCreateEverySupportedManualEntityType(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewEntityManager(st)
	host, err := m.CreateManual(ctx, ManualEntityInput{EntityType: "host", Name: "gateway"})
	if err != nil {
		t.Fatal(err)
	}
	hostID := host.Entity["id"].(int64)
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	cases := []ManualEntityInput{
		{EntityType: "service", Name: "router-ui", HostID: &hostID, State: "running"},
		{EntityType: "route", Name: "router.example", Domain: "router.example", UpstreamHost: "10.0.0.1", UpstreamPort: 443, TLS: true},
		{EntityType: "cert", Name: "router.example", Subject: "router.example", Endpoint: "router.example:443", ExpiresAt: expires},
		{EntityType: "domain", Name: "router.example", Registrar: "Example", ExpiresAt: expires},
		{EntityType: "expiry", Name: "Router warranty", Kind: "warranty", ExpiresAt: expires},
	}
	for _, input := range cases {
		detail, err := m.CreateManual(ctx, input)
		if err != nil {
			t.Errorf("create %s: %v", input.EntityType, err)
			continue
		}
		if detail.Entity["id"].(int64) <= 0 || detail.EntityType != input.EntityType {
			t.Errorf("create %s returned %#v", input.EntityType, detail)
		}
	}
}

func TestDiscoveredEntityCannotBeDeletedButCanBeEnriched(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connector, _ := st.CreateConnector(ctx, "fixture", "fixture", nil)
	result, err := st.DB().Exec(`INSERT INTO hosts(connector_id,natural_key,name,kind,state,first_seen,last_seen,created_at,updated_at) VALUES(?, 'host:nas','nas','docker','active','now','now','now','now')`, connector)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	m := NewEntityManager(st)
	note := "Do not reboot during backups"
	if err = m.Patch(ctx, "host", id, EntityPatch{Notes: &note}); err != nil {
		t.Fatal(err)
	}
	if err = m.DeleteManual(ctx, "host", id); err == nil {
		t.Fatal("discovered host was deleted")
	}
}
