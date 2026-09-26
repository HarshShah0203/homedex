package engine

import (
	"context"
	"database/sql"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

// TestDNSViewScanResolvesRouteAndLinksProxy runs a DNS source next to Docker
// and Caddy: its view host is stored with kind dns, the existing route whose
// upstream is a local DNS name resolves right after the DNS scan, and the
// proxy reached by such a name is scoped to the machine behind it.
func TestDNSViewScanResolvesRouteAndLinksProxy(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	docker := &runnerConnector{kind: "docker", snapshot: domain.Snapshot{
		Hosts:    []domain.Host{{Key: "docker:nas", Name: "storage", Kind: "docker", Address: "192.0.2.10"}},
		Services: []domain.Service{{Key: "container:jellyfin", HostKey: "docker:nas", Name: "jellyfin", State: "running"}},
		Ports:    []domain.Port{{ServiceKey: "container:jellyfin", HostKey: "docker:nas", Number: 8096, ContainerPort: 8096, Protocol: "tcp", Published: true, HostIP: "0.0.0.0"}},
	}}
	caddy := &runnerConnector{kind: "caddy", snapshot: domain.Snapshot{Routes: []domain.Route{
		{Key: "caddy:media.example", Domain: "media.example", PathPrefix: "/", UpstreamHost: "nas.home.arpa", UpstreamPort: 8096},
	}}}
	dns := &runnerConnector{kind: "pihole", snapshot: domain.Snapshot{Hosts: []domain.Host{{
		Key: "pihole:192.0.2.10", Name: "nas.home.arpa", Kind: domain.HostKindDNS, Address: "192.0.2.10",
		Aliases: []string{"media.home.arpa", "nas.home.arpa"},
	}}}}
	runner, configs := newTestRunner(t, st, docker, caddy, dns)
	ids := map[string]int64{}
	for _, kind := range []string{"docker", "caddy", "pihole"} {
		cfg := map[string]string{}
		if kind == "caddy" {
			cfg["url"] = "http://media.home.arpa:2019"
		}
		id, err := configs.Create(ctx, kind, kind, cfg)
		if err != nil {
			t.Fatal(err)
		}
		ids[kind] = id
	}
	scan := func(kind string) {
		t.Helper()
		if _, _, err := runner.Scan(ctx, ids[kind]); err != nil {
			t.Fatalf("%s scan: %v", kind, err)
		}
	}
	route := func() (string, string, string) {
		t.Helper()
		var confidence, status string
		var service sql.NullString
		if err := st.DB().QueryRow(`SELECT r.resolve_confidence,r.status,s.natural_key FROM routes r LEFT JOIN services s ON s.id=r.resolved_service_id WHERE r.natural_key='caddy:media.example'`).Scan(&confidence, &status, &service); err != nil {
			t.Fatal(err)
		}
		return confidence, status, service.String
	}
	proxyHost := func() sql.NullInt64 {
		t.Helper()
		var id sql.NullInt64
		if err := st.DB().QueryRow(`SELECT host_id FROM proxies WHERE connector_id=?`, ids["caddy"]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	scan("docker")
	scan("caddy")
	if confidence, status, _ := route(); confidence != "none" || status != "broken" {
		t.Fatalf("before the DNS scan route=%s/%s, want none/broken", confidence, status)
	}
	if id := proxyHost(); id.Valid {
		t.Fatalf("before the DNS scan proxy host=%v, want NULL", id)
	}

	scan("pihole")
	var kind, aliases string
	if err := st.DB().QueryRow(`SELECT kind,aliases FROM hosts WHERE natural_key='pihole:192.0.2.10'`).Scan(&kind, &aliases); err != nil || kind != "dns" || aliases != `["media.home.arpa","nas.home.arpa"]` {
		t.Fatalf("dns view stored as kind=%q aliases=%s err=%v", kind, aliases, err)
	}
	if confidence, status, service := route(); confidence != "medium" || status != "ok" || service != "container:jellyfin" {
		t.Fatalf("after the DNS scan route=%s/%s -> %q, want medium/ok -> container:jellyfin", confidence, status, service)
	}

	scan("caddy")
	var machine int64
	if err := st.DB().QueryRow(`SELECT id FROM hosts WHERE natural_key='docker:nas'`).Scan(&machine); err != nil {
		t.Fatal(err)
	}
	if id := proxyHost(); !id.Valid || id.Int64 != machine {
		t.Fatalf("proxy host=%v, want the docker host %d behind media.home.arpa", id, machine)
	}
	if confidence, status, service := route(); confidence != "medium" || status != "ok" || service != "container:jellyfin" {
		t.Fatalf("after the proxy rescan route=%s/%s -> %q", confidence, status, service)
	}
}
