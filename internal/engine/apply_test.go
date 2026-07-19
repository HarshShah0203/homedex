package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

type captureRuleEvaluator struct{ runs chan int64 }

func (c captureRuleEvaluator) Evaluate(_ context.Context, runID int64) error {
	c.runs <- runID
	return nil
}

func TestApplyIsIdempotentAndMarksMissingEntitiesGone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	connectorID, err := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snapshot := domain.Snapshot{Hosts: []domain.Host{{Key: "host:nas", Name: "nas", Kind: "docker", Address: "10.0.0.2"}}, Services: []domain.Service{{Key: "container:abc", HostKey: "host:nas", Name: "jellyfin", Stack: "media", Image: "jellyfin/jellyfin", Tag: "latest", State: "running"}}, Ports: []domain.Port{{ServiceKey: "container:abc", HostKey: "host:nas", Number: 8096, ContainerPort: 8096, Protocol: "tcp", Published: true, HostIP: "0.0.0.0", Source: "docker"}}}
	_, first, err := a.Apply(ctx, connectorID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if first != 3 {
		t.Fatalf("first scan changes=%d, want 3", first)
	}
	_, second, err := a.Apply(ctx, connectorID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Fatalf("idempotent scan changes=%d, want 0", second)
	}
	_, third, err := a.Apply(ctx, connectorID, domain.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if third != 3 {
		t.Fatalf("empty scan changes=%d, want 3", third)
	}
	var state string
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE natural_key='container:abc'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "gone" {
		t.Fatalf("service state=%q, want gone", state)
	}
	var lastSeen string
	if err = st.DB().QueryRow(`SELECT last_seen FROM services WHERE natural_key='container:abc'`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if lastSeen == "" {
		t.Fatal("last_seen was not retained")
	}
	var changes int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM changes`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 6 {
		t.Fatalf("change rows=%d, want 6", changes)
	}
}

func TestApplyTracksOnlyMeaningfulServiceChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snap := domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app", Image: "app", Tag: "v1", State: "running", RawLabels: map[string]string{"cosmetic": "one"}}}}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	snap.Services[0].RawLabels["cosmetic"] = "two"
	_, count, err := a.Apply(ctx, id, snap)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("untracked label generated %d changes", count)
	}
	snap.Services[0].Tag = "v2"
	_, count, err = a.Apply(ctx, id, snap)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("tag update generated %d changes, want 1", count)
	}
}

func TestPurgeGoneHonorsRetention(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	a.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, _, err = a.Apply(ctx, id, domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.Apply(ctx, id, domain.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) }
	if err = a.PurgeGone(ctx, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM services WHERE natural_key='svc'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("gone service count=%d, want 0", count)
	}
}

func TestApplyTreatsSameComposeIdentityAsRecreated(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	a := New(st, nil)
	host := domain.Host{Key: "host", Name: "nas", Kind: "docker"}
	old := domain.Snapshot{Hosts: []domain.Host{host}, Services: []domain.Service{{Key: "container:old", HostKey: "host", Name: "app", Stack: "stack", State: "running"}}}
	if _, _, err = a.Apply(ctx, id, old); err != nil {
		t.Fatal(err)
	}
	old.Services[0].Key = "container:new"
	_, changes, err := a.Apply(ctx, id, old)
	if err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("changes=%d, want one recreated change", changes)
	}
	var count int
	var key string
	if err = st.DB().QueryRow(`SELECT COUNT(*),MAX(natural_key) FROM services WHERE name='app'`).Scan(&count, &key); err != nil {
		t.Fatal(err)
	}
	if count != 1 || key != "container:new" {
		t.Fatalf("count=%d key=%q", count, key)
	}
}
func TestApplyDoesNotCollapseComposeReplicas(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	id, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	snap := domain.Snapshot{Services: []domain.Service{{Key: "one", Name: "web", Stack: "app"}, {Key: "two", Name: "web", Stack: "app"}}}
	if _, _, e = New(st, nil).Apply(ctx, id, snap); e != nil {
		t.Fatal(e)
	}
	var count int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM services WHERE name='web'`).Scan(&count)
	if count != 2 {
		t.Fatalf("replica count=%d", count)
	}
}

func TestApplyKeepsDuplicateDockerNaturalKeysScopedByConnector(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	firstConnector, err := st.CreateConnector(ctx, "docker", "Docker one", nil)
	if err != nil {
		t.Fatal(err)
	}
	secondConnector, err := st.CreateConnector(ctx, "docker", "Docker two", nil)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := func(network string) domain.Snapshot {
		return domain.Snapshot{
			Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker", Address: "10.0.0.2"}},
			Services: []domain.Service{{
				Key:     "container:abc123",
				HostKey: "docker:nas",
				Name:    "immich",
				Stack:   "photos",
				State:   "running",
				Networks: []domain.ServiceNetwork{{
					Name: network,
					IP:   "172.20.0.8",
				}},
			}},
			Ports: []domain.Port{{
				ServiceKey:    "container:abc123",
				HostKey:       "docker:nas",
				Number:        2283,
				ContainerPort: 2283,
				Protocol:      "tcp",
				Published:     true,
				HostIP:        "0.0.0.0",
				Source:        "docker",
			}},
		}
	}

	applier := New(st, nil)
	if _, _, err = applier.Apply(ctx, firstConnector, snapshot("source-one")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, secondConnector, snapshot("source-two")); err != nil {
		t.Fatal(err)
	}

	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM hosts WHERE natural_key='docker:nas' AND state='active'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("active duplicate-key hosts=%d, want 2", count)
	}
	if err = st.DB().QueryRow(`
		SELECT COUNT(*)
		FROM services s
		JOIN hosts h ON h.id=s.host_id
		WHERE s.natural_key='container:abc123'
		  AND s.connector_id=h.connector_id
		  AND s.state='running'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("source-owned duplicate-key services=%d, want 2", count)
	}
	if err = st.DB().QueryRow(`
		SELECT COUNT(*)
		FROM ports p
		JOIN services s ON s.id=p.service_id
		JOIN hosts h ON h.id=p.host_id
		WHERE p.connector_id=s.connector_id
		  AND p.connector_id=h.connector_id
		  AND s.natural_key='container:abc123'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("source-owned duplicate-key ports=%d, want 2", count)
	}
	if err = st.DB().QueryRow(`
		SELECT COUNT(*)
		FROM service_networks n
		JOIN services s ON s.id=n.service_id
		WHERE (s.connector_id=? AND n.network_name='source-one')
		   OR (s.connector_id=? AND n.network_name='source-two')`, firstConnector, secondConnector).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("source-owned service networks=%d, want 2", count)
	}

	if _, _, err = applier.Apply(ctx, firstConnector, domain.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	var firstHostState, firstServiceState, secondHostState, secondServiceState string
	if err = st.DB().QueryRow(`SELECT state FROM hosts WHERE connector_id=? AND natural_key='docker:nas'`, firstConnector).Scan(&firstHostState); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE connector_id=? AND natural_key='container:abc123'`, firstConnector).Scan(&firstServiceState); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT state FROM hosts WHERE connector_id=? AND natural_key='docker:nas'`, secondConnector).Scan(&secondHostState); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT state FROM services WHERE connector_id=? AND natural_key='container:abc123'`, secondConnector).Scan(&secondServiceState); err != nil {
		t.Fatal(err)
	}
	if firstHostState != "gone" || firstServiceState != "gone" || secondHostState != "active" || secondServiceState != "running" {
		t.Fatalf("states after source-one empty scan: first host/service=%q/%q second=%q/%q", firstHostState, firstServiceState, secondHostState, secondServiceState)
	}
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM ports WHERE connector_id=?`, secondConnector).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("source-two ports after source-one empty scan=%d, want 1", count)
	}
}

