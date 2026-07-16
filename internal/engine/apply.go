package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

type Event struct {
	Type      string `json:"type"`
	ScanRunID int64  `json:"scan_run_id"`
	Changes   int    `json:"changes"`
}

type Publisher interface{ Publish(Event) }

type Applier struct {
	store *store.Store
	now   func() time.Time
	pub   Publisher
	mu    sync.Mutex
}

func New(s *store.Store, pub Publisher) *Applier { return &Applier{store: s, now: time.Now, pub: pub} }

// PurgeGone removes observations that have remained gone beyond retention.
func (a *Applier) PurgeGone(ctx context.Context, retention time.Duration) error {
	if retention <= 0 {
		return fmt.Errorf("gone retention must be positive")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := a.now().UTC().Add(-retention).Format(time.RFC3339Nano)
	tx, err := a.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"routes", "certs", "domains", "services", "hosts"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE state='gone' AND updated_at < ?`, cutoff); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *Applier) Apply(ctx context.Context, connectorID int64, snapshot domain.Snapshot) (runID int64, changeCount int, err error) {
	// SQLite WAL permits concurrent readers while this process keeps exactly one
	// snapshot writer, avoiding interleaved reconciliation for the same entities.
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	now := a.now().UTC().Format(time.RFC3339Nano)
	r, err := tx.ExecContext(ctx, `INSERT INTO scan_runs(connector_id,started_at,status) VALUES(?,?,'running')`, connectorID, now)
	if err != nil {
		return 0, 0, err
	}
	runID, err = r.LastInsertId()
	if err != nil {
		return 0, 0, err
	}

	hostIDs, n, err := applyHosts(ctx, tx, connectorID, runID, now, snapshot.Hosts)
	if err != nil {
		return 0, 0, err
	}
	changeCount += n
	serviceIDs, n, err := applyServices(ctx, tx, connectorID, runID, now, snapshot.Services, hostIDs)
	if err != nil {
		return 0, 0, err
	}
	changeCount += n
	n, err = applyPorts(ctx, tx, connectorID, runID, now, snapshot.Ports, hostIDs, serviceIDs)
	if err != nil {
		return 0, 0, err
	}
	changeCount += n
	n, err = applyRoutes(ctx, tx, connectorID, runID, now, snapshot.Routes, serviceIDs)
	if err != nil {
		return 0, 0, err
	}
	changeCount += n
	n, err = applyCerts(ctx, tx, connectorID, runID, now, snapshot.Certs)
	if err != nil {
		return 0, 0, err
	}
	changeCount += n
	n, err = applyDomains(ctx, tx, connectorID, runID, now, snapshot.Domains)
	if err != nil {
		return 0, 0, err
	}
	changeCount += n
	stats, _ := json.Marshal(map[string]int{"changes": changeCount, "hosts": len(snapshot.Hosts), "services": len(snapshot.Services)})
	if _, err = tx.ExecContext(ctx, `UPDATE scan_runs SET finished_at=?,status='success',stats=? WHERE id=?`, now, stats, runID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE connectors SET last_status='success',last_error='',updated_at=? WHERE id=?`, now, connectorID); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	if a.pub != nil {
		a.pub.Publish(Event{Type: "scan.complete", ScanRunID: runID, Changes: changeCount})
	}
	return runID, changeCount, nil
}

