package resolve

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/netip"
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
	ID      int64
	Ref     EntityRef
	Kind    string
	Name    string
	Address string
	Aliases []string
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
	HostIP        string
}

// Routes applies deterministic route-to-container resolution. It never probes
// or mutates connected infrastructure.
func Routes(routes []domain.Route, inv Inventory) []domain.Route {
	links := linkMachines(inv.Hosts)
	out := append([]domain.Route(nil), routes...)
	for i := range out {
		resolve(&out[i], inv, links)
	}
	return out
}
func resolve(route *domain.Route, inv Inventory, links map[EntityRef]EntityRef) {
	host := normalize(route.UpstreamHost)
	proxyHost := EntityRef{ConnectorID: route.ProxyHostConnectorID, Key: route.ProxyHostKey}
	// Network IP match is strongest only after all candidates are collected and
	// scoped to the proxy's Docker host/network identity where available.
	var ipCandidates []candidate
	for _, svc := range inv.Services {
		for _, n := range svc.Networks {
			if n.IP == "" || host != normalize(n.IP) {
				continue
			}
			if serves, verified := listens(svc.Ref, route.UpstreamPort, inv.Ports); serves {
				ipCandidates = append(ipCandidates, candidate{service: svc, network: n.Name, portVerified: verified})
			}
		}
	}
	if resolved, ok := uniqueCandidate(scopeCandidates(ipCandidates, proxyHost, route.ProxyNetworks)); ok {
		matched(route, resolved.service.Ref, confidenceForPort(resolved.portVerified))
		return
	}
	if len(ipCandidates) > 0 {
		broken(route)
		return
	}
	// Docker name, Compose service, or a network alias.
	var named []candidate
	for _, svc := range inv.Services {
		if !nameMatch(host, svc) {
			continue
		}
		serves, verified := listens(svc.Ref, route.UpstreamPort, inv.Ports)
		if !serves {
			continue
		}
		for _, network := range matchingNetworks(host, svc) {
			named = append(named, candidate{service: svc, network: network, portVerified: verified})
		}
	}
	if resolved, ok := uniqueCandidate(scopeCandidates(named, proxyHost, route.ProxyNetworks)); ok {
		matched(route, resolved.service.Ref, confidenceForPort(resolved.portVerified))
		return
	}
	if len(named) > 0 {
		broken(route)
		return
	}
	// Published host port. localhost and Docker gateway names are accepted when
	// exactly one published mapping matches, avoiding false high-confidence links.
	// A host is addressed by its address or any alias, and a tailnet device also
	// stands for the machine it is linked to, whose connector reports the ports.
	local := host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "host.docker.internal" || host == "gateway.docker.internal"
	hostRefs := map[EntityRef]bool{}
	for _, h := range inv.Hosts {
		if !addressedBy(h, host) {
			continue
		}
		hostRefs[h.Ref] = true
		if m, ok := links[h.Ref]; ok {
			hostRefs[m] = true
		}
	}
	// A loopback-only listener (the SSH collector records those unpublished) is
	// reachable only from a proxy running on that same host, and only through a
	// loopback upstream; host.docker.internal reaches the bridge, not loopback.
	loopbackUpstream := validRef(proxyHost) && loopback(host)
	var candidates []EntityRef
	for _, p := range inv.Ports {
		if p.Number != route.UpstreamPort {
			continue
		}
		localHostMatch := local && (!validRef(proxyHost) || proxyHost == p.HostRef)
		loop := loopbackUpstream && !p.Published && p.HostRef == proxyHost && loopback(p.HostIP)
		if (p.Published && (localHostMatch || hostRefs[p.HostRef])) || loop {
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
	service      Service
	network      string
	portVerified bool
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

// confidenceForPort keeps a verified port match at high confidence while
// downgrading name/IP matches whose port could not be verified (the service
// exposes no known ports) to medium, so an unprovable port never fabricates a
// high-confidence link.
func confidenceForPort(verified bool) string {
	if verified {
		return "high"
	}
	return "medium"
}
func normalize(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// canonicalHost lets the spellings of one IP ("[fd7a::5]", "fd7a:0:0::5",
// "::ffff:10.0.0.2") compare equal; anything else is compared normalized.
func canonicalHost(s string) string {
	s = strings.Trim(normalize(s), "[]")
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap().String()
	}
	return s
}

// shortName is the first DNS label of a host name, or "" for an IP or
// localhost, which name no particular machine.
func shortName(s string) string {
	s = normalize(s)
	if s == "" || s == "localhost" {
		return ""
	}
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return ""
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return s
}

func loopback(s string) bool {
	s = normalize(s)
	if s == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	return err == nil && a.Unmap().IsLoopback()
}

// addressedBy reports whether a route upstream host (already normalized)
// names h by its address or one of its aliases.
func addressedBy(h Host, host string) bool {
	if normalize(h.Address) == host {
		return true
	}
	want := canonicalHost(host)
	if want == "" {
		return false
	}
	if canonicalHost(h.Address) == want {
		return true
	}
	for _, alias := range h.Aliases {
		if canonicalHost(alias) == want {
			return true
		}
	}
	return false
}

// linkMachines pairs each tailnet device with the machine another connector
// (Docker, SSH, manual) reports under the same short host name. It makes no
// guesses: ambiguity on either side -- two machines with that name, or two
// devices claiming one machine -- leaves the device unlinked.
func linkMachines(hosts []Host) map[EntityRef]EntityRef {
	byName := map[string][]EntityRef{}
	for _, h := range hosts {
		if h.Kind == domain.HostKindTailscale {
			continue
		}
		if name := shortName(h.Name); name != "" {
			byName[name] = append(byName[name], h.Ref)
		}
	}
	links := map[EntityRef]EntityRef{}
	claims := map[EntityRef]int{}
	for _, h := range hosts {
		if h.Kind != domain.HostKindTailscale {
			continue
		}
		matches := map[EntityRef]bool{}
		for _, name := range append([]string{h.Name}, h.Aliases...) {
			for _, ref := range byName[shortName(name)] {
				matches[ref] = true
			}
		}
		if len(matches) != 1 {
			continue
		}
		for machine := range matches {
			links[h.Ref] = machine
			claims[machine]++
		}
	}
	for device, machine := range links {
		if claims[machine] > 1 {
			delete(links, device)
		}
	}
	return links
}

// MachineHostIDs returns the ids of the hosts a proxy URL's host name refers
// to, by name, address or alias. A tailnet device stands for the machine it is
// linked to and is dropped when unlinked: scoping a proxy to a device that runs
// no services would hide every candidate.
func MachineHostIDs(hosts []Host, hostname string) []int64 {
	name := normalize(hostname)
	if name == "" {
		return nil
	}
	links := linkMachines(hosts)
	byRef := make(map[EntityRef]int64, len(hosts))
	for _, h := range hosts {
		byRef[h.Ref] = h.ID
	}
	seen := map[int64]bool{}
	var ids []int64
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, h := range hosts {
		if normalize(h.Name) != name && !addressedBy(h, name) {
			continue
		}
		if h.Kind != domain.HostKindTailscale {
			add(h.ID)
		} else if machine, ok := links[h.Ref]; ok {
			add(byRef[machine])
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
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
// listens reports whether the service can serve the requested port (serves) and
// whether that was verified against a known port row (verified). A service with
// no known ports at all is treated as possibly serving (serves=true) but
// unverified (verified=false): the route may still resolve on name/IP evidence,
// yet callers must not upgrade it to high confidence on an unprovable port. A
// service that has known ports none of which match does not serve the port
// (serves=false), so it is not a candidate.
func listens(ref EntityRef, port int, ports []Port) (serves, verified bool) {
	known := false
	for _, p := range ports {
		if p.ServiceRef != ref {
			continue
		}
		known = true
		if p.ContainerPort == port || (!p.Published && p.Number == port) {
			return true, true
		}
	}
	if !known {
		return true, false
	}
	return false, false
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

// LoadHosts returns the active hosts with the identifiers route resolution and
// proxy linking match on.
func LoadHosts(ctx context.Context, db *sql.DB) ([]Host, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,COALESCE(connector_id,0),natural_key,name,kind,address,aliases FROM hosts WHERE state='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hosts []Host
	for rows.Next() {
		var h Host
		var aliases string
		if err = rows.Scan(&h.ID, &h.Ref.ConnectorID, &h.Ref.Key, &h.Name, &h.Kind, &h.Address, &aliases); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(aliases), &h.Aliases)
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// LoadInventory returns active Docker observations needed by Routes.
func LoadInventory(ctx context.Context, db *sql.DB) (Inventory, error) {
	var inv Inventory
	var e error
	if inv.Hosts, e = LoadHosts(ctx, db); e != nil {
		return inv, e
	}
	rows, e := db.QueryContext(ctx, `SELECT s.id,COALESCE(s.connector_id,0),s.natural_key,s.name,COALESCE(h.connector_id,0),COALESCE(h.natural_key,'') FROM services s LEFT JOIN hosts h ON h.id=s.host_id WHERE s.state!='gone'`)
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
	rows, e = db.QueryContext(ctx, `SELECT COALESCE(s.connector_id,0),s.natural_key,COALESCE(h.connector_id,0),COALESCE(h.natural_key,''),p.number,p.container_port,p.published,p.host_ip FROM ports p JOIN services s ON s.id=p.service_id LEFT JOIN hosts h ON h.id=p.host_id`)
	if e != nil {
		return inv, e
	}
	for rows.Next() {
		var p Port
		if e = rows.Scan(&p.ServiceRef.ConnectorID, &p.ServiceRef.Key, &p.HostRef.ConnectorID, &p.HostRef.Key, &p.Number, &p.ContainerPort, &p.Published, &p.HostIP); e != nil {
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
	links := linkMachines(inv.Hosts)
	for _, x := range items {
		x.r.ProxyNetworks = proxyNetworks(inv, EntityRef{ConnectorID: x.r.ProxyHostConnectorID, Key: x.r.ProxyHostKey}, x.endpoint)
		r := x.r
		resolve(&r, inv, links)
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