func TestApplyKeepsNPMAndTLSProbeCertificatesSeparate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	npmConnector, err := st.CreateConnector(ctx, "npm", "NPM", nil)
	if err != nil {
		t.Fatal(err)
	}
	tlsConnector, err := st.CreateConnector(ctx, "tlsprobe", "TLS probe", nil)
	if err != nil {
		t.Fatal(err)
	}

	const endpoint = "photos.example:443"
	expires := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	certificate := func(source string) domain.Snapshot {
		return domain.Snapshot{Certs: []domain.Cert{{
			Key:      "tls:" + endpoint,
			Subject:  "photos.example",
			SANs:     []string{"photos.example"},
			NotAfter: expires,
			Source:   source,
			Endpoint: endpoint,
		}}}
	}

	applier := New(st, nil)
	if _, _, err = applier.Apply(ctx, npmConnector, certificate("proxy")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, tlsConnector, certificate("probe")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, npmConnector, certificate("proxy")); err != nil {
		t.Fatal(err)
	}

	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM certs WHERE natural_key=? AND endpoint=? AND state='active'`, "tls:"+endpoint, endpoint).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("active shared-endpoint certificates=%d, want 2", count)
	}
	var npmSource, tlsSource string
	if err = st.DB().QueryRow(`SELECT source FROM certs WHERE connector_id=? AND natural_key=?`, npmConnector, "tls:"+endpoint).Scan(&npmSource); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT source FROM certs WHERE connector_id=? AND natural_key=?`, tlsConnector, "tls:"+endpoint).Scan(&tlsSource); err != nil {
		t.Fatal(err)
	}
	if npmSource != "proxy" || tlsSource != "probe" {
		t.Fatalf("certificate sources npm/tls=%q/%q", npmSource, tlsSource)
	}

	if _, _, err = applier.Apply(ctx, npmConnector, domain.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	var npmState, tlsState string
	if err = st.DB().QueryRow(`SELECT state FROM certs WHERE connector_id=? AND natural_key=?`, npmConnector, "tls:"+endpoint).Scan(&npmState); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT state FROM certs WHERE connector_id=? AND natural_key=?`, tlsConnector, "tls:"+endpoint).Scan(&tlsState); err != nil {
		t.Fatal(err)
	}
	if npmState != "gone" || tlsState != "active" {
		t.Fatalf("certificate states after NPM empty scan=%q/%q", npmState, tlsState)
	}
}

func TestApplyEvaluatesNotificationRulesAfterCommit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.CreateConnector(ctx, "fixture", "Fixture", nil)
	applier := New(st, nil)
	runs := make(chan int64, 1)
	applier.SetRuleEvaluator(captureRuleEvaluator{runs: runs})
	runID, _, err := applier.Apply(ctx, id, domain.Snapshot{Services: []domain.Service{{Key: "svc", Name: "app"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case evaluated := <-runs:
		if evaluated != runID {
			t.Fatalf("evaluated run=%d, want %d", evaluated, runID)
		}
		var status string
		if err = st.DB().QueryRow(`SELECT status FROM scan_runs WHERE id=?`, evaluated).Scan(&status); err != nil || status != "success" {
			t.Fatalf("notification ran before committed success: status=%q error=%v", status, err)
		}
	case <-time.After(time.Second):
		t.Fatal("notification rules were not evaluated")
	}
}

// BUG 1: reconciling ports by natural key must keep unchanged ports' ids stable,
// so user metadata (entity_notes/entity_tags/custom_fields keyed by the port id)
// is not orphaned when the port set changes.
func TestApplyPortsPreserveIDAndMetadataAcrossChange(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	base := domain.Snapshot{
		Hosts:    []domain.Host{{Key: "host", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{{Key: "svc", HostKey: "host", Name: "app", State: "running"}},
		Ports:    []domain.Port{{ServiceKey: "svc", HostKey: "host", Number: 8080, ContainerPort: 80, Protocol: "tcp", Published: true}},
	}
	if _, _, err = a.Apply(ctx, id, base); err != nil {
		t.Fatal(err)
	}
	var portID int64
	if err = st.DB().QueryRow(`SELECT id FROM ports WHERE connector_id=? AND number=8080`, id).Scan(&portID); err != nil {
		t.Fatal(err)
	}
	// User pins metadata to this port; it must survive a later scan.
	if _, err = st.DB().Exec(`INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES('port',?,'exposed intentionally','now')`, portID); err != nil {
		t.Fatal(err)
	}

	grow := base
	grow.Ports = append(append([]domain.Port(nil), base.Ports...), domain.Port{ServiceKey: "svc", HostKey: "host", Number: 9090, ContainerPort: 90, Protocol: "tcp", Published: true})
	if _, _, err = a.Apply(ctx, id, grow); err != nil {
		t.Fatal(err)
	}
	var stillID int64
	if err = st.DB().QueryRow(`SELECT id FROM ports WHERE connector_id=? AND number=8080`, id).Scan(&stillID); err != nil {
		t.Fatal(err)
	}
	if stillID != portID {
		t.Fatalf("unchanged port id changed %d -> %d, orphaning metadata", portID, stillID)
	}
	var notes string
	if err = st.DB().QueryRow(`SELECT notes FROM entity_notes WHERE entity_type='port' AND entity_id=?`, portID).Scan(&notes); err != nil {
		t.Fatalf("port metadata lost after reconcile: %v", err)
	}
	if notes != "exposed intentionally" {
		t.Fatalf("port notes=%q, want preserved", notes)
	}
	var count int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM ports WHERE connector_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("port count=%d, want 2", count)
	}
}

// BUG 2: port change events must use entity_type 'port' and the real port id (not
// 'ports'/connector id) so EntityManager.Detail history surfaces them.
func TestApplyEmitsPerPortChangeEventsWithRealIDs(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snap := domain.Snapshot{
		Hosts:    []domain.Host{{Key: "host", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{{Key: "svc", HostKey: "host", Name: "app", State: "running"}},
		Ports:    []domain.Port{{ServiceKey: "svc", HostKey: "host", Number: 8080, ContainerPort: 80, Protocol: "tcp", Published: true}},
	}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	var portID int64
	if err = st.DB().QueryRow(`SELECT id FROM ports WHERE connector_id=?`, id).Scan(&portID); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err = st.DB().QueryRow(`SELECT change_kind FROM changes WHERE entity_type='port' AND entity_id=? AND change_kind='added'`, portID).Scan(&kind); err != nil {
		t.Fatalf("no per-port 'added' change for id %d: %v", portID, err)
	}
	// The old bug filed the change under the plural entity_type 'ports' keyed to
	// the connector id; that misrouted row must no longer exist.
	var misfiled int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM changes WHERE entity_type='ports'`).Scan(&misfiled); err != nil {
		t.Fatal(err)
	}
	if misfiled != 0 {
		t.Fatalf("found %d misfiled plural 'ports' change rows", misfiled)
	}

	if _, _, err = a.Apply(ctx, id, domain.Snapshot{Hosts: snap.Hosts, Services: snap.Services}); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT change_kind FROM changes WHERE entity_type='port' AND entity_id=? AND change_kind='removed'`, portID).Scan(&kind); err != nil {
		t.Fatalf("no per-port 'removed' change for id %d: %v", portID, err)
	}
}

// BUG 3: an unchanged network set must not trigger a delete+reinsert every scan.
func TestApplyNetworksSkipUnchangedRewrite(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "docker", "Docker", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	snap := domain.Snapshot{
		Hosts:    []domain.Host{{Key: "host", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{{Key: "svc", HostKey: "host", Name: "app", State: "running", Networks: []domain.ServiceNetwork{{Name: "apps", IP: "172.20.0.8", Aliases: []string{"web"}}}}},
	}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	// Count row deletions on service_networks to catch needless rewrites.
	if _, err = st.DB().Exec(`CREATE TABLE net_deletes(n INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`INSERT INTO net_deletes VALUES(0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`CREATE TRIGGER net_del AFTER DELETE ON service_networks BEGIN UPDATE net_deletes SET n=n+1; END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	var deletes int
	if err = st.DB().QueryRow(`SELECT n FROM net_deletes`).Scan(&deletes); err != nil {
		t.Fatal(err)
	}
	if deletes != 0 {
		t.Fatalf("unchanged network set was rewritten (%d deletes)", deletes)
	}
	// A genuine change still rewrites and persists.
	snap.Services[0].Networks[0].IP = "172.20.0.9"
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT n FROM net_deletes`).Scan(&deletes); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("changed network set delete count=%d, want 1", deletes)
	}
	var ip string
	if err = st.DB().QueryRow(`SELECT ip_address FROM service_networks`).Scan(&ip); err != nil {
		t.Fatal(err)
	}
	if ip != "172.20.0.9" {
		t.Fatalf("changed network not persisted: ip=%q", ip)
	}
}

// BUG 4: routes.cert_id must be populated by matching the route domain against a
// cert subject/SAN, and only when the match is unique.
func TestApplyLinksRouteToMatchingCert(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateConnector(ctx, "npm", "NPM", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	expires := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	snap := domain.Snapshot{
		Routes: []domain.Route{{Key: "route:app", Domain: "App.Example", UpstreamHost: "app", UpstreamPort: 8080, TLS: true, Status: "ok"}},
		Certs:  []domain.Cert{{Key: "cert:app", Subject: "cn.example", SANs: []string{"app.example"}, NotAfter: expires, Endpoint: "app.example:443"}},
	}
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	var certID int64
	if err = st.DB().QueryRow(`SELECT id FROM certs WHERE natural_key='cert:app'`).Scan(&certID); err != nil {
		t.Fatal(err)
	}
	var linked sql.NullInt64
	if err = st.DB().QueryRow(`SELECT cert_id FROM routes WHERE natural_key='route:app'`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if !linked.Valid || linked.Int64 != certID {
		t.Fatalf("route cert_id=%v, want %d (matched via SAN, case-insensitive)", linked, certID)
	}

	// Ambiguity guard: a second active cert also covering the domain unlinks it.
	snap.Certs = append(snap.Certs, domain.Cert{Key: "cert:dup", Subject: "app.example", NotAfter: expires, Endpoint: "app.example:8443"})
	if _, _, err = a.Apply(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT cert_id FROM routes WHERE natural_key='route:app'`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked.Valid {
		t.Fatalf("ambiguous cert match must leave cert_id NULL, got %v", linked)
	}
}