func applyHosts(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.Host) (map[string]int64, int, error) {
	ids := make(map[string]int64, len(items))
	seen := make(map[string]bool)
	changes := 0
	for _, h := range items {
		if h.NaturalKey() == "" || h.Name == "" {
			return nil, 0, fmt.Errorf("host natural key and name are required")
		}
		seen[h.NaturalKey()] = true
		var id int64
		var oldName, oldKind, oldAddress, oldOS, oldArch, oldState string
		err := tx.QueryRowContext(ctx, `SELECT id,name,kind,address,os,arch,state FROM hosts WHERE natural_key=?`, h.NaturalKey()).Scan(&id, &oldName, &oldKind, &oldAddress, &oldOS, &oldArch, &oldState)
		switch err {
		case sql.ErrNoRows:
			r, e := tx.ExecContext(ctx, `INSERT INTO hosts(connector_id,natural_key,name,kind,address,os,arch,notes,state,first_seen,last_seen,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,'active',?,?,?,?)`, connectorID, h.NaturalKey(), h.Name, h.Kind, h.Address, h.OS, h.Arch, h.Notes, now, now, now, now)
			if e != nil {
				return nil, 0, e
			}
			id, _ = r.LastInsertId()
			if e = addChange(ctx, tx, runID, "host", id, "added", "Host "+h.Name+" discovered", map[string]any{"natural_key": h.NaturalKey()}, now); e != nil {
				return nil, 0, e
			}
			changes++
		case nil:
			diff := fieldsDiff(map[string]string{"name": oldName, "kind": oldKind, "address": oldAddress, "os": oldOS, "arch": oldArch, "state": oldState}, map[string]string{"name": h.Name, "kind": h.Kind, "address": h.Address, "os": h.OS, "arch": h.Arch, "state": "active"})
			if _, err = tx.ExecContext(ctx, `UPDATE hosts SET connector_id=?,name=?,kind=?,address=?,os=?,arch=?,notes=?,state='active',last_seen=?,updated_at=? WHERE id=?`, connectorID, h.Name, h.Kind, h.Address, h.OS, h.Arch, h.Notes, now, now, id); err != nil {
				return nil, 0, err
			}
			if len(diff) > 0 {
				if err = addChange(ctx, tx, runID, "host", id, "modified", "Host "+h.Name+" changed", diff, now); err != nil {
					return nil, 0, err
				}
				changes++
			}
		default:
			return nil, 0, err
		}
		ids[h.NaturalKey()] = id
	}
	n, err := markGone(ctx, tx, "hosts", connectorID, runID, seen, now)
	return ids, changes + n, err
}

