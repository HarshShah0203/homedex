package resolve

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/HarshShah0203/homedex/internal/domain"
)

type Inventory struct {
	Hosts    []Host
	Services []Service
	Ports    []Port
}

type EntityRef struct {
	ConnectorID int64
	Key         string
}

type Host struct {
	Ref     EntityRef
	Name    string
	Address string
}

type Service struct {
	Ref      EntityRef
	HostRef  EntityRef
	Name     string
	Networks []domain.ServiceNetwork
}

type Port struct {
	ServiceRef    EntityRef
	HostRef       EntityRef
	Number        int
	ContainerPort int
	Published     bool
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
	proxyHost := EntityRef{ConnectorID: route.ProxyHostConnectorID, Key: route.ProxyHostKey}
	// Network IP match is strongest only after all candidates are collected and
	// scoped to the proxy's Docker host/network identity where available.
	var ipCandidates []candidate
	for _, svc := range inv.Services {
		for _, n := range svc.Networks {
			if n.IP != "" && host == normalize(n.IP) && listens(svc.Ref, route.UpstreamPort, inv.Ports) {
				ipCandidates = append(ipCandidates, candidate{service: svc, network: n.Name})
			}
		}
	}
	if resolved, ok := uniqueCandidate(scopeCandidates(ipCandidates, proxyHost, route.ProxyNetworks)); ok {
		matched(route, resolved.service.Ref, "high")
		return
	}
	if len(ipCandidates) > 0 {
		broken(route)
		return
	}
	// Docker name, Compose service, or a network alias.
	var named []candidate
	for _, svc := range inv.Services {
		if nameMatch(host, svc) && listens(svc.Ref, route.UpstreamPort, inv.Ports) {
			for _, network := range matchingNetworks(host, svc) {
				named = append(named, candidate{service: svc, network: network})
			}
		}
	}
	if resolved, ok := uniqueCandidate(scopeCandidates(named, proxyHost, route.ProxyNetworks)); ok {
		matched(route, resolved.service.Ref, "high")
		return
	}
	if len(named) > 0 {
		broken(route)
		return
	}
	// Published host port. localhost and Docker gateway names are accepted when
	// exactly one published mapping matches, avoiding false high-confidence links.
	local := host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "host.docker.internal" || host == "gateway.docker.internal"
	hostRefs := map[EntityRef]bool{}
	for _, h := range inv.Hosts {
		if normalize(h.Address) == host {
			hostRefs[h.Ref] = true
		}
	}
	var candidates []EntityRef
	for _, p := range inv.Ports {
		localHostMatch := local && (!validRef(proxyHost) || proxyHost == p.HostRef)
		if p.Published && p.Number == route.UpstreamPort && (localHostMatch || hostRefs[p.HostRef]) {
			candidates = append(candidates, p.ServiceRef)
		}
	}
	candidates = dedupeRefs(candidates)
	if len(candidates) == 1 {
		matched(route, candidates[0], "medium")
		return
	}
	broken(route)
}

type candidate struct {
	service Service
	network string
}

func scopeCandidates(candidates []candidate, proxyHost EntityRef, proxyNetworks []string) []candidate {
	if validRef(proxyHost) {
		candidates = filterCandidates(candidates, func(c candidate) bool { return c.service.HostRef == proxyHost })
	}
	if len(proxyNetworks) > 0 {
		networks := make(map[string]bool, len(proxyNetworks))
		for _, network := range proxyNetworks {
			networks[normalize(network)] = true
		}
		candidates = filterCandidates(candidates, func(c candidate) bool { return networks[normalize(c.network)] })
	}
	return candidates
}

