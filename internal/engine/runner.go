package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/imageref"
	"github.com/HarshShah0203/homedex/internal/resolve"
	"github.com/HarshShah0203/homedex/internal/store"
)

type Runner struct {
	Store    *store.Store
	Configs  *store.ConnectorConfigs
	Registry *connectors.Registry
	Applier  *Applier
	Timeout  time.Duration
	mu       sync.Mutex
	running  map[int64]bool
}

func NewRunner(s *store.Store, c *store.ConnectorConfigs, r *connectors.Registry, a *Applier) *Runner {
	return &Runner{Store: s, Configs: c, Registry: r, Applier: a, Timeout: 60 * time.Second, running: map[int64]bool{}}
}
func (r *Runner) Test(ctx context.Context, id int64) error {
	rec, cfg, c, e := r.load(ctx, id)
	if e != nil {
		return e
	}
	cfg = r.AddTargets(ctx, rec.Kind, cfg)
	tctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	return c.Validate(tctx, cfg)
}
func (r *Runner) Scan(ctx context.Context, id int64) (int64, int, error) {
	r.mu.Lock()
	if r.running[id] {
		r.mu.Unlock()
		return 0, 0, fmt.Errorf("scan already running")
	}
	r.running[id] = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.running, id); r.mu.Unlock() }()
	rec, cfg, c, e := r.load(ctx, id)
	if e != nil {
		return 0, 0, e
	}
	if !rec.Enabled {
		return 0, 0, fmt.Errorf("connector is disabled")
	}
	cfg = r.AddTargets(ctx, rec.Kind, cfg)
	tctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	started := time.Now().UTC()
	_, _ = r.Store.DB().ExecContext(ctx, `UPDATE connectors SET last_status='running',last_error='',updated_at=? WHERE id=?`, started.Format(time.RFC3339Nano), id)
	r.publish(Event{Type: "scan.started", ConnectorID: id, Phase: "connect", Message: "Connecting to read-only source", Progress: 5})
	snap, e := c.Scan(tctx, cfg)
	if e != nil {
		r.failed(ctx, id, started, e)
		return 0, 0, e
	}
	stats := map[string]int{"hosts": len(snap.Hosts), "services": len(snap.Services), "ports": len(snap.Ports), "routes": len(snap.Routes), "certs": len(snap.Certs), "domains": len(snap.Domains), "images": len(snap.ImageUpdates)}
	r.publish(Event{Type: "scan.progress", ConnectorID: id, Phase: "discover", Message: "Discovery complete", Progress: 60, Stats: stats})
	if proxyKinds[rec.Kind] {
		endpoint, pe := proxyEndpoint(c, cfg)
		var proxyID int64
		if pe == nil {
			proxyID, pe = r.ensureProxy(ctx, id, rec.Kind, endpoint)
		}
		if pe != nil {
			r.failed(ctx, id, started, pe)
			return 0, 0, pe
		}
		proxyHost, proxyNetworks, scopeErr := resolve.LoadProxyScope(ctx, r.Store.DB(), proxyID)
		if scopeErr != nil {
			r.failed(ctx, id, started, scopeErr)
			return 0, 0, scopeErr
		}
		for i := range snap.Routes {
			snap.Routes[i].ProxyID = &proxyID
			snap.Routes[i].ProxyHostConnectorID = proxyHost.ConnectorID
			snap.Routes[i].ProxyHostKey = proxyHost.Key
			snap.Routes[i].ProxyNetworks = proxyNetworks
		}
	}
	if len(snap.Routes) > 0 {
		r.publish(Event{Type: "scan.progress", ConnectorID: id, Phase: "resolve", Message: "Resolving routes to services", Progress: 75, Stats: stats})
		inv, ie := resolve.LoadInventory(ctx, r.Store.DB())
		if ie != nil {
			r.failed(ctx, id, started, ie)
			return 0, 0, ie
		}
		snap.Routes = resolve.Routes(snap.Routes, inv)
	}
	r.publish(Event{Type: "scan.progress", ConnectorID: id, Phase: "persist", Message: "Saving inventory and computing changes", Progress: 85, Stats: stats})
	run, changes, e := r.Applier.Apply(ctx, id, snap)
	if e != nil {
		r.failed(ctx, id, started, e)
		return 0, 0, e
	}
	if e = r.Applier.ReconcileRoutes(ctx); e != nil {
		r.markRunFailed(ctx, id, run, e)
		return run, changes, fmt.Errorf("resolve routes: %w", e)
	}
	return run, changes, nil
}