func applyServices(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.Service, hosts map[string]int64) (map[string]int64, int, error) {
	ids := make(map[string]int64, len(items))
	seen := make(map[string]bool)
	changes := 0
	for _, s := range items {
		if s.NaturalKey() == "" || s.Name == "" {
			return nil, 0, fmt.Errorf("service natural key and name are required")
		}
		seen[s.NaturalKey()] = true
		labels, _ := json.Marshal(s.RawLabels)
		var hostID any
		if id, ok := hosts[s.HostKey]; ok {
			hostID = id
		}
		var id int64
		var oldName, oldStack, oldImage, oldTag, oldDigest, oldState string
		err := tx.QueryRowContext(ctx, `SELECT id,name,stack,image,tag,digest,state FROM services WHERE natural_key=?`, s.NaturalKey()).Scan(&id, &oldName, &oldStack, &oldImage, &oldTag, &oldDigest, &oldState)
		if err == sql.ErrNoRows {
			r, e := tx.ExecContext(ctx, `INSERT INTO services(connector_id,host_id,name,kind,stack,image,tag,digest,state,first_seen,last_seen,restart_policy,raw_labels,natural_key,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, connectorID, hostID, s.Name, defaultString(s.Kind, "container"), s.Stack, s.Image, s.Tag, s.Digest, defaultString(s.State, "unknown"), now, now, s.RestartPolicy, string(labels), s.NaturalKey(), now, now)
			if e != nil {
				return nil, 0, e
			}
			id, _ = r.LastInsertId()
			if e = addChange(ctx, tx, runID, "service", id, "added", "Service "+s.Name+" discovered", map[string]any{"natural_key": s.NaturalKey()}, now); e != nil {
				return nil, 0, e
			}
			changes++
		} else if err == nil {
			newState := defaultString(s.State, "unknown")
			diff := fieldsDiff(map[string]string{"name": oldName, "stack": oldStack, "image": oldImage, "tag": oldTag, "digest": oldDigest, "state": oldState}, map[string]string{"name": s.Name, "stack": s.Stack, "image": s.Image, "tag": s.Tag, "digest": s.Digest, "state": newState})
			if _, err = tx.ExecContext(ctx, `UPDATE services SET connector_id=?,host_id=?,name=?,kind=?,stack=?,image=?,tag=?,digest=?,state=?,last_seen=?,restart_policy=?,raw_labels=?,updated_at=? WHERE id=?`, connectorID, hostID, s.Name, defaultString(s.Kind, "container"), s.Stack, s.Image, s.Tag, s.Digest, newState, now, s.RestartPolicy, string(labels), now, id); err != nil {
				return nil, 0, err
			}
			if len(diff) > 0 {
				if err = addChange(ctx, tx, runID, "service", id, "modified", "Service "+s.Name+" changed", diff, now); err != nil {
					return nil, 0, err
				}
				changes++
			}
		} else {
			return nil, 0, err
		}
		ids[s.NaturalKey()] = id
	}
	n, err := markGone(ctx, tx, "services", connectorID, runID, seen, now)
	return ids, changes + n, err
}

func applyPorts(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.Port, hosts, services map[string]int64) (int, error) {
	old := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT natural_key FROM ports WHERE connector_id=? ORDER BY natural_key`, connectorID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			return 0, err
		}
		old = append(old, k)
	}
	rows.Close()
	newKeys := make([]string, 0, len(items))
	for _, p := range items {
		newKeys = append(newKeys, p.NaturalKey())
	}
	sort.Strings(newKeys)
	if stringSliceEqual(old, newKeys) {
		return 0, nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM ports WHERE connector_id=?`, connectorID); err != nil {
		return 0, err
	}
	for _, p := range items {
		sid, ok := services[p.ServiceKey]
		if !ok {
			return 0, fmt.Errorf("port references unknown service %q", p.ServiceKey)
		}
		var hid any
		if id, ok := hosts[p.HostKey]; ok {
			hid = id
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ports(connector_id,service_id,host_id,number,protocol,published,host_ip,container_port,source,natural_key) VALUES(?,?,?,?,?,?,?,?,?,?)`, connectorID, sid, hid, p.Number, defaultString(p.Protocol, "tcp"), p.Published, p.HostIP, p.ContainerPort, p.Source, p.NaturalKey()); err != nil {
			return 0, err
		}
	}
	diff := map[string]any{"before": old, "after": newKeys}
	if err = addChange(ctx, tx, runID, "ports", connectorID, "modified", "Published ports changed", diff, now); err != nil {
		return 0, err
	}
	return 1, nil
}

func applyRoutes(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.Route, services map[string]int64) (int, error) {
	seen := map[string]bool{}
	changes := 0
	for _, r := range items {
		if r.NaturalKey() == "" {
			return 0, fmt.Errorf("route natural key is required")
		}
		seen[r.NaturalKey()] = true
		var id int64
		var oldHost, oldStatus, oldState string
		var oldPort int
		err := tx.QueryRowContext(ctx, `SELECT id,upstream_host,COALESCE(upstream_port,0),status,state FROM routes WHERE natural_key=?`, r.NaturalKey()).Scan(&id, &oldHost, &oldPort, &oldStatus, &oldState)
		var resolved any
		if x, ok := services[r.ResolvedServiceKey]; ok {
			resolved = x
		}
		confidence := defaultString(r.ResolveConfidence, "none")
		status := defaultString(r.Status, "unknown")
		if err == sql.ErrNoRows {
			res, e := tx.ExecContext(ctx, `INSERT INTO routes(connector_id,proxy_id,domain,path_prefix,upstream_host,upstream_port,resolved_service_id,resolve_confidence,tls,status,natural_key,state,first_seen,last_seen,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'active',?,?,?,?)`, connectorID, r.ProxyID, r.Domain, r.PathPrefix, r.UpstreamHost, r.UpstreamPort, resolved, confidence, r.TLS, status, r.NaturalKey(), now, now, now, now)
			if e != nil {
				return 0, e
			}
			id, _ = res.LastInsertId()
			if e = addChange(ctx, tx, runID, "route", id, "added", "Route "+r.Domain+" discovered", map[string]any{"natural_key": r.NaturalKey()}, now); e != nil {
				return 0, e
			}
			changes++
		} else if err == nil {
			diff := fieldsDiff(map[string]string{"upstream": fmt.Sprintf("%s:%d", oldHost, oldPort), "status": oldStatus, "state": oldState}, map[string]string{"upstream": fmt.Sprintf("%s:%d", r.UpstreamHost, r.UpstreamPort), "status": status, "state": "active"})
			if _, err = tx.ExecContext(ctx, `UPDATE routes SET connector_id=?,proxy_id=?,domain=?,path_prefix=?,upstream_host=?,upstream_port=?,resolved_service_id=?,resolve_confidence=?,tls=?,status=?,state='active',last_seen=?,updated_at=? WHERE id=?`, connectorID, r.ProxyID, r.Domain, r.PathPrefix, r.UpstreamHost, r.UpstreamPort, resolved, confidence, r.TLS, status, now, now, id); err != nil {
				return 0, err
			}
			if len(diff) > 0 {
				if err = addChange(ctx, tx, runID, "route", id, "modified", "Route "+r.Domain+" changed", diff, now); err != nil {
					return 0, err
				}
				changes++
			}
		} else {
			return 0, err
		}
	}
	n, err := markGone(ctx, tx, "routes", connectorID, runID, seen, now)
	return changes + n, err
}

