package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func countChanges(t *testing.T, st *store.Store, where string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM changes WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyHostAliasesDiffButNotReportedLastSeen(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	id, err := st.CreateConnector(ctx, "tailscale", "Tailnet", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	t1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.FixedZone("IST", 19800))
	host := domain.Host{Key: "tailscale:n1", Name: "NAS", Kind: domain.HostKindTailscale, Address: "100.64.0.5",
		Aliases: []string{"nas.tail1234.ts.net", " fd7a:115c:a1e0::5", "nas", "nas", ""}, ReportedLastSeen: &t1}
	apply := func(hosts ...domain.Host) int {
		t.Helper()
		_, n, err := a.Apply(ctx, id, domain.Snapshot{Hosts: hosts})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := apply(host); n != 1 {
		t.Fatalf("first scan changes=%d, want 1", n)
	}
	var aliases string
	var reported sql.NullString
	read := func() {
		t.Helper()
		if err := st.DB().QueryRow(`SELECT aliases,reported_last_seen FROM hosts WHERE natural_key='tailscale:n1'`).Scan(&aliases, &reported); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if aliases != `["fd7a:115c:a1e0::5","nas","nas.tail1234.ts.net"]` || reported.String != "2026-09-01T04:30:00Z" {
		t.Fatalf("stored aliases=%s reported=%v", aliases, reported)
	}

	t2 := t1.Add(7 * time.Minute)
	host.ReportedLastSeen = &t2
	host.Aliases = []string{"nas", "nas.tail1234.ts.net", "fd7a:115c:a1e0::5"} // reordered only
	if n := apply(host); n != 0 {
		t.Fatalf("last-seen and alias order changes=%d, want 0", n)
	}
	read()
	if reported.String != "2026-09-01T04:37:00Z" {
		t.Fatalf("reported_last_seen=%v, want the newer poll", reported)
	}
	if n := countChanges(t, st, `change_kind='modified'`); n != 0 {
		t.Fatalf("modified rows=%d, want 0", n)
	}

	host.Aliases = []string{"nas", "nas.tail9999.ts.net", "fd7a:115c:a1e0::5"}
	if n := apply(host); n != 1 {
		t.Fatalf("alias change changes=%d, want 1", n)
	}
	var diff string
	if err = st.DB().QueryRow(`SELECT diff FROM changes WHERE change_kind='modified'`).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	var fields map[string]map[string]string
	if err = json.Unmarshal([]byte(diff), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields["aliases"]["before"] != "fd7a:115c:a1e0::5, nas, nas.tail1234.ts.net" || fields["aliases"]["after"] != "fd7a:115c:a1e0::5, nas, nas.tail9999.ts.net" {
		t.Fatalf("diff=%s, want only aliases", diff)
	}

	host.ReportedLastSeen = nil
	if n := apply(host); n != 0 {
		t.Fatalf("missing last-seen changes=%d, want 0", n)
	}
	read()
	if reported.Valid {
		t.Fatalf("reported_last_seen=%v, want NULL when the source stops reporting it", reported)
	}
	if n := apply(); n != 1 {
		t.Fatalf("empty scan changes=%d, want 1 removal", n)
	}
	if n := countChanges(t, st, `change_kind='removed' AND entity_type='host'`); n != 1 {
		t.Fatalf("removed rows=%d, want 1", n)
	}
}

func TestApplyHostWithoutAliasesMatchesMigratedRow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	id, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01T00:00:00Z"
	// A row as migration 0008 leaves it: aliases defaulted, no reported time.
	if _, err = st.DB().Exec(`INSERT INTO hosts(connector_id,natural_key,name,kind,address,state,first_seen,last_seen,created_at,updated_at) VALUES(?,'docker:nas','nas','docker','10.0.0.2','active',?,?,?,?)`, id, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	_, n, err := New(st, nil).Apply(ctx, id, domain.Snapshot{Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker", Address: "10.0.0.2"}}})
	if err != nil {
		t.Fatal(err)
	}
	var aliases string
	if err = st.DB().QueryRow(`SELECT aliases FROM hosts WHERE natural_key='docker:nas'`).Scan(&aliases); err != nil {
		t.Fatal(err)
	}
	if n != 0 || aliases != "[]" {
		t.Fatalf("upgrade rescan changes=%d aliases=%s, want 0 and []", n, aliases)
	}
}

func TestApplyStoresMissingRoutePortAsNull(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	id, err := st.CreateConnector(ctx, "nginx", "nginx", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snap := domain.Snapshot{Routes: []domain.Route{
		{Key: "nginx:app.example:/", Domain: "app.example", PathPrefix: "/", UpstreamHost: "$backend"},
		{Key: "nginx:odd.example:/", Domain: "odd.example", PathPrefix: "/", UpstreamHost: "odd", UpstreamPort: 70000},
	}}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatalf("route without a port failed Apply: %v", err)
	}
	var nulls int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM routes WHERE upstream_port IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 2 {
		t.Fatalf("routes with NULL port=%d, want 2", nulls)
	}
	if _, n, err := a.Apply(ctx, id, snap); err != nil || n != 0 {
		t.Fatalf("rescan changes=%d err=%v, want 0", n, err)
	}
	snap.Routes[0].UpstreamHost, snap.Routes[0].UpstreamPort = "app", 8080
	if _, n, err := a.Apply(ctx, id, snap); err != nil || n != 1 {
		t.Fatalf("resolved port changes=%d err=%v, want 1", n, err)
	}
}

type endpointConnector struct {
	runnerConnector
	endpoint string
	err      error
}

func (c *endpointConnector) ProxyEndpoint(connectors.Config) (string, error) { return c.endpoint, c.err }

func newTestRunner(t *testing.T, st *store.Store, cs ...connectors.Connector) (*Runner, *store.ConnectorConfigs) {
	t.Helper()
	box, err := auth.NewSecretBox(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	configs := store.NewConnectorConfigs(st, box)
	reg := connectors.NewRegistry()
	for _, c := range cs {
		if err = reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	return NewRunner(st, configs, reg, New(st, nil)), configs
}

func insertHost(t *testing.T, st *store.Store, key, name, kind, address, aliases string) int64 {
	t.Helper()
	now := "2026-01-01T00:00:00Z"
	res, err := st.DB().Exec(`INSERT INTO hosts(natural_key,name,kind,address,aliases,state,first_seen,last_seen,created_at,updated_at) VALUES(?,?,?,?,?,'active',?,?,?,?)`, key, name, kind, address, aliases, now, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestRunnerStoresFileBasedProxyEndpoint(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name, endpoint string
		linked         bool
	}{
		{"named host", "file://nas/etc/nginx", true},
		{"no host", "file:///etc/nginx", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := openTestStore(t)
			nas := insertHost(t, st, "docker:nas", "nas", "docker", "10.0.0.2", "[]")
			c := &endpointConnector{runnerConnector: runnerConnector{kind: "nginx", snapshot: domain.Snapshot{Routes: []domain.Route{{Key: "nginx:app.example:/", Domain: "app.example", PathPrefix: "/", UpstreamHost: "app", UpstreamPort: 80}}}}, endpoint: tt.endpoint}
			runner, configs := newTestRunner(t, st, c)
			id, err := configs.Create(ctx, "nginx", "nginx", map[string]string{"path": "/etc/nginx"})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = runner.Scan(ctx, id); err != nil {
				t.Fatal(err)
			}
			var kind, endpoint string
			var hostID sql.NullInt64
			var routes int
			if err = st.DB().QueryRow(`SELECT p.kind,p.endpoint,p.host_id,(SELECT COUNT(*) FROM routes r WHERE r.proxy_id=p.id) FROM proxies p WHERE p.connector_id=?`, id).Scan(&kind, &endpoint, &hostID, &routes); err != nil {
				t.Fatal(err)
			}
			if kind != "nginx" || endpoint != tt.endpoint || routes != 1 || hostID.Valid != tt.linked || (tt.linked && hostID.Int64 != nas) {
				t.Fatalf("kind=%q endpoint=%q host=%v routes=%d", kind, endpoint, hostID, routes)
			}
		})
	}
}

func TestRunnerRequiresAProxyEndpoint(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"traefik", "caddy", "npm", "nginx"} {
		t.Run(kind, func(t *testing.T) {
			st := openTestStore(t)
			runner, configs := newTestRunner(t, st, &runnerConnector{kind: kind})
			id, err := configs.Create(ctx, kind, kind, map[string]string{"path": "/etc/nginx"})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = runner.Scan(ctx, id); err == nil || err.Error() != "proxy endpoint URL is required" {
				t.Fatalf("scan without url err=%v", err)
			}
		})
	}
	st := openTestStore(t)
	c := &endpointConnector{runnerConnector: runnerConnector{kind: "nginx"}, err: errors.New("path must be an absolute path inside the Homedex container")}
	runner, configs := newTestRunner(t, st, c)
	id, err := configs.Create(ctx, "nginx", "nginx", map[string]string{"path": "etc/nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = runner.Scan(ctx, id); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("endpoint error not surfaced: %v", err)
	}
	var status string
	if err = st.DB().QueryRow(`SELECT last_status FROM connectors WHERE id=?`, id).Scan(&status); err != nil || status != "error" {
		t.Fatalf("connector status=%q err=%v, want error", status, err)
	}
}

func TestAddRouteTargetsSkipsWildcardDomains(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := "2026-01-01T00:00:00Z"
	for _, d := range []string{"photos.example", "*.example.com", "radarr.*"} {
		if _, err := st.DB().Exec(`INSERT INTO routes(domain,natural_key,tls,state,first_seen,last_seen,created_at,updated_at) VALUES(?,?,1,'active',?,?,?,?)`, d, d, now, now, now, now); err != nil {
			t.Fatal(err)
		}
	}
	runner := &Runner{Store: st}
	for kind, key := range map[string]string{"tlsprobe": "targets", "rdap": "domains"} {
		cfg := connectors.Config{}
		runner.addRouteTargets(ctx, kind, cfg)
		var got []string
		_ = json.Unmarshal(cfg[key], &got)
		want := "photos.example"
		if kind == "tlsprobe" {
			want += ":443"
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%s %s=%v, want [%s]", kind, key, got, want)
		}
	}
}

const tailnetAliases = `["fd7a:115c:a1e0::5","nas","nas.tail1234.ts.net"]`

func TestEnsureProxyLinksTailnetNameToMachine(t *testing.T) {
	ctx := context.Background()
	scan := func(t *testing.T, extra func(*store.Store)) (sql.NullInt64, int64) {
		st := openTestStore(t)
		nas := insertHost(t, st, "docker:nas", "nas", "docker", "10.0.0.2", "[]")
		insertHost(t, st, "tailscale:n1", "NAS", "tailscale", "100.64.0.5", tailnetAliases)
		if extra != nil {
			extra(st)
		}
		runner, configs := newTestRunner(t, st, &runnerConnector{kind: "caddy"})
		id, err := configs.Create(ctx, "caddy", "Caddy", map[string]string{"url": "http://nas.tail1234.ts.net:2019"})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = runner.Scan(ctx, id); err != nil {
			t.Fatal(err)
		}
		var hostID sql.NullInt64
		if err = st.DB().QueryRow(`SELECT host_id FROM proxies WHERE connector_id=?`, id).Scan(&hostID); err != nil {
			t.Fatal(err)
		}
		return hostID, nas
	}
	if hostID, nas := scan(t, nil); !hostID.Valid || hostID.Int64 != nas {
		t.Fatalf("proxy host=%v, want the docker host %d behind the tailnet name", hostID, nas)
	}
	// A second machine named nas makes the link ambiguous: no guess.
	hostID, _ := scan(t, func(st *store.Store) { insertHost(t, st, "ssh:nas", "nas", "ssh", "10.0.0.9", "[]") })
	if hostID.Valid {
		t.Fatalf("proxy host=%v, want NULL for an unlinked tailnet device", hostID)
	}
}

func TestTailnetScanResolvesExistingRouteWithoutRouteChanges(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	docker := &runnerConnector{kind: "docker", snapshot: domain.Snapshot{
		Hosts:    []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker", Address: "10.0.0.2"}},
		Services: []domain.Service{{Key: "container:app", HostKey: "docker:nas", Name: "app", State: "running"}},
		Ports:    []domain.Port{{ServiceKey: "container:app", HostKey: "docker:nas", Number: 8080, ContainerPort: 80, Protocol: "tcp", Published: true, HostIP: "0.0.0.0"}},
	}}
	caddy := &runnerConnector{kind: "caddy", snapshot: domain.Snapshot{Routes: []domain.Route{
		{Key: "caddy:app.example", Domain: "app.example", PathPrefix: "/", UpstreamHost: "100.64.0.5", UpstreamPort: 8080},
	}}}
	lastSeen := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	tailnet := &runnerConnector{kind: "tailscale", snapshot: domain.Snapshot{Hosts: []domain.Host{{
		Key: "tailscale:n1", Name: "NAS", Kind: domain.HostKindTailscale, Address: "100.64.0.5",
		Aliases: []string{"fd7a:115c:a1e0::5", "nas.tail1234.ts.net", "nas"}, ReportedLastSeen: &lastSeen,
	}}}}
	runner, configs := newTestRunner(t, st, docker, caddy, tailnet)
	ids := map[string]int64{}
	for _, kind := range []string{"docker", "caddy", "tailscale"} {
		cfg := map[string]string{}
		if kind == "caddy" {
			cfg["url"] = "http://caddy:2019"
		}
		id, err := configs.Create(ctx, kind, kind, cfg)
		if err != nil {
			t.Fatal(err)
		}
		ids[kind] = id
	}
	route := func() (string, string) {
		t.Helper()
		var confidence, status string
		if err := st.DB().QueryRow(`SELECT resolve_confidence,status FROM routes WHERE natural_key='caddy:app.example'`).Scan(&confidence, &status); err != nil {
			t.Fatal(err)
		}
		return confidence, status
	}
	for _, kind := range []string{"docker", "caddy"} {
		if _, _, err := runner.Scan(ctx, ids[kind]); err != nil {
			t.Fatal(err)
		}
	}
	if confidence, status := route(); confidence != "none" || status != "broken" {
		t.Fatalf("before the tailnet scan route=%s/%s, want none/broken", confidence, status)
	}
	for range 2 {
		if _, _, err := runner.Scan(ctx, ids["tailscale"]); err != nil {
			t.Fatal(err)
		}
		lastSeen = lastSeen.Add(time.Minute)
	}
	if confidence, status := route(); confidence != "medium" || status != "ok" {
		t.Fatalf("after the tailnet scan route=%s/%s, want medium/ok", confidence, status)
	}
	var service string
	if err := st.DB().QueryRow(`SELECT s.natural_key FROM routes r JOIN services s ON s.id=r.resolved_service_id WHERE r.natural_key='caddy:app.example'`).Scan(&service); err != nil || service != "container:app" {
		t.Fatalf("resolved service=%q err=%v", service, err)
	}
	if n := countChanges(t, st, `entity_type='route' AND change_kind!='added'`); n != 0 {
		t.Fatalf("route change rows=%d, want 0", n)
	}
	if n := countChanges(t, st, `entity_type='host' AND change_kind='modified'`); n != 0 {
		t.Fatalf("host modified rows=%d after a last-seen-only rescan, want 0", n)
	}
}