func filterCandidates(candidates []candidate, keep func(candidate) bool) []candidate {
	filtered := make([]candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if keep(candidate) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func uniqueCandidate(candidates []candidate) (candidate, bool) {
	byService := make(map[EntityRef]candidate, len(candidates))
	for _, candidate := range candidates {
		byService[candidate.service.Ref] = candidate
	}
	if len(byService) != 1 {
		return candidate{}, false
	}
	for _, candidate := range byService {
		return candidate, true
	}
	return candidate{}, false
}

func broken(route *domain.Route) {
	route.ResolvedServiceKey = ""
	route.ResolvedServiceConnectorID = 0
	route.ResolveConfidence = "none"
	route.Status = "broken"
}
func matched(r *domain.Route, ref EntityRef, confidence string) {
	r.ResolvedServiceKey = ref.Key
	r.ResolvedServiceConnectorID = ref.ConnectorID
	r.ResolveConfidence = confidence
	r.Status = "ok"
}
func normalize(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}
func nameMatch(host string, s Service) bool {
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
func matchingNetworks(host string, s Service) []string {
	var networks []string
	if host == normalize(s.Name) && len(s.Networks) == 0 {
		return []string{""}
	}
	for _, network := range s.Networks {
		if host == normalize(s.Name) {
			networks = append(networks, network.Name)
			continue
		}
		for _, alias := range network.Aliases {
			if host == normalize(alias) {
				networks = append(networks, network.Name)
				break
			}
		}
	}
	return networks
}
func listens(ref EntityRef, port int, ports []Port) bool {
	known := false
	for _, p := range ports {
		if p.ServiceRef != ref {
			continue
		}
		known = true
		if p.ContainerPort == port || (!p.Published && p.Number == port) {
			return true
		}
	}
	return !known
}
func validRef(ref EntityRef) bool { return ref.ConnectorID != 0 || ref.Key != "" }
func dedupeRefs(in []EntityRef) []EntityRef {
	m := map[EntityRef]bool{}
	for _, ref := range in {
		m[ref] = true
	}
	out := make([]EntityRef, 0, len(m))
	for ref := range m {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ConnectorID == out[j].ConnectorID {
			return out[i].Key < out[j].Key
		}
		return out[i].ConnectorID < out[j].ConnectorID
	})
	return out
}

// LoadInventory returns active Docker observations needed by Routes.
func LoadInventory(ctx context.Context, db *sql.DB) (Inventory, error) {
	var inv Inventory
	rows, e := db.QueryContext(ctx, `SELECT COALESCE(connector_id,0),natural_key,name,address FROM hosts WHERE state='active'`)
	if e != nil {
		return inv, e
	}
	for rows.Next() {
		var h Host
		if e = rows.Scan(&h.Ref.ConnectorID, &h.Ref.Key, &h.Name, &h.Address); e != nil {
			rows.Close()
			return inv, e
		}
		inv.Hosts = append(inv.Hosts, h)
	}
	rows.Close()
	rows, e = db.QueryContext(ctx, `SELECT s.id,COALESCE(s.connector_id,0),s.natural_key,s.name,COALESCE(h.connector_id,0),COALESCE(h.natural_key,'') FROM services s LEFT JOIN hosts h ON h.id=s.host_id WHERE s.state!='gone'`)
	if e != nil {
		return inv, e
	}
	ids := map[int64]int{}
	for rows.Next() {
		var id int64
		var s Service
		if e = rows.Scan(&id, &s.Ref.ConnectorID, &s.Ref.Key, &s.Name, &s.HostRef.ConnectorID, &s.HostRef.Key); e != nil {
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
	rows, e = db.QueryContext(ctx, `SELECT COALESCE(s.connector_id,0),s.natural_key,COALESCE(h.connector_id,0),COALESCE(h.natural_key,''),p.number,p.container_port,p.published FROM ports p JOIN services s ON s.id=p.service_id LEFT JOIN hosts h ON h.id=p.host_id`)
	if e != nil {
		return inv, e
	}
	for rows.Next() {
		var p Port
		if e = rows.Scan(&p.ServiceRef.ConnectorID, &p.ServiceRef.Key, &p.HostRef.ConnectorID, &p.HostRef.Key, &p.Number, &p.ContainerPort, &p.Published); e != nil {
			rows.Close()
			return inv, e
		}
		inv.Ports = append(inv.Ports, p)
	}
	return inv, rows.Close()
}

func ReconcileStore(ctx context.Context, db *sql.DB) error {
	inv, err := LoadInventory(ctx, db)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT r.id,r.natural_key,r.domain,r.path_prefix,r.upstream_host,COALESCE(r.upstream_port,0),r.tls,r.status,COALESCE(h.connector_id,0),COALESCE(h.natural_key,''),COALESCE(p.endpoint,'') FROM routes r LEFT JOIN proxies p ON p.id=r.proxy_id LEFT JOIN hosts h ON h.id=p.host_id WHERE r.state='active'`)
	if err != nil {
		return err
	}
	type item struct {
		id       int64
		r        domain.Route
		endpoint string
	}
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.r.Key, &x.r.Domain, &x.r.PathPrefix, &x.r.UpstreamHost, &x.r.UpstreamPort, &x.r.TLS, &x.r.Status, &x.r.ProxyHostConnectorID, &x.r.ProxyHostKey, &x.endpoint); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	for _, x := range items {
		x.r.ProxyNetworks = proxyNetworks(inv, EntityRef{ConnectorID: x.r.ProxyHostConnectorID, Key: x.r.ProxyHostKey}, x.endpoint)
		r := Routes([]domain.Route{x.r}, inv)[0]
		var sid any
		if r.ResolvedServiceKey != "" {
			var id int64
			if db.QueryRowContext(ctx, `SELECT id FROM services WHERE connector_id=? AND natural_key=?`, r.ResolvedServiceConnectorID, r.ResolvedServiceKey).Scan(&id) == nil {
				sid = id
			}
		}
		if _, err = db.ExecContext(ctx, `UPDATE routes SET resolved_service_id=?,resolve_confidence=?,status=? WHERE id=?`, sid, r.ResolveConfidence, r.Status, x.id); err != nil {
			return err
		}
	}
	return nil
}

func LoadProxyScope(ctx context.Context, db *sql.DB, proxyID int64) (EntityRef, []string, error) {
	var host EntityRef
	var endpoint string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(h.connector_id,0),COALESCE(h.natural_key,''),p.endpoint FROM proxies p LEFT JOIN hosts h ON h.id=p.host_id WHERE p.id=?`, proxyID).Scan(&host.ConnectorID, &host.Key, &endpoint)
	if err != nil {
		return EntityRef{}, nil, err
	}
	inv, err := LoadInventory(ctx, db)
	if err != nil {
		return EntityRef{}, nil, err
	}
	return host, proxyNetworks(inv, host, endpoint), nil
}

func proxyNetworks(inv Inventory, proxyHost EntityRef, endpoint string) []string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := normalize(u.Hostname())
	var candidates []candidate
	for _, service := range inv.Services {
		if validRef(proxyHost) && service.HostRef != proxyHost {
			continue
		}
		for _, network := range matchingNetworks(host, service) {
			candidates = append(candidates, candidate{service: service, network: network})
		}
	}
	resolved, ok := uniqueCandidate(candidates)
	if !ok {
		return nil
	}
	var networks []string
	for _, network := range resolved.service.Networks {
		networks = append(networks, network.Name)
	}
	sort.Strings(networks)
	return networks
}