func applyCerts(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.Cert) (int, error) {
	seen := map[string]bool{}
	changes := 0
	for _, c := range items {
		key := defaultString(c.NaturalKey(), c.Endpoint)
		if key == "" {
			return 0, fmt.Errorf("cert natural key or endpoint required")
		}
		seen[key] = true
		sans, _ := json.Marshal(c.SANs)
		notAfter := ""
		if !c.NotAfter.IsZero() {
			notAfter = c.NotAfter.UTC().Format(time.RFC3339Nano)
		}
		var id int64
		var oldExpiry, oldState string
		err := tx.QueryRowContext(ctx, `SELECT id,COALESCE(not_after,''),state FROM certs WHERE natural_key=?`, key).Scan(&id, &oldExpiry, &oldState)
		if err == sql.ErrNoRows {
			r, e := tx.ExecContext(ctx, `INSERT INTO certs(connector_id,natural_key,subject,sans,issuer,not_after,chain_valid,source,endpoint,state,first_seen,last_seen,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'active',?,?,?,?)`, connectorID, key, c.Subject, string(sans), c.Issuer, nullableString(notAfter), c.ChainValid, c.Source, c.Endpoint, now, now, now, now)
			if e != nil {
				return 0, e
			}
			id, _ = r.LastInsertId()
			if e = addChange(ctx, tx, runID, "cert", id, "added", "Certificate "+c.Subject+" discovered", map[string]any{"not_after": notAfter}, now); e != nil {
				return 0, e
			}
			changes++
		} else if err == nil {
			diff := fieldsDiff(map[string]string{"not_after": oldExpiry, "state": oldState}, map[string]string{"not_after": notAfter, "state": "active"})
			if _, err = tx.ExecContext(ctx, `UPDATE certs SET connector_id=?,subject=?,sans=?,issuer=?,not_after=?,chain_valid=?,source=?,endpoint=?,state='active',last_seen=?,updated_at=? WHERE id=?`, connectorID, c.Subject, string(sans), c.Issuer, nullableString(notAfter), c.ChainValid, c.Source, c.Endpoint, now, now, id); err != nil {
				return 0, err
			}
			if len(diff) > 0 {
				if err = addChange(ctx, tx, runID, "cert", id, "modified", "Certificate "+c.Subject+" changed", diff, now); err != nil {
					return 0, err
				}
				changes++
			}
		} else {
			return 0, err
		}
	}
	n, err := markGone(ctx, tx, "certs", connectorID, runID, seen, now)
	return changes + n, err
}

