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

func TestManualAndPatchMetadataUseIdenticalNormalization(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewEntityManager(st)
	detail, err := m.CreateManual(ctx, ManualEntityInput{
		EntityType: "host",
		Name:       "normalized",
		Tags: []TagInput{
			{Name: "  network  ", Color: "first"},
			{Name: "network", Color: "duplicate"},
			{Name: "   ", Color: "ignored"},
		},
		CustomFields: []CustomFieldInput{{Key: "  serial  ", Kind: "text", Value: "one"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := detail.Entity["id"].(int64)
	assertNormalizedMetadata(t, detail, "network", "first", "serial", "one")

	if err = m.Patch(ctx, "host", id, EntityPatch{
		Tags: []TagInput{
			{Name: "  storage  ", Color: "patch-first"},
			{Name: "storage", Color: "patch-duplicate"},
			{Name: ""},
		},
		CustomFields: []CustomFieldInput{{Key: "  location  ", Kind: "text", Value: "rack"}},
	}); err != nil {
		t.Fatal(err)
	}
	detail, err = m.Detail(ctx, "host", id)
	if err != nil {
		t.Fatal(err)
	}
	assertNormalizedMetadata(t, detail, "storage", "patch-first", "location", "rack")
}

func TestManualAndPatchMetadataRejectTheSameInvalidFields(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewEntityManager(st)
	detail, err := m.CreateManual(ctx, ManualEntityInput{
		EntityType:   "host",
		Name:         "existing",
		Tags:         []TagInput{{Name: "original"}},
		CustomFields: []CustomFieldInput{{Key: "original", Kind: "text", Value: "kept"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := detail.Entity["id"].(int64)

	tests := []struct {
		name   string
		fields []CustomFieldInput
		want   string
	}{
		{name: "blank key", fields: []CustomFieldInput{{Key: "   ", Kind: "text"}}, want: "custom field key is required"},
		{name: "normalized duplicate", fields: []CustomFieldInput{{Key: "serial", Kind: "text"}, {Key: " serial ", Kind: "text"}}, want: `duplicate custom field "serial"`},
		{name: "unsupported kind", fields: []CustomFieldInput{{Key: "role", Kind: "choice"}}, want: `unsupported custom field kind "choice"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, createErr := m.CreateManual(ctx, ManualEntityInput{EntityType: "host", Name: "invalid-" + tt.name, CustomFields: tt.fields})
			if createErr == nil || createErr.Error() != tt.want {
				t.Fatalf("manual create error=%v, want %q", createErr, tt.want)
			}
			patchErr := m.Patch(ctx, "host", id, EntityPatch{Tags: []TagInput{{Name: "replacement"}}, CustomFields: tt.fields})
			if patchErr == nil || patchErr.Error() != tt.want {
				t.Fatalf("patch error=%v, want %q", patchErr, tt.want)
			}
			after, detailErr := m.Detail(ctx, "host", id)
			if detailErr != nil {
				t.Fatal(detailErr)
			}
			assertNormalizedMetadata(t, after, "original", "", "original", "kept")
			var invalidCount int
			if queryErr := st.DB().QueryRow(`SELECT COUNT(*) FROM hosts WHERE name=?`, "invalid-"+tt.name).Scan(&invalidCount); queryErr != nil {
				t.Fatal(queryErr)
			}
			if invalidCount != 0 {
				t.Fatal("invalid manual entity was not rolled back")
			}
		})
	}
}

func assertNormalizedMetadata(t *testing.T, detail EntityDetail, tagName, tagColor, fieldKey, fieldValue string) {
	t.Helper()
	if len(detail.Tags) != 1 || detail.Tags[0].Name != tagName || detail.Tags[0].Color != tagColor {
		t.Fatalf("tags=%#v, want one normalized %q tag with color %q", detail.Tags, tagName, tagColor)
	}
	if len(detail.CustomFields) != 1 || detail.CustomFields[0].Key != fieldKey || detail.CustomFields[0].Value != fieldValue {
		t.Fatalf("custom fields=%#v, want one normalized %q field with value %q", detail.CustomFields, fieldKey, fieldValue)
	}
}