// AddTargets adds what the engine supplies to a source's config before it is
// tested or scanned: route domains for the TLS probe and RDAP, and the
// deployed image references for image update checks. A source being added is
// tested with the same targets it will be scanned with once saved.
func (r *Runner) AddTargets(ctx context.Context, kind string, cfg connectors.Config) connectors.Config {
	if cfg == nil {
		cfg = connectors.Config{}
	}
	r.addRouteTargets(ctx, kind, cfg)
	r.addImageTargets(ctx, kind, cfg)
	return cfg
}

func (r *Runner) addRouteTargets(ctx context.Context, kind string, cfg connectors.Config) {
	if kind != "tlsprobe" && kind != "rdap" {
		return
	}
	// Wildcard route domains ("*.example.com", SWAG's "radarr.*") name no single
	// host a TLS probe can dial or RDAP can look up.
	rows, e := r.Store.DB().QueryContext(ctx, `SELECT DISTINCT domain FROM routes WHERE state='active' AND domain!='' AND instr(domain,'*')=0 AND (?='rdap' OR tls=1)`, kind)
	if e != nil {
		return
	}
	defer rows.Close()
	key := "domains"
	if kind == "tlsprobe" {
		key = "targets"
	}
	var values []string
	if raw := cfg[key]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			if kind == "tlsprobe" {
				d = d + ":443"
			}
			if seen[d] {
				continue
			}
			values = append(values, d)
			seen[d] = true
		}
	}
	if b, e := json.Marshal(values); e == nil {
		cfg[key] = b
	}
}

// addImageTargets hands the registry connector the distinct references the
// containers still in the inventory run. It replaces any "images" in the
// stored config: the source checks what is deployed, not an arbitrary list.
// Only Docker-source containers count (imageCheckedServices): containers an
// SSH host lists carry no registry digests, so a lookup could never be
// compared. References come out sorted, so a scan's lookups are deterministic.
func (r *Runner) addImageTargets(ctx context.Context, kind string, cfg connectors.Config) {
	if kind != "registry" {
		return
	}
	rows, e := r.Store.DB().QueryContext(ctx, `SELECT DISTINCT s.image,s.tag FROM services s WHERE `+imageCheckedServices, imageref.SourceKind)
	if e != nil {
		return
	}
	defer rows.Close()
	seen := map[string]bool{}
	refs := []string{}
	for rows.Next() {
		var image, tag string
		if rows.Scan(&image, &tag) != nil {
			continue
		}
		if ref := imageref.Join(image, tag); ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	if b, e := json.Marshal(refs); e == nil {
		cfg["images"] = b
	}
}

// proxyKinds are the connectors whose routes belong to a proxies row.
var proxyKinds = map[string]bool{"traefik": true, "caddy": true, "npm": true, "nginx": true}

// proxyEndpoint is the value stored in proxies.endpoint: the connector's own
// answer when it has one (file-based nginx has no url), else its "url" config.
func proxyEndpoint(c connectors.Connector, cfg connectors.Config) (string, error) {
	if p, ok := c.(connectors.ProxyEndpointer); ok {
		return p.ProxyEndpoint(cfg)
	}
	var endpoint string
	if raw := cfg["url"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &endpoint)
	}
	if endpoint == "" {
		return "", fmt.Errorf("proxy endpoint URL is required")
	}
	return endpoint, nil
}