func applyDomains(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.Domain) (int, error) {
	seen := map[string]bool{}
	changes := 0
	for _, d := range items {
		key := defaultString(d.NaturalKey(), d.Name)
		if key == "" {
			return 0, fmt.Errorf("domain name required")
		}
		seen[key] = true
		ns, _ := json.Marshal(d.Nameservers)
		expiry := ""
		if d.ExpiresAt != nil {
			expiry = d.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		var checked any
		if d.LastChecked != nil {
			checked = d.LastChecked.UTC().Format(time.RFC3339Nano)
		}
		var id int64
		var oldExpiry, oldState string
		err := tx.QueryRowContext(ctx, `SELECT id,COALESCE(expires_at,''),state FROM domains WHERE natural_key=?`, key).Scan(&id, &oldExpiry, &oldState)
		if err == sql.ErrNoRows {
			r, e := tx.ExecContext(ctx, `INSERT INTO domains(connector_id,natural_key,name,registrar,expires_at,nameservers,source,last_checked,state,first_seen,last_seen,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,'active',?,?,?,?)`, connectorID, key, d.Name, d.Registrar, nullableString(expiry), string(ns), d.Source, checked, now, now, now, now)
			if e != nil {
				return 0, e
			}
			id, _ = r.LastInsertId()
			if e = addChange(ctx, tx, runID, "domain", id, "added", "Domain "+d.Name+" discovered", map[string]any{"expires_at": expiry}, now); e != nil {
				return 0, e
			}
			changes++
		} else if err == nil {
			diff := fieldsDiff(map[string]string{"expires_at": oldExpiry, "state": oldState}, map[string]string{"expires_at": expiry, "state": "active"})
			if _, err = tx.ExecContext(ctx, `UPDATE domains SET connector_id=?,name=?,registrar=?,expires_at=?,nameservers=?,source=?,last_checked=?,state='active',last_seen=?,updated_at=? WHERE id=?`, connectorID, d.Name, d.Registrar, nullableString(expiry), string(ns), d.Source, checked, now, now, id); err != nil {
				return 0, err
			}
			if len(diff) > 0 {
				if err = addChange(ctx, tx, runID, "domain", id, "modified", "Domain "+d.Name+" changed", diff, now); err != nil {
					return 0, err
				}
				changes++
			}
		} else {
			return 0, err
		}
	}
	n, err := markGone(ctx, tx, "domains", connectorID, runID, seen, now)
	return changes + n, err
}

func markGone(ctx context.Context, tx *sql.Tx, table string, connectorID, runID int64, seen map[string]bool, now string) (int, error) {
	allowed := map[string]bool{"hosts": true, "services": true, "routes": true, "certs": true, "domains": true}
	if !allowed[table] {
		return 0, fmt.Errorf("unsupported entity table")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,natural_key FROM `+table+` WHERE connector_id=? AND state!='gone'`, connectorID)
	if err != nil {
		return 0, err
	}
	type item struct {
		id  int64
		key string
	}
	var absent []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.key); err != nil {
			rows.Close()
			return 0, err
		}
		if !seen[x.key] {
			absent = append(absent, x)
		}
	}
	rows.Close()
	for _, x := range absent {
		if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET state='gone',updated_at=? WHERE id=?`, now, x.id); err != nil {
			return 0, err
		}
		if err = addChange(ctx, tx, runID, strings.TrimSuffix(table, "s"), x.id, "removed", strings.Title(strings.TrimSuffix(table, "s"))+" no longer observed", map[string]any{"state": map[string]string{"before": "active", "after": "gone"}}, now); err != nil {
			return 0, err
		}
	}
	return len(absent), nil
}

func addChange(ctx context.Context, tx *sql.Tx, runID int64, entity string, id int64, kind, summary string, diff any, now string) error {
	b, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO changes(scan_run_id,entity_type,entity_id,change_kind,summary,diff,created_at) VALUES(?,?,?,?,?,?,?)`, runID, entity, id, kind, summary, string(b), now)
	return err
}
func fieldsDiff(before, after map[string]string) map[string]any {
	d := map[string]any{}
	for k, v := range after {
		if before[k] != v {
			d[k] = map[string]string{"before": before[k], "after": v}
		}
	}
	return d
}
func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
