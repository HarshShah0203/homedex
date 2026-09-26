package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestPowerStateMigrationOverAPopulatedDatabase upgrades a v0009 database:
// existing hosts gain an empty power state (so nothing is diffed on the next
// scan) and keep their ids, aliases, parents and search entries.
func TestPowerStateMigrationOverAPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v9.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrationsThrough(ctx, db, 9); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	for _, statement := range []string{
		`INSERT INTO connectors(id,kind,name) VALUES(1,'ssh','SSH')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,aliases,state,first_seen,last_seen,created_at,updated_at) VALUES(10,1,'ssh:pve1','pve1','ssh','192.0.2.2','["pve1.lab.example"]','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,parent_host_id,state,first_seen,last_seen,created_at,updated_at) VALUES(11,1,'ssh:web','web','ssh','192.0.2.20',10,'active','` + now + `','` + now + `','` + now + `','` + now + `')`,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatalf("v0009 fixture: %v", err)
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
	var power, aliases string
	var parent sql.NullInt64
	if err = q.QueryRow(`SELECT power_state,aliases FROM hosts WHERE id=10`).Scan(&power, &aliases); err != nil || power != "" || aliases != `["pve1.lab.example"]` {
		t.Fatalf("host after migration: power=%q aliases=%q err=%v", power, aliases, err)
	}
	if err = q.QueryRow(`SELECT parent_host_id FROM hosts WHERE id=11`).Scan(&parent); err != nil || parent.Int64 != 10 {
		t.Fatalf("parent after migration: %v, %v", parent, err)
	}
	if _, err = q.Exec(`UPDATE hosts SET power_state='running' WHERE id=11`); err != nil {
		t.Fatal(err)
	}
	var hits int
	if err = q.QueryRow(`SELECT COUNT(*) FROM search_index WHERE entity_type='host' AND search_index MATCH '"pve1.lab.example"'`).Scan(&hits); err != nil || hits != 1 {
		t.Fatalf("alias search after migration: %d, %v", hits, err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	again.Close()
}

// Migrations are tracked by their number alone, so two files sharing one
// (branches that each added "the next" migration) would silently skip the
// second. Fail loudly instead.
func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			t.Fatalf("migration %s has no numeric version", entry.Name())
		}
		if other, dup := seen[version]; dup {
			t.Fatalf("migrations %s and %s share version %d; renumber the newer one", other, entry.Name(), version)
		}
		seen[version] = entry.Name()
	}
}
