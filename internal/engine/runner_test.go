package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

type runnerConnector struct {
	kind     string
	snapshot domain.Snapshot
	err      error
}
type capturePublisher struct{ events []Event }

func (p *capturePublisher) Publish(e Event) { p.events = append(p.events, e) }

func (c *runnerConnector) Kind() string {
	if c.kind != "" {
		return c.kind
	}
	return "fixture"
}
func (*runnerConnector) Validate(context.Context, connectors.Config) error { return nil }
func (c *runnerConnector) Scan(context.Context, connectors.Config) (domain.Snapshot, error) {
	return c.snapshot, c.err
}
func TestRunnerPersistsProxyIdentityOnRoutes(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{7}, 32))
	configs := store.NewConnectorConfigs(st, box)
	id, _ := configs.Create(ctx, "caddy", "Caddy", map[string]string{"url": "http://nas:2019"})
	now := "2026-01-01T00:00:00Z"
	_, _ = st.DB().Exec(`INSERT INTO hosts(natural_key,name,kind,address,state,first_seen,last_seen,created_at,updated_at) VALUES('host:nas','nas','docker','10.0.0.2','active',?,?,?,?)`, now, now, now, now)
	c := &runnerConnector{kind: "caddy", snapshot: domain.Snapshot{Routes: []domain.Route{{Key: "route", Domain: "app.example", UpstreamHost: "app", UpstreamPort: 80}}}}
	reg := connectors.NewRegistry()
	_ = reg.Register(c)
	runner := NewRunner(st, configs, reg, New(st, nil))
	if _, _, e = runner.Scan(ctx, id); e != nil {
		t.Fatal(e)
	}
	var kind, endpoint string
	var proxyID, hostID int64
	if e = st.DB().QueryRow(`SELECT r.proxy_id,p.host_id,p.kind,p.endpoint FROM routes r JOIN proxies p ON p.id=r.proxy_id WHERE r.natural_key='route'`).Scan(&proxyID, &hostID, &kind, &endpoint); e != nil {
		t.Fatal(e)
	}
	if proxyID == 0 || hostID == 0 || kind != "caddy" || endpoint != "http://nas:2019" {
		t.Fatalf("proxy=%d host=%d kind=%q endpoint=%q", proxyID, hostID, kind, endpoint)
	}
}

func TestRunnerScopesDuplicateNetworkIPRoutes(t *testing.T) {
	tests := []struct {
		name               string
		endpoint           string
		wantServiceSource  bool
		wantConfidence     string
		wantStatus         string
		wantProxyHostMatch bool
	}{
		{
			name:               "unique proxy source",
			endpoint:           "http://10.0.0.3:2019",
			wantServiceSource:  true,
			wantConfidence:     "high",
			wantStatus:         "ok",
			wantProxyHostMatch: true,
		},
		{
			name:           "ambiguous proxy source",
			endpoint:       "http://nas:2019",
			wantConfidence: "none",
			wantStatus:     "broken",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			firstDocker, err := st.CreateConnector(ctx, "docker", "Docker one", nil)
			if err != nil {
				t.Fatal(err)
			}
			secondDocker, err := st.CreateConnector(ctx, "docker", "Docker two", nil)
			if err != nil {
				t.Fatal(err)
			}
			dockerSnapshot := func(address string) domain.Snapshot {
				return domain.Snapshot{
					Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker", Address: address}},
					Services: []domain.Service{{
						Key:     "container:abc123",
						HostKey: "docker:nas",
						Name:    "immich",
						State:   "running",
						Networks: []domain.ServiceNetwork{{
							Name: "apps",
							IP:   "172.20.0.8",
						}},
					}},
					Ports: []domain.Port{{
						ServiceKey:    "container:abc123",
						HostKey:       "docker:nas",
						Number:        2283,
						ContainerPort: 2283,
						Protocol:      "tcp",
					}},
				}
			}
			applier := New(st, nil)
			if _, _, err = applier.Apply(ctx, firstDocker, dockerSnapshot("10.0.0.2")); err != nil {
				t.Fatal(err)
			}
			if _, _, err = applier.Apply(ctx, secondDocker, dockerSnapshot("10.0.0.3")); err != nil {
				t.Fatal(err)
			}

			box, err := auth.NewSecretBox(bytes.Repeat([]byte{9}, 32))
			if err != nil {
				t.Fatal(err)
			}
			configs := store.NewConnectorConfigs(st, box)
			caddyID, err := configs.Create(ctx, "caddy", "Caddy", map[string]string{"url": tt.endpoint})
			if err != nil {
				t.Fatal(err)
			}
			connector := &runnerConnector{kind: "caddy", snapshot: domain.Snapshot{Routes: []domain.Route{{
				Key:          "route:photos.example:/",
				Domain:       "photos.example",
				PathPrefix:   "/",
				UpstreamHost: "172.20.0.8",
				UpstreamPort: 2283,
			}}}}
			registry := connectors.NewRegistry()
			if err = registry.Register(connector); err != nil {
				t.Fatal(err)
			}
			runner := NewRunner(st, configs, registry, applier)
			if _, _, err = runner.Scan(ctx, caddyID); err != nil {
				t.Fatal(err)
			}

			var resolvedServiceID, resolvedConnectorID, proxyHostID int64
			var confidence, status string
			if err = st.DB().QueryRow(`
				SELECT COALESCE(r.resolved_service_id,0),COALESCE(s.connector_id,0),
				       r.resolve_confidence,r.status,COALESCE(p.host_id,0)
				FROM routes r
				JOIN proxies p ON p.id=r.proxy_id
				LEFT JOIN services s ON s.id=r.resolved_service_id
				WHERE r.connector_id=? AND r.natural_key='route:photos.example:/'`, caddyID).
				Scan(&resolvedServiceID, &resolvedConnectorID, &confidence, &status, &proxyHostID); err != nil {
				t.Fatal(err)
			}
			if confidence != tt.wantConfidence || status != tt.wantStatus {
				t.Fatalf("route confidence/status=%q/%q, want %q/%q", confidence, status, tt.wantConfidence, tt.wantStatus)
			}
			if tt.wantServiceSource {
				if resolvedServiceID == 0 || resolvedConnectorID != secondDocker {
					t.Fatalf("resolved service id/source=%d/%d, want source %d", resolvedServiceID, resolvedConnectorID, secondDocker)
				}
			} else if resolvedServiceID != 0 || resolvedConnectorID != 0 {
				t.Fatalf("ambiguous route persisted service id/source=%d/%d", resolvedServiceID, resolvedConnectorID)
			}
			if tt.wantProxyHostMatch != (proxyHostID != 0) {
				t.Fatalf("proxy host id=%d, want matched=%t", proxyHostID, tt.wantProxyHostMatch)
			}
		})
	}
}

