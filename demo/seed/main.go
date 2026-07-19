package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/store"
)

type fixtureStats struct {
	Hosts    int
	Services int
	Ports    int
	Routes   int
	Certs    int
	Domains  int
}

func main() {
	dataDir := flag.String("data-dir", "data-demo", "directory for the seeded Homedex database")
	reset := flag.Bool("reset", false, "replace an existing seeded database")
	flag.Parse()

	stats, err := seed(context.Background(), *dataDir, *reset)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed fake lab:", err)
		os.Exit(1)
	}
	fmt.Printf("seeded fake lab: %d hosts, %d services, %d ports, %d routes, %d certificates, %d domains\n",
		stats.Hosts, stats.Services, stats.Ports, stats.Routes, stats.Certs, stats.Domains)
}

func seed(ctx context.Context, dataDir string, reset bool) (fixtureStats, error) {
	if dataDir == "" {
		return fixtureStats{}, fmt.Errorf("data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fixtureStats{}, fmt.Errorf("create data directory: %w", err)
	}
	dbPath := filepath.Join(dataDir, "homedex.db")
	if reset {
		for _, suffix := range []string{"", "-shm", "-wal"} {
			if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
				return fixtureStats{}, fmt.Errorf("remove old database: %w", err)
			}
		}
	} else if _, err := os.Stat(dbPath); err == nil {
		return fixtureStats{}, fmt.Errorf("%s already exists; pass --reset to replace it", dbPath)
	} else if !os.IsNotExist(err) {
		return fixtureStats{}, fmt.Errorf("inspect database path: %w", err)
	}

	box, err := auth.LoadOrCreateSecretBox(dataDir)
	if err != nil {
		return fixtureStats{}, fmt.Errorf("initialize instance key: %w", err)
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fixtureStats{}, err
	}
	defer st.Close()

	config := map[string]any{
		"endpoint":     "tcp://127.0.0.1:9",
		"host_name":    "Deterministic fake lab",
		"host_address": "127.0.0.1",
	}
	configs := store.NewConnectorConfigs(st, box)
	connectorID, err := configs.Create(ctx, "docker", "Fake lab fixture", config)
	if err != nil {
		return fixtureStats{}, fmt.Errorf("create fixture connector: %w", err)
	}

	snapshot := fakeLabSnapshot()
	applier := engine.New(st, nil)
	if _, _, err = applier.Apply(ctx, connectorID, snapshot); err != nil {
		return fixtureStats{}, fmt.Errorf("apply fixture snapshot: %w", err)
	}
	if err = applier.ReconcileRoutes(ctx); err != nil {
		return fixtureStats{}, fmt.Errorf("resolve fixture routes: %w", err)
	}
	// Register the reverse proxy that declared these routes so route detail
	// names it, mirroring what the runner records for live proxy connectors.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := st.DB().ExecContext(ctx, `INSERT INTO proxies(kind,host_id,endpoint,connector_id,last_scan) SELECT 'traefik', id, 'http://traefik:8080/api', ?, ? FROM hosts WHERE name='gateway'`, connectorID, now)
	if err != nil {
		return fixtureStats{}, fmt.Errorf("register fixture proxy: %w", err)
	}
	proxyID, err := res.LastInsertId()
	if err != nil {
		return fixtureStats{}, fmt.Errorf("read fixture proxy id: %w", err)
	}
	if _, err = st.DB().ExecContext(ctx, `UPDATE routes SET proxy_id=?`, proxyID); err != nil {
		return fixtureStats{}, fmt.Errorf("attach fixture proxy to routes: %w", err)
	}
	// Keep the fixture visible in Settings without allowing the scheduler to
	// contact the intentionally non-routable placeholder endpoint.
	if err = configs.Update(ctx, connectorID, "Fake lab fixture", config, false, 1440); err != nil {
		return fixtureStats{}, fmt.Errorf("disable fixture connector: %w", err)
	}

	return fixtureStats{
		Hosts: len(snapshot.Hosts), Services: len(snapshot.Services), Ports: len(snapshot.Ports),
		Routes: len(snapshot.Routes), Certs: len(snapshot.Certs), Domains: len(snapshot.Domains),
	}, nil
}

