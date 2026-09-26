package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestImageUpdatesMigrationOverAPopulatedDatabase upgrades a v0008 database:
// existing services gain an empty repo_digests list (so nothing is diffed on
// the next scan), their ids and search entries survive, and image_updates
// enforces one row per reference and a known lookup outcome.
func TestImageUpdatesMigrationOverAPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v8.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrationsThrough(ctx, db, 8); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	for _, statement := range []string{
		`INSERT INTO connectors(id,kind,name) VALUES(1,'docker','Docker')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,state,first_seen,last_seen,created_at,updated_at) VALUES(10,1,'docker:nas','nas','docker','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO services(id,connector_id,host_id,name,kind,image,tag,digest,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES(20,1,10,'whoami','container','traefik/whoami','v1.10.0','sha256:abc','running','` + now + `','` + now + `','docker:nas:whoami','` + now + `','` + now + `')`,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatalf("v0008 fixture: %v", err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q := st.DB()
	var digests, image string
	if err = q.QueryRow(`SELECT repo_digests,image FROM services WHERE id=20`).Scan(&digests, &image); err != nil || digests != "[]" || image != "traefik/whoami" {
		t.Fatalf("service after migration: digests=%q image=%q err=%v", digests, image, err)
	}
	var hits int
	if err = q.QueryRow(`SELECT COUNT(*) FROM search_index WHERE entity_type='service' AND search_index MATCH '"whoami"'`).Scan(&hits); err != nil || hits != 1 {
		t.Fatalf("service search after migration: %d, %v", hits, err)
	}
	if _, err = q.Exec(`INSERT INTO connectors(id,kind,name) VALUES(2,'registry','Image updates')`); err != nil {
		t.Fatal(err)
	}
	insert := func(ref, lookup string) error {
		_, err := q.Exec(`INSERT INTO image_updates(connector_id,image_ref,lookup,checked_at,created_at,updated_at) VALUES(2,?,?,?,?,?)`, ref, lookup, now, now, now)
		return err
	}
	if err = insert("traefik/whoami:v1.10.0", "resolved"); err != nil {
		t.Fatal(err)
	}
	if err = insert("traefik/whoami:v1.10.0", "unknown"); err == nil {
		t.Fatal("second row for one reference accepted")
	}
	if err = insert("nginx:alpine", "maybe"); err == nil {
		t.Fatal("unknown lookup outcome accepted")
	}
	if _, err = q.Exec(`DELETE FROM connectors WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	var owner sql.NullInt64
	if err = q.QueryRow(`SELECT connector_id FROM image_updates WHERE image_ref='traefik/whoami:v1.10.0'`).Scan(&owner); err != nil || owner.Valid {
		t.Fatalf("deleting the source left owner=%v, %v", owner, err)
	}
	// Reopening an already-migrated database is a no-op.
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	again.Close()
}
