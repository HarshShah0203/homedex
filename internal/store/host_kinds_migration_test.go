package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestNewHostKindsMigrationKeepsEveryHostColumn upgrades a populated v0009
// database. hosts is rebuilt to widen its kind CHECK, so ids, aliases and
// reported_last_seen must survive, every row referencing a host by id must
// still point at it (and still cascade or clear when it is deleted), and host
// search must keep indexing aliases.
func TestNewHostKindsMigrationKeepsEveryHostColumn(t *testing.T) {
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
	seen := "2025-12-31T23:59:00Z"
	for _, statement := range []string{
		`INSERT INTO connectors(id,kind,name) VALUES(1,'caddy','Caddy'),(2,'docker','Docker'),(3,'ssh','SSH'),(4,'tailscale','Tailnet')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,os,arch,notes,state,aliases,first_seen,last_seen,created_at,updated_at) VALUES(101,2,'docker:nas','nas','docker','10.0.0.2','linux','x86_64','rack two','active','["nas.lan"]','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,parent_host_id,state,first_seen,last_seen,created_at,updated_at) VALUES(102,3,'ssh:vm','vm','ssh','10.0.0.3',101,'gone','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,aliases,reported_last_seen,state,first_seen,last_seen,created_at,updated_at) VALUES(103,4,'tailscale:n1','NAS','tailscale','100.64.0.5','["fd7a:115c:a1e0::5","nas.tail1234.ts.net"]','` + seen + `','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO services(id,connector_id,host_id,name,kind,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES(201,2,101,'app','container','running','` + now + `','` + now + `','svc','` + now + `','` + now + `')`,
		`INSERT INTO ports(id,connector_id,service_id,host_id,number,protocol,published,container_port,natural_key) VALUES(301,2,201,101,8080,'tcp',1,80,'port')`,
		`INSERT INTO proxies(id,kind,host_id,endpoint,connector_id) VALUES(42,'caddy',101,'http://nas:2019',1)`,
		`INSERT INTO routes(id,connector_id,proxy_id,domain,upstream_host,upstream_port,resolved_service_id,natural_key,state,first_seen,last_seen,created_at,updated_at) VALUES(501,1,42,'app.example','nas',8080,201,'route','active','` + now + `','` + now + `','` + now + `','` + now + `')`,
		`INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES('host',101,'user metadata','` + now + `')`,
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

	type hostRow struct {
		connector                             sql.NullInt64
		key, name, kind, address, os, arch    string
		notes, state, aliases                 string
		parent                                sql.NullInt64
		reported                              sql.NullString
		firstSeen, lastSeen, created, updated string
	}
	row := func(id int64) hostRow {
		t.Helper()
		var h hostRow
		if err := q.QueryRow(`SELECT connector_id,natural_key,name,kind,address,os,arch,notes,state,aliases,parent_host_id,reported_last_seen,first_seen,last_seen,created_at,updated_at FROM hosts WHERE id=?`, id).
			Scan(&h.connector, &h.key, &h.name, &h.kind, &h.address, &h.os, &h.arch, &h.notes, &h.state, &h.aliases, &h.parent, &h.reported, &h.firstSeen, &h.lastSeen, &h.created, &h.updated); err != nil {
			t.Fatalf("host %d after migration: %v", id, err)
		}
		return h
	}
	nas := row(101)
	if nas.connector.Int64 != 2 || nas.key != "docker:nas" || nas.name != "nas" || nas.kind != "docker" || nas.address != "10.0.0.2" ||
		nas.os != "linux" || nas.arch != "x86_64" || nas.notes != "rack two" || nas.state != "active" || nas.aliases != `["nas.lan"]` ||
		nas.parent.Valid || nas.reported.Valid || nas.firstSeen != now || nas.updated != now {
		t.Fatalf("docker host after migration = %+v", nas)
	}
	if vm := row(102); vm.parent.Int64 != 101 || vm.state != "gone" || vm.aliases != "[]" {
		t.Fatalf("ssh host after migration = %+v", vm)
	}
	if device := row(103); device.kind != "tailscale" || device.aliases != `["fd7a:115c:a1e0::5","nas.tail1234.ts.net"]` || device.reported.String != seen {
		t.Fatalf("tailnet device after migration = %+v", device)
	}
	var serviceHost, portHost, proxyHost, routeProxy int64
	if err = q.QueryRow(`SELECT s.host_id,p.host_id,x.host_id,r.proxy_id FROM services s JOIN ports p ON p.id=301 JOIN proxies x ON x.id=42 JOIN routes r ON r.id=501 WHERE s.id=201`).
		Scan(&serviceHost, &portHost, &proxyHost, &routeProxy); err != nil {
		t.Fatal(err)
	}
	if serviceHost != 101 || portHost != 101 || proxyHost != 101 || routeProxy != 42 {
		t.Fatalf("service=%d port=%d proxy=%d route=%d", serviceHost, portHost, proxyHost, routeProxy)
	}
	var note string
	if err = q.QueryRow(`SELECT notes FROM entity_notes WHERE entity_type='host' AND entity_id=101`).Scan(&note); err != nil || note != "user metadata" {
		t.Fatalf("host metadata = %q, %v", note, err)
	}

	insertHost := func(id int, kind, address, aliases string) error {
		_, err := q.Exec(`INSERT INTO hosts(id,connector_id,natural_key,name,kind,address,aliases,state,first_seen,last_seen,created_at,updated_at) VALUES(?,NULL,?,?,?,?,?,'active',?,?,?,?)`, id, kind+"-key", kind+"-host", kind, address, aliases, now, now, now, now)
		return err
	}
	for i, kind := range []string{"docker", "proxmox-node", "vm", "lxc", "manual", "ssh", "tailscale", "unraid", "truenas", "k8s-node"} {
		if err = insertHost(110+i, kind, "", "[]"); err != nil {
			t.Fatalf("host kind %q rejected: %v", kind, err)
		}
	}
	if err = insertHost(130, "dns", "192.0.2.10", `["nas.home.arpa","media.home.arpa"]`); err != nil {
		t.Fatalf("dns host kind rejected: %v", err)
	}
	if err = insertHost(131, "bogus", "", "[]"); err == nil {
		t.Fatal("unknown host kind accepted")
	}
	if _, err = q.Exec(`UPDATE hosts SET state='stale' WHERE id=101`); err == nil {
		t.Fatal("unknown host state accepted")
	}
	if _, err = q.Exec(`INSERT INTO hosts(connector_id,natural_key,name,kind,first_seen,last_seen,created_at,updated_at) VALUES(2,'docker:nas','twin','docker',?,?,?,?)`, now, now, now, now); err == nil {
		t.Fatal("second host with one connector's natural key accepted")
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
	// Indexed before the rebuild, still present.
	only("rack two", 101)
	only("nas.lan", 101)
	only("nas.tail1234.ts.net", 103)
	// Indexed by the recreated triggers.
	only("media.home.arpa", 130)
	if _, err = q.Exec(`UPDATE hosts SET aliases='["photos.home.arpa"]' WHERE id=130`); err != nil {
		t.Fatal(err)
	}
	only("photos.home.arpa", 130)
	if got := search("media.home.arpa"); len(got) != 0 {
		t.Fatalf("stale alias still indexed: %v", got)
	}
	if _, err = q.Exec(`UPDATE hosts SET aliases='not json' WHERE id=130`); err != nil {
		t.Fatalf("malformed aliases aborted the update: %v", err)
	}
	only("192.0.2.10", 130)
	if _, err = q.Exec(`DELETE FROM hosts WHERE id=130`); err != nil {
		t.Fatal(err)
	}
	if got := search("192.0.2.10"); len(got) != 0 {
		t.Fatalf("deleted host still indexed: %v", got)
	}

	// Foreign keys point at the rebuilt table: an unknown host id is refused,
	// and deleting a host clears or cascades exactly as the schema says.
	if _, err = q.Exec(`INSERT INTO services(connector_id,host_id,name,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES(2,999,'orphan','running',?,?,'orphan',?,?)`, now, now, now, now); err == nil {
		t.Fatal("service referencing a missing host accepted")
	}
	if _, err = q.Exec(`DELETE FROM hosts WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	var parent, svcHost, proxyHostAfter sql.NullInt64
	if err = q.QueryRow(`SELECT v.parent_host_id,s.host_id,x.host_id FROM hosts v JOIN services s ON s.id=201 JOIN proxies x ON x.id=42 WHERE v.id=102`).Scan(&parent, &svcHost, &proxyHostAfter); err != nil {
		t.Fatal(err)
	}
	if parent.Valid || svcHost.Valid || proxyHostAfter.Valid {
		t.Fatalf("deleting the host left parent=%v service=%v proxy=%v", parent, svcHost, proxyHostAfter)
	}
	var ports int
	if err = q.QueryRow(`SELECT COUNT(*) FROM ports WHERE id=301`).Scan(&ports); err != nil || ports != 0 {
		t.Fatalf("host port survived its host: %d, %v", ports, err)
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
