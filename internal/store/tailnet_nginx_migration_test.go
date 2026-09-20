package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestHostAndProxyKindMigrationKeepsIDsAndSearch upgrades a populated v0007
// database: hosts and proxies are rebuilt to widen their kind CHECKs, so every
// row referencing them by id must still line up and host search must still
// follow inserts, updates and deletes.
func TestHostAndProxyKindMigrationKeepsIDsAndSearch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v7.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrationsThrough(ctx, db, 7); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	for _, statement := range []string{
		`INSERT INTO connectors(id,kind,name) VALUES(1,'caddy','Caddy'),(2,'docker','Docker'),(3,'ssh','SSH')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,notes,state,first_seen,last_seen,created_at,updated_at) VALUES(101,2,'docker:nas','nas','docker','10.0.0.2','rack two','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,parent_host_id,state,first_seen,last_seen,created_at,updated_at) VALUES(102,3,'ssh:vm','vm','ssh','10.0.0.3',101,'active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO services(id,connector_id,host_id,name,kind,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES(201,2,101,'app','container','running','` + now + `','` + now + `','svc','` + now + `','` + now + `')`,
		`INSERT INTO ports(id,connector_id,service_id,host_id,number,protocol,published,container_port,natural_key) VALUES(301,2,201,101,8080,'tcp',1,80,'port')`,
		`INSERT INTO proxies(id,kind,host_id,endpoint,connector_id) VALUES(42,'caddy',101,'http://nas:2019',1)`,
		`INSERT INTO routes(id,connector_id,proxy_id,domain,upstream_host,upstream_port,resolved_service_id,natural_key,state,first_seen,last_seen,created_at,updated_at) VALUES(501,1,42,'app.example','nas',8080,201,'route','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES('host',101,'user metadata','` + now + `')`,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			db.Close()
			t.Fatalf("v0007 fixture: %v", err)
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

	var aliases string
	var reported sql.NullString
	var parent, serviceHost, portHost, proxyHost, routeProxy int64
	if err = q.QueryRow(`SELECT h.aliases,h.reported_last_seen,v.parent_host_id,s.host_id,p.host_id,x.host_id,r.proxy_id FROM hosts h JOIN hosts v ON v.id=102 JOIN services s ON s.id=201 JOIN ports p ON p.id=301 JOIN proxies x ON x.id=42 JOIN routes r ON r.id=501 WHERE h.id=101`).
		Scan(&aliases, &reported, &parent, &serviceHost, &portHost, &proxyHost, &routeProxy); err != nil {
		t.Fatal(err)
	}
	if aliases != "[]" || reported.Valid || parent != 101 || serviceHost != 101 || portHost != 101 || proxyHost != 101 || routeProxy != 42 {
		t.Fatalf("aliases=%q reported=%v parent=%d service=%d port=%d proxy=%d route=%d", aliases, reported, parent, serviceHost, portHost, proxyHost, routeProxy)
	}
	var note string
	if err = q.QueryRow(`SELECT notes FROM entity_notes WHERE entity_type='host' AND entity_id=101`).Scan(&note); err != nil || note != "user metadata" {
		t.Fatalf("host metadata = %q, %v", note, err)
	}

	insertHost := func(id int, kind, aliases string) error {
		_, err := q.Exec(`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,aliases,state,first_seen,last_seen,created_at,updated_at) VALUES(?,NULL,?,?,?,'100.64.0.5',?,'active',?,?,?,?)`, id, kind+"-key", "NAS", kind, aliases, now, now, now, now)
		return err
	}
	if err = insertHost(103, "tailscale", `["fd7a:115c:a1e0::5","nas.tail1234.ts.net","nas"]`); err != nil {
		t.Fatalf("tailscale host kind rejected: %v", err)
	}
	if err = insertHost(104, "bogus", `[]`); err == nil {
		t.Fatal("unknown host kind accepted")
	}
	if _, err = q.Exec(`INSERT INTO proxies(kind,endpoint,connector_id) VALUES('nginx','file://nas/etc/nginx',3)`); err != nil {
		t.Fatalf("nginx proxy kind rejected: %v", err)
	}
	if _, err = q.Exec(`INSERT INTO proxies(kind,endpoint) VALUES('bogus','http://x')`); err == nil {
		t.Fatal("unknown proxy kind accepted")
	}
	if _, err = q.Exec(`INSERT INTO proxies(kind,endpoint,connector_id) VALUES('caddy','http://other:2019',1)`); err == nil {
		t.Fatal("second proxies row for one connector accepted; idx_proxies_connector was not recreated")
	}

	search := func(term string) []int64 {
		t.Helper()
		rows, err := q.Query(`SELECT entity_id FROM search_index WHERE entity_type='host' AND search_index MATCH ? ORDER BY entity_id`, `"`+term+`"*`)
		if err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	only := func(term string, want int64) {
		t.Helper()
		if got := search(term); len(got) != 1 || got[0] != want {
			t.Fatalf("search %q = %v, want [%d]", term, got, want)
		}
	}
	only("rack two", 101) // indexed before the rebuild, still present
	only("nas.tail1234.ts.net", 103)
	only("fd7a:115c:a1e0::5", 103)

	if _, err = q.Exec(`UPDATE hosts SET aliases='["nas.lan"]' WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	only("nas.lan", 101)
	if _, err = q.Exec(`UPDATE hosts SET aliases='not json' WHERE id=101`); err != nil {
		t.Fatalf("malformed aliases aborted the update: %v", err)
	}
	if got := search("nas.lan"); len(got) != 0 {
		t.Fatalf("stale alias still indexed: %v", got)
	}
	only("rack two", 101)
	if _, err = q.Exec(`DELETE FROM hosts WHERE id=103`); err != nil {
		t.Fatal(err)
	}
	if got := search("nas.tail1234.ts.net"); len(got) != 0 {
		t.Fatalf("deleted host still indexed: %v", got)
	}

	rows, err := q.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	violations := 0
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("migration left %d foreign key violations", violations)
	}
}
