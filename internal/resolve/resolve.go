package resolve

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/HarshShah0203/homedex/internal/domain"
)

type Inventory struct {
	Hosts    []domain.Host
	Services []domain.Service
	Ports    []domain.Port
}

// Routes applies deterministic route-to-container resolution. It never probes
// or mutates connected infrastructure.
func Routes(routes []domain.Route, inv Inventory) []domain.Route {
	out := append([]domain.Route(nil), routes...)
	for i := range out {
		resolve(&out[i], inv)
	}
	return out
}
func resolve(route *domain.Route, inv Inventory) {
	host := normalize(route.UpstreamHost)
	// Network IP match is the strongest and least ambiguous path.
	for _, svc := range inv.Services {
		for _, n := range svc.Networks {
			if n.IP != "" && host == normalize(n.IP) && listens(svc.Key, route.UpstreamPort, inv.Ports) {
				matched(route, svc.Key, "high")
				return
			}
		}
	}
	// Docker name, Compose service, or a network alias.
	var named []string
	for _, svc := range inv.Services {
		if nameMatch(host, svc) && listens(svc.Key, route.UpstreamPort, inv.Ports) {
			named = append(named, svc.Key)
		}
	}
	named = dedupe(named)
	if len(named) == 1 {
		matched(route, named[0], "high")
		return
	}
	// Published host port. localhost and Docker gateway names are accepted when
	// exactly one published mapping matches, avoiding false high-confidence links.
	local := host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "host.docker.internal" || host == "gateway.docker.internal"
	hostKeys := map[string]bool{}
	for _, h := range inv.Hosts {
		if normalize(h.Address) == host {
			hostKeys[h.Key] = true
		}
	}
	var candidates []string
	for _, p := range inv.Ports {
		localHostMatch := local && (route.ProxyHostKey == "" || route.ProxyHostKey == p.HostKey)
		if p.Published && p.Number == route.UpstreamPort && (localHostMatch || hostKeys[p.HostKey]) {
			candidates = append(candidates, p.ServiceKey)
		}
	}
	candidates = dedupe(candidates)
	if len(candidates) == 1 {
		matched(route, candidates[0], "medium")
		return
	}
	route.ResolvedServiceKey = ""
	route.ResolveConfidence = "none"
	route.Status = "broken"
}
func matched(r *domain.Route, key, confidence string) {
	r.ResolvedServiceKey = key
	r.ResolveConfidence = confidence
	r.Status = "ok"
}
func normalize(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}
func nameMatch(host string, s domain.Service) bool {
	if host == normalize(s.Name) {
		return true
	}
	for _, n := range s.Networks {
		for _, a := range n.Aliases {
			if host == normalize(a) {
				return true
			}
		}
	}
	return false
}
func listens(key string, port int, ports []domain.Port) bool {
	known := false
	for _, p := range ports {
		if p.ServiceKey != key {
			continue
		}
		known = true
		if p.ContainerPort == port || (!p.Published && p.Number == port) {
			return true
		}
	}
	return !known
}
func dedupe(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// LoadInventory returns active Docker observations needed by Routes.
func LoadInventory(ctx context.Context, db *sql.DB) (Inventory, error) {
	var inv Inventory
	rows, e := db.QueryContext(ctx, `SELECT natural_key,name,address FROM hosts WHERE state='active'`)
	if e != nil {
		return inv, e
	}
	for rows.Next() {
		var h domain.Host
		if e = rows.Scan(&h.Key, &h.Name, &h.Address); e != nil {
			rows.Close()
			return inv, e
		}
		inv.Hosts = append(inv.Hosts, h)
	}
	rows.Close()
	rows, e = db.QueryContext(ctx, `SELECT id,natural_key,name FROM services WHERE state!='gone'`)
	if e != nil {
		return inv, e
	}
	ids := map[int64]int{}
	for rows.Next() {
		var id int64
		var s domain.Service
		if e = rows.Scan(&id, &s.Key, &s.Name); e != nil {
			rows.Close()
			return inv, e
		}
		ids[id] = len(inv.Services)
		inv.Services = append(inv.Services, s)
	}
	rows.Close()
	rows, e = db.QueryContext(ctx, `SELECT service_id,network_name,ip_address,aliases FROM service_networks`)
	if e != nil {
		return inv, e
	}
	for rows.Next() {
		var id int64
		var n domain.ServiceNetwork
		var aliases string
		if e = rows.Scan(&id, &n.Name, &n.IP, &aliases); e != nil {
			rows.Close()
			return inv, e
		}
		_ = json.Unmarshal([]byte(aliases), &n.Aliases)
		if i, ok := ids[id]; ok {
			inv.Services[i].Networks = append(inv.Services[i].Networks, n)
		}
	}
	rows.Close()
	rows, e = db.QueryContext(ctx, `SELECT s.natural_key,h.natural_key,p.number,p.container_port,p.protocol,p.published,p.host_ip FROM ports p JOIN services s ON s.id=p.service_id LEFT JOIN hosts h ON h.id=p.host_id`)
	if e != nil {
		return inv, e
	}
	for rows.Next() {
		var p domain.Port
		var hk sql.NullString
		if e = rows.Scan(&p.ServiceKey, &hk, &p.Number, &p.ContainerPort, &p.Protocol, &p.Published, &p.HostIP); e != nil {
			rows.Close()
			return inv, e
		}
		p.HostKey = hk.String
		inv.Ports = append(inv.Ports, p)
	}
	return inv, rows.Close()
}

func ReconcileStore(ctx context.Context, db *sql.DB) error {
	inv, err := LoadInventory(ctx, db)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT r.id,r.natural_key,r.domain,r.path_prefix,r.upstream_host,COALESCE(r.upstream_port,0),r.tls,r.status,COALESCE(h.natural_key,'') FROM routes r LEFT JOIN proxies p ON p.id=r.proxy_id LEFT JOIN hosts h ON h.id=p.host_id WHERE r.state='active'`)
	if err != nil {
		return err
	}
	type item struct {
		id int64
		r  domain.Route
	}
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.r.Key, &x.r.Domain, &x.r.PathPrefix, &x.r.UpstreamHost, &x.r.UpstreamPort, &x.r.TLS, &x.r.Status, &x.r.ProxyHostKey); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	for _, x := range items {
		r := Routes([]domain.Route{x.r}, inv)[0]
		var sid any
		if r.ResolvedServiceKey != "" {
			var id int64
			if db.QueryRowContext(ctx, `SELECT id FROM services WHERE natural_key=?`, r.ResolvedServiceKey).Scan(&id) == nil {
				sid = id
			}
		}
		if _, err = db.ExecContext(ctx, `UPDATE routes SET resolved_service_id=?,resolve_confidence=?,status=? WHERE id=?`, sid, r.ResolveConfidence, r.Status, x.id); err != nil {
			return err
		}
	}
	return nil
}
