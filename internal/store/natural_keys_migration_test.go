package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestConnectorScopedNaturalKeyMigrationPreservesIDsAndMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrationsThrough(ctx, db, 5); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	statements := []string{
		`INSERT INTO connectors(id,kind,name) VALUES(1,'docker','Primary')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,state,first_seen,last_seen,created_at,updated_at) VALUES(101,1,'host','nas','docker','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO services(id,connector_id,host_id,name,kind,state,first_seen,last_seen,natural_key,created_at,updated_at,notes) VALUES(201,1,101,'app','container','running','` + now + `','` + now + `','svc','` + now + `','` + now + `','service note')`,
		`INSERT INTO ports(id,connector_id,service_id,host_id,number,protocol,published,container_port,natural_key) VALUES(301,1,201,101,8080,'tcp',1,80,'port')`,
		`INSERT INTO certs(id,connector_id,natural_key,subject,endpoint,state,first_seen,last_seen,created_at,updated_at) VALUES(401,1,'cert','app.example','app.example:443','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO routes(id,connector_id,domain,resolved_service_id,cert_id,natural_key,state,first_seen,last_seen,created_at,updated_at) VALUES(501,1,'app.example',201,401,'route','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO domains(id,connector_id,natural_key,name,state,first_seen,last_seen,created_at,updated_at) VALUES(601,1,'domain','app.example','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES('service',201,'user metadata','` + now + `')`,
		`INSERT INTO custom_fields(entity_type,entity_id,key,kind,value) VALUES('service',201,'rack','text','A1')`,
		`INSERT INTO tags(id,name) VALUES(701,'important')`,
		`INSERT INTO entity_tags(tag_id,entity_type,entity_id) VALUES(701,'service',201)`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatalf("legacy fixture: %v", err)
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
	var serviceID, hostID, portID, routeID, certID, domainID int64
	if err = st.DB().QueryRow(`SELECT s.id,s.host_id,p.id,r.id,c.id,d.id FROM services s JOIN ports p ON p.service_id=s.id JOIN routes r ON r.resolved_service_id=s.id JOIN certs c ON c.id=r.cert_id JOIN domains d ON d.name=r.domain WHERE s.natural_key='svc'`).Scan(&serviceID, &hostID, &portID, &routeID, &certID, &domainID); err != nil {
		t.Fatal(err)
	}
	if serviceID != 201 || hostID != 101 || portID != 301 || routeID != 501 || certID != 401 || domainID != 601 {
		t.Fatalf("IDs changed: service=%d host=%d port=%d route=%d cert=%d domain=%d", serviceID, hostID, portID, routeID, certID, domainID)
	}
	var note, field, tag string
	if err = st.DB().QueryRow(`SELECT n.notes,cf.value,t.name FROM entity_notes n JOIN custom_fields cf ON cf.entity_type=n.entity_type AND cf.entity_id=n.entity_id JOIN entity_tags et ON et.entity_type=n.entity_type AND et.entity_id=n.entity_id JOIN tags t ON t.id=et.tag_id WHERE n.entity_type='service' AND n.entity_id=201`).Scan(&note, &field, &tag); err != nil {
		t.Fatal(err)
	}
	if note != "user metadata" || field != "A1" || tag != "important" {
		t.Fatalf("metadata changed: note=%q field=%q tag=%q", note, field, tag)
	}
	var indexed int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM search_index WHERE entity_type='service' AND entity_id=201 AND title='app'`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Fatalf("search metadata was not preserved: count=%d", indexed)
	}
	if _, err = st.DB().Exec(`INSERT INTO connectors(id,kind,name) VALUES(2,'docker','Secondary')`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`INSERT INTO hosts(connector_id,natural_key,name,kind,state,first_seen,last_seen,created_at,updated_at) VALUES(2,'host','nas-2','docker','active',?,?,?,?)`, now, now, now, now); err != nil {
		t.Fatalf("same host key in another connector: %v", err)
	}
	if _, err = st.DB().Exec(`INSERT INTO hosts(connector_id,natural_key,name,kind,state,first_seen,last_seen,created_at,updated_at) VALUES(1,'host','duplicate','docker','active',?,?,?,?)`, now, now, now, now); err == nil {
		t.Fatal("same connector accepted a duplicate natural key")
	}
	var violations int
	rows, err := st.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("migration left %d foreign key violations", violations)
	}
}

func applyMigrationsThrough(ctx context.Context, db *sql.DB, maximum int) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || version > maximum {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err = db.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply fixture migration %s: %w", entry.Name(), err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,'now')`, version); err != nil {
			return err
		}
	}
	return nil
}