func TestRunnerRecordsFailureAndRetainsPreviousData(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{2}, 32))
	configs := store.NewConnectorConfigs(st, box)
	id, e := configs.Create(ctx, "fixture", "Fixture", map[string]string{"token": "read-only"})
	if e != nil {
		t.Fatal(e)
	}
	c := &runnerConnector{snapshot: domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app", State: "running"}}}}
	reg := connectors.NewRegistry()
	_ = reg.Register(c)
	pub := &capturePublisher{}
	runner := NewRunner(st, configs, reg, New(st, pub))
	if _, _, e = runner.Scan(ctx, id); e != nil {
		t.Fatal(e)
	}
	c.err = errors.New("upstream unavailable")
	if _, _, e = runner.Scan(ctx, id); e == nil {
		t.Fatal("failed scan succeeded")
	}
	var state, status, lastError string
	if e = st.DB().QueryRow(`SELECT state FROM services WHERE natural_key='svc'`).Scan(&state); e != nil {
		t.Fatal(e)
	}
	if e = st.DB().QueryRow(`SELECT last_status,last_error FROM connectors WHERE id=?`, id).Scan(&status, &lastError); e != nil {
		t.Fatal(e)
	}
	if state != "running" || status != "error" || lastError != "upstream unavailable" {
		t.Fatalf("state=%q status=%q error=%q", state, status, lastError)
	}
	var failed int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM scan_runs WHERE connector_id=? AND status='error'`, id).Scan(&failed)
	if failed != 1 {
		t.Fatalf("failed runs=%d", failed)
	}
	if len(pub.events) < 6 || pub.events[0].Type != "scan.started" || pub.events[len(pub.events)-1].Type != "scan.failed" || pub.events[len(pub.events)-1].Error != "upstream unavailable" {
		t.Fatalf("events=%#v", pub.events)
	}
	foundComplete := false
	foundStats := false
	for _, event := range pub.events {
		foundComplete = foundComplete || event.Type == "scan.complete"
		foundStats = foundStats || event.Type == "scan.progress" && event.Phase == "discover" && event.Stats["services"] == 1
	}
	if !foundComplete || !foundStats {
		t.Fatalf("missing complete or rich progress event: %#v", pub.events)
	}
}
func TestRunnerAddsDiscoveredRouteTargets(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	defer st.Close()
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{2}, 32))
	configs := store.NewConnectorConfigs(st, box)
	id, _ := configs.Create(ctx, "tlsprobe", "TLS", map[string]any{"targets": []string{"manual.example:443"}})
	now := "2026-01-01T00:00:00Z"
	_, _ = st.DB().Exec(`INSERT INTO routes(connector_id,domain,path_prefix,upstream_host,upstream_port,resolve_confidence,tls,status,natural_key,state,first_seen,last_seen,created_at,updated_at) VALUES(?, 'photos.example','/','app',80,'none',1,'unknown','route','active',?,?,?,?)`, id, now, now, now, now)
	runner := &Runner{Store: st}
	cfg := connectors.Config{"targets": json.RawMessage(`["manual.example:443"]`)}
	runner.addRouteTargets(ctx, "tlsprobe", cfg)
	var got []string
	_ = json.Unmarshal(cfg["targets"], &got)
	if len(got) != 2 || got[1] != "photos.example:443" {
		t.Fatalf("targets=%v", got)
	}
}
func TestSchedulerHonorsIntervalAndJitter(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{1}, 32))
	configs := store.NewConnectorConfigs(st, box)
	id, _ := configs.Create(ctx, "fixture", "Fixture", map[string]string{})
	fixed := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	_, _ = st.DB().Exec(`INSERT INTO scan_runs(connector_id,started_at,status) VALUES(?,?,'success')`, id, fixed.Format(time.RFC3339Nano))
	runner := &Runner{Store: st, Configs: configs}
	scheduler := NewScheduler(runner)
	rec, _ := configs.Record(ctx, id)
	scheduler.now = func() time.Time { return fixed.Add(13 * time.Minute) }
	if scheduler.due(ctx, rec) {
		t.Fatal("connector became due before jittered interval")
	}
	scheduler.now = func() time.Time { return fixed.Add(14 * time.Minute) }
	if !scheduler.due(ctx, rec) {
		t.Fatal("connector was not due after jittered interval")
	}
}