func (r *Runner) ensureProxy(ctx context.Context, connectorID int64, kind, endpoint string) (int64, error) {
	var hostID any
	if u, e := url.Parse(endpoint); e == nil && u.Hostname() != "" {
		hosts, err := resolve.LoadHosts(ctx, r.Store.DB())
		if err != nil {
			return 0, err
		}
		// A tailnet name or IP links the proxy to the machine behind the device.
		if ids := resolve.MachineHostIDs(hosts, u.Hostname()); len(ids) == 1 {
			hostID = ids[0]
		}
	}
	var id int64
	e := r.Store.DB().QueryRowContext(ctx, `SELECT id FROM proxies WHERE connector_id=?`, connectorID).Scan(&id)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if e == sql.ErrNoRows {
		res, ie := r.Store.DB().ExecContext(ctx, `INSERT INTO proxies(kind,host_id,endpoint,connector_id,last_scan) VALUES(?,?,?,?,?)`, kind, hostID, endpoint, connectorID, now)
		if ie != nil {
			return 0, ie
		}
		id, _ = res.LastInsertId()
		return id, nil
	}
	if e != nil {
		return 0, e
	}
	_, e = r.Store.DB().ExecContext(ctx, `UPDATE proxies SET kind=?,host_id=?,endpoint=?,last_scan=? WHERE id=?`, kind, hostID, endpoint, now, id)
	return id, e
}
func (r *Runner) load(ctx context.Context, id int64) (store.ConnectorRecord, connectors.Config, connectors.Connector, error) {
	rec, e := r.Configs.Record(ctx, id)
	if e != nil {
		return rec, nil, nil, e
	}
	c, ok := r.Registry.Get(rec.Kind)
	if !ok {
		return rec, nil, nil, fmt.Errorf("connector kind %q is not registered", rec.Kind)
	}
	var cfg connectors.Config
	if e = r.Configs.Load(ctx, id, &cfg); e != nil {
		return rec, nil, nil, e
	}
	return rec, cfg, c, nil
}
func (r *Runner) failed(ctx context.Context, id int64, started time.Time, scanErr error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	stats, _ := json.Marshal(map[string]int{})
	res, _ := r.Store.DB().ExecContext(ctx, `INSERT INTO scan_runs(connector_id,started_at,finished_at,status,error,stats) VALUES(?,?,?,?,?,?)`, id, started.Format(time.RFC3339Nano), now, "error", scanErr.Error(), string(stats))
	var runID int64
	if res != nil {
		runID, _ = res.LastInsertId()
	}
	_, _ = r.Store.DB().ExecContext(ctx, `UPDATE connectors SET last_status='error',last_error=?,updated_at=? WHERE id=?`, scanErr.Error(), now, id)
	r.publish(Event{Type: "scan.failed", ConnectorID: id, ScanRunID: runID, Error: scanErr.Error()})
}
func (r *Runner) markRunFailed(ctx context.Context, connectorID, runID int64, scanErr error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = r.Store.DB().ExecContext(ctx, `UPDATE scan_runs SET status='error',error=?,finished_at=? WHERE id=?`, scanErr.Error(), now, runID)
	_, _ = r.Store.DB().ExecContext(ctx, `UPDATE connectors SET last_status='error',last_error=?,updated_at=? WHERE id=?`, scanErr.Error(), now, connectorID)
	r.publish(Event{Type: "scan.failed", ConnectorID: connectorID, ScanRunID: runID, Error: scanErr.Error()})
}
func (r *Runner) publish(e Event) {
	if r.Applier != nil && r.Applier.pub != nil {
		r.Applier.pub.Publish(e)
	}
}
func (r *Runner) timeout() time.Duration {
	if r.Timeout <= 0 {
		return 60 * time.Second
	}
	return r.Timeout
}

type Scheduler struct {
	runner              *Runner
	interval            time.Duration
	now                 func() time.Time
	goneRetention       time.Duration
	maintenanceInterval time.Duration
	lastMaintenance     time.Time
}

const DefaultGoneRetention = 30 * 24 * time.Hour

func NewScheduler(r *Runner) *Scheduler {
	return &Scheduler{runner: r, interval: 30 * time.Second, now: time.Now, goneRetention: DefaultGoneRetention, maintenanceInterval: 24 * time.Hour}
}

func (s *Scheduler) SetGoneRetention(retention time.Duration) error {
	if retention <= 0 {
		return fmt.Errorf("gone retention must be positive")
	}
	s.goneRetention = retention
	return nil
}
func (s *Scheduler) Run(ctx context.Context) {
	s.tick(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}
func (s *Scheduler) tick(ctx context.Context) {
	s.maintain(ctx)
	if s.runner == nil || s.runner.Configs == nil {
		return
	}
	records, e := s.runner.Configs.List(ctx)
	if e != nil {
		return
	}
	for _, r := range records {
		if !r.Enabled || !s.due(ctx, r) {
			continue
		}
		go func(id int64) { _, _, _ = s.runner.Scan(ctx, id) }(r.ID)
	}
}

func (s *Scheduler) maintain(ctx context.Context) {
	if s.runner == nil || s.runner.Applier == nil {
		return
	}
	now := s.now()
	if !s.lastMaintenance.IsZero() && now.Before(s.lastMaintenance.Add(s.maintenanceInterval)) {
		return
	}
	if err := s.runner.Applier.PurgeGone(ctx, s.goneRetention); err == nil {
		s.lastMaintenance = now
	}
}
func (s *Scheduler) due(ctx context.Context, r store.ConnectorRecord) bool {
	var last sql.NullString
	e := s.runner.Store.DB().QueryRowContext(ctx, `SELECT MAX(started_at) FROM scan_runs WHERE connector_id=?`, r.ID).Scan(&last)
	if e != nil || !last.Valid {
		return true
	}
	t, e := time.Parse(time.RFC3339Nano, last.String)
	if e != nil {
		t, _ = time.Parse("2006-01-02 15:04:05", last.String)
	}
	jitter := time.Duration((r.ID%21)-10) * time.Duration(r.ScheduleMinutes) * time.Minute / 100
	return !s.now().Before(t.Add(time.Duration(r.ScheduleMinutes)*time.Minute + jitter))
}