func fakeLabSnapshot() domain.Snapshot {
	checked := mustTime("2026-07-16T06:00:00Z")
	domainExpiry := mustTime("2027-02-18T00:00:00Z")

	hosts := []domain.Host{
		{Key: "docker:gateway", Name: "gateway", Kind: "docker", Address: "10.0.10.5", OS: "Debian 13", Arch: "amd64"},
		{Key: "docker:nas-01", Name: "nas-01", Kind: "docker", Address: "10.0.20.10", OS: "Ubuntu 24.04", Arch: "amd64"},
		{Key: "docker:core-01", Name: "core-01", Kind: "docker", Address: "10.0.10.8", OS: "Alpine 3.22", Arch: "arm64"},
	}

	services := []domain.Service{
		service("traefik", "gateway", "proxy", "traefik", "v3.4.1", "172.30.0.2", "traefik"),
		service("authelia", "gateway", "identity", "authelia/authelia", "4.39.4", "172.30.0.3", "authelia"),
		service("vaultwarden", "gateway", "identity", "vaultwarden/server", "1.34.1", "172.30.0.4", "vaultwarden"),
		service("immich-server", "nas-01", "immich", "ghcr.io/immich-app/immich-server", "v1.135.3", "172.31.0.10", "immich-server", "immich"),
		service("jellyfin", "nas-01", "media", "jellyfin/jellyfin", "10.10.7", "172.31.0.11", "jellyfin"),
		service("paperless-web", "nas-01", "paperless", "ghcr.io/paperless-ngx/paperless-ngx", "2.17.1", "172.31.0.12", "paperless-web", "paperless"),
		service("nextcloud", "nas-01", "cloud", "nextcloud", "31.0.6", "172.31.0.13", "nextcloud"),
		service("gitea", "nas-01", "dev", "gitea/gitea", "1.24.2", "172.31.0.14", "gitea"),
		service("pihole", "core-01", "network", "pihole/pihole", "2026.05", "172.32.0.10", "pihole"),
		service("home-assistant", "core-01", "automation", "ghcr.io/home-assistant/home-assistant", "2026.7.1", "172.32.0.11", "home-assistant"),
		service("grafana", "core-01", "observability", "grafana/grafana", "12.0.2", "172.32.0.12", "grafana"),
		service("uptime-kuma", "core-01", "observability", "louislam/uptime-kuma", "1.23.16", "172.32.0.13", "uptime-kuma"),
	}
	// A documentation-only public address exercises external-IP masking without
	// referring to a real endpoint or causing the demo to contact one.
	services[11].RawLabels["homedex.demo.example-egress"] = "203.0.113.10"

	ports := []domain.Port{
		port("traefik", "gateway", 80, 80, true, "tcp"),
		port("traefik", "gateway", 443, 443, true, "tcp"),
		port("traefik", "gateway", 8080, 8080, false, "tcp"),
		port("authelia", "gateway", 9091, 9091, false, "tcp"),
		port("vaultwarden", "gateway", 80, 80, false, "tcp"),
		port("immich-server", "nas-01", 2283, 2283, false, "tcp"),
		port("jellyfin", "nas-01", 8096, 8096, true, "tcp"),
		port("paperless-web", "nas-01", 8000, 8000, false, "tcp"),
		port("nextcloud", "nas-01", 80, 80, false, "tcp"),
		port("gitea", "nas-01", 3000, 3000, false, "tcp"),
		port("gitea", "nas-01", 2222, 22, true, "tcp"),
		port("pihole", "core-01", 53, 53, true, "tcp"),
		port("pihole", "core-01", 53, 53, true, "udp"),
		port("home-assistant", "core-01", 8123, 8123, true, "tcp"),
		port("grafana", "core-01", 3000, 3000, false, "tcp"),
		port("uptime-kuma", "core-01", 3001, 3001, false, "tcp"),
	}

	routes := []domain.Route{
		route("photos.lab.example", "immich", 2283),
		route("watch.lab.example", "jellyfin", 8096),
		route("docs.lab.example", "paperless", 8000),
		route("cloud.lab.example", "nextcloud", 80),
		route("git.lab.example", "gitea", 3000),
		route("auth.lab.example", "authelia", 9091),
		route("vault.lab.example", "vaultwarden", 80),
		route("metrics.lab.example", "grafana", 3000),
		{Key: "demo:home.lab.example", Domain: "home.lab.example", PathPrefix: "/", UpstreamHost: "10.0.10.8", UpstreamPort: 8123, TLS: true, Status: "unknown"},
		{Key: "demo:old.lab.example", Domain: "old.lab.example", PathPrefix: "/", UpstreamHost: "retired-wiki", UpstreamPort: 3000, TLS: true, Status: "unknown"},
	}

	certs := []domain.Cert{
		cert("auth.lab.example", "2026-07-30T00:00:00Z", []string{"auth.lab.example", "vault.lab.example"}),
		cert("docs.lab.example", "2026-08-08T00:00:00Z", []string{"docs.lab.example", "cloud.lab.example"}),
		cert("watch.lab.example", "2026-09-18T00:00:00Z", []string{"watch.lab.example", "metrics.lab.example"}),
		cert("photos.lab.example", "2026-10-13T00:00:00Z", []string{"photos.lab.example", "git.lab.example", "home.lab.example"}),
	}

	domains := []domain.Domain{{
		Key: "rdap:lab.example", Name: "lab.example", Registrar: "Example Registrar",
		ExpiresAt: &domainExpiry, Nameservers: []string{"ns1.lab.example", "ns2.lab.example"},
		Source: "demo", LastChecked: &checked,
	}}

	return domain.Snapshot{Hosts: hosts, Services: services, Ports: ports, Routes: routes, Certs: certs, Domains: domains}
}

func service(name, host, stack, image, tag, ip string, aliases ...string) domain.Service {
	labels := map[string]string{
		"com.docker.compose.project": stack,
		"com.docker.compose.service": name,
	}
	return domain.Service{
		Key: "container:" + host + ":" + name, HostKey: "docker:" + host, Name: name,
		Kind: "container", Stack: stack, Image: image, Tag: tag, State: "running", Health: "healthy",
		RestartPolicy: "unless-stopped", RawLabels: labels,
		Networks: []domain.ServiceNetwork{{Name: "proxy", IP: ip, Aliases: aliases}},
	}
}

func port(serviceName, host string, number, containerPort int, published bool, protocol string) domain.Port {
	hostIP := ""
	if published {
		hostIP = "0.0.0.0"
	}
	return domain.Port{
		ServiceKey: "container:" + host + ":" + serviceName, HostKey: "docker:" + host,
		Number: number, ContainerPort: containerPort, Published: published, Protocol: protocol,
		HostIP: hostIP, Source: "docker",
	}
}

func route(domainName, upstream string, upstreamPort int) domain.Route {
	return domain.Route{
		Key: "demo:" + domainName, Domain: domainName, PathPrefix: "/", UpstreamHost: upstream,
		UpstreamPort: upstreamPort, TLS: true, Status: "unknown",
	}
}

func cert(subject, notAfter string, sans []string) domain.Cert {
	return domain.Cert{
		Key: "tls:" + subject + ":443", Subject: subject, SANs: sans, Issuer: "Let's Encrypt R11",
		NotAfter: mustTime(notAfter), ChainValid: true, Source: "demo", Endpoint: subject + ":443",
	}
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
