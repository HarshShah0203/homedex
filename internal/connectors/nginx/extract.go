package nginx

import (
	"fmt"
	"maps"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/HarshShah0203/homedex/internal/domain"
)

const (
	locPrefix = iota
	locExact
	locRegex
	locNamed
)

type member struct {
	host string
	port int
}

type target struct {
	id      string
	members []member
}

type loc struct {
	kind        int
	internal    bool
	path, label string
	target      *target
}

type server struct {
	ordinal   int
	names     []string
	ports     []int
	unix, tls bool
	vars      map[string]string
	locs      []loc
}

type extractor struct {
	lim     limits
	groups  map[string][]member
	httpSSL bool
}

// collect finds every http-context server block. Upstream groups are gathered
// first because proxy_pass may name a group defined later in the files.
func collect(tree []*directive, lim limits) ([]server, error) {
	x := &extractor{lim: lim, groups: map[string][]member{}}
	var blocks []*directive
	x.walk(tree, &blocks)
	out := make([]server, 0, len(blocks))
	for i, d := range blocks {
		s, err := x.server(d, i)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// walk treats the top level as http context too, so fragment files (conf.d,
// sites-enabled) mounted on their own still yield their servers.
func (x *extractor) walk(ds []*directive, servers *[]*directive) {
	for _, d := range ds {
		switch {
		case d.name == "http" && d.isBlock:
			x.walk(d.block, servers)
		case d.name == "server" && d.isBlock:
			*servers = append(*servers, d)
		case d.name == "upstream" && d.isBlock && len(d.args) == 1:
			name := strings.ToLower(d.args[0])
			if _, ok := x.groups[name]; !ok {
				x.groups[name] = members(d.block)
			}
		case d.name == "ssl" && len(d.args) == 1:
			x.httpSSL = strings.EqualFold(d.args[0], "on")
		}
	}
}

func members(ds []*directive) []member {
	out := []member{}
	for _, d := range ds {
		if d.name != "server" || d.isBlock || len(d.args) == 0 || slices.Contains(d.args[1:], "down") || hasPrefixFold(d.args[0], "unix:") {
			continue
		}
		host, port, explicit := splitHostPort(d.args[0])
		if !explicit {
			port = 80
		}
		out = append(out, member{host, port})
	}
	return out
}

func (x *extractor) server(d *directive, ordinal int) (server, error) {
	s := server{ordinal: ordinal, vars: map[string]string{}}
	ssl, listened := x.httpSSL, false
	seen := map[string]bool{}
	for _, c := range d.block {
		switch c.name {
		case "server_name":
			for _, a := range c.args {
				for _, n := range serverNames(a) {
					if !seen[n] {
						seen[n] = true
						s.names = append(s.names, n)
					}
				}
			}
		case "listen":
			if len(c.args) == 0 {
				continue
			}
			listened = true
			if hasPrefixFold(c.args[0], "unix:") {
				s.unix = true
			} else {
				p, ok := listenPort(c.args[0])
				if !ok {
					return s, dirErr(c, "listen has an invalid port")
				}
				if !slices.Contains(s.ports, p) {
					s.ports = append(s.ports, p)
				}
			}
			// ssl_certificate alone is not TLS: it usually sits in http{} and
			// is inherited by plain port-80 redirect servers.
			if slices.Contains(c.args[1:], "ssl") || slices.Contains(c.args[1:], "quic") {
				s.tls = true
			}
		case "ssl":
			if len(c.args) == 1 {
				ssl = strings.EqualFold(c.args[0], "on")
			}
		case "set":
			if err := x.set(c, s.vars); err != nil {
				return s, err
			}
		}
	}
	if !listened {
		s.ports = []int{80}
	}
	slices.Sort(s.ports)
	s.tls = s.tls || ssl
	if len(s.names) == 0 {
		s.names = []string{"_"}
	}
	err := x.locations(d.block, s.vars, &s.locs)
	return s, err
}

// locations flattens nested locations. Each sees the server's sets plus its
// own, never its parent location's: nginx does not inherit rewrite directives.
func (x *extractor) locations(ds []*directive, serverVars map[string]string, out *[]loc) error {
	for _, d := range ds {
		if d.name != "location" || !d.isBlock {
			continue
		}
		l, err := locationOf(d)
		if err != nil {
			return err
		}
		vars := maps.Clone(serverVars)
		var pass *directive
		for _, c := range d.block {
			switch c.name {
			case "set":
				if err := x.set(c, vars); err != nil {
					return err
				}
			case "internal":
				l.internal = true
			case "proxy_pass":
				if pass == nil {
					pass = c
				}
			}
		}
		if pass != nil {
			if len(pass.args) != 1 {
				return dirErr(pass, "proxy_pass expects 1 argument")
			}
			if l.target, err = x.target(pass, vars); err != nil {
				return err
			}
		}
		*out = append(*out, l)
		if err := x.locations(d.block, serverVars, out); err != nil {
			return err
		}
	}
	return nil
}

func locationOf(d *directive) (loc, error) {
	switch len(d.args) {
	case 2:
		mod, p := d.args[0], d.args[1]
		switch mod {
		case "=":
			return loc{kind: locExact, path: p, label: "= " + p}, nil
		case "^~":
			return loc{kind: locPrefix, path: p, label: p}, nil
		case "~", "~*":
			return loc{kind: locRegex, path: p, label: mod + " " + p}, nil
		}
		return loc{}, dirErr(d, "invalid location modifier")
	case 1:
		a := d.args[0]
		switch {
		case strings.HasPrefix(a, "="):
			return loc{kind: locExact, path: a[1:], label: "= " + a[1:]}, nil
		case strings.HasPrefix(a, "~*"):
			return loc{kind: locRegex, path: a[2:], label: "~* " + a[2:]}, nil
		case strings.HasPrefix(a, "~"):
			return loc{kind: locRegex, path: a[1:], label: "~ " + a[1:]}, nil
		case strings.HasPrefix(a, "@"):
			return loc{kind: locNamed, path: a, label: a}, nil
		}
		return loc{kind: locPrefix, path: a, label: a}, nil
	}
	return loc{}, dirErr(d, "location expects 1 or 2 arguments")
}

func (x *extractor) set(d *directive, vars map[string]string) error {
	if len(d.args) != 2 || !strings.HasPrefix(d.args[0], "$") {
		return nil
	}
	v, ok := subst(d.args[1], vars, x.lim.varExpansion)
	if !ok {
		return dirErr(d, fmt.Sprintf("variable expansion longer than %d bytes", x.lim.varExpansion))
	}
	vars[strings.ToLower(d.args[0][1:])] = v
	return nil
}

// runtimeVars carry the request path, never the upstream authority.
var runtimeVars = []string{"request_uri", "uri", "document_uri", "args", "query_string", "is_args"}

// target reduces a proxy_pass to upstream host:port members. A host still
// holding a $variable is kept verbatim with no port: the route stays visible
// and resolves as broken, which says more than dropping it would.
func (x *extractor) target(d *directive, vars map[string]string) (*target, error) {
	s, ok := subst(d.args[0], vars, x.lim.varExpansion)
	if !ok {
		return nil, dirErr(d, fmt.Sprintf("variable expansion longer than %d bytes", x.lim.varExpansion))
	}
	scheme, rest := "", s
	if i := strings.Index(s, "://"); i >= 0 {
		scheme, rest = strings.ToLower(s[:i]), s[i+3:]
	}
	rest = cutRuntime(rest)
	if hasPrefixFold(rest, "unix:") {
		return nil, nil
	}
	auth := rest
	if i := strings.IndexAny(auth, "/?"); i >= 0 {
		auth = auth[:i]
	}
	if i := strings.LastIndex(auth, "@"); i >= 0 {
		auth = auth[i+1:]
	}
	host, port, explicit := splitHostPort(auth)
	variable := strings.Contains(host, "$")
	if !explicit && !variable {
		if ms, ok := x.groups[strings.ToLower(host)]; ok {
			if len(ms) == 0 {
				return nil, nil
			}
			return &target{id: "group:" + strings.ToLower(host), members: ms}, nil
		}
	}
	if !explicit && !variable {
		switch scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		}
	}
	if host == "" {
		return nil, nil
	}
	return &target{id: strings.ToLower(host) + ":" + strconv.Itoa(port), members: []member{{host, port}}}, nil
}

func cutRuntime(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		if name, _ := varAt(s, i); slices.Contains(runtimeVars, strings.ToLower(name)) {
			return s[:i]
		}
	}
	return s
}

// subst expands $name and ${name} from vars; unknown variables stay verbatim.
func subst(s string, vars map[string]string, max int) (string, bool) {
	if !strings.Contains(s, "$") {
		return s, len(s) <= max
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		name, end := varAt(s, i)
		if name == "" {
			b.WriteByte(s[i])
			i++
			continue
		}
		if v, ok := vars[strings.ToLower(name)]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[i:end])
		}
		i = end
		if b.Len() > max {
			return "", false
		}
	}
	return b.String(), b.Len() <= max
}

// varAt returns the variable name starting at s[i] and the index after it,
// or "" when s[i] does not start one.
func varAt(s string, i int) (string, int) {
	if s[i] != '$' || i+1 >= len(s) {
		return "", i
	}
	if s[i+1] == '{' {
		j := strings.IndexByte(s[i+2:], '}')
		if j <= 0 || !isWord(s[i+2:i+2+j]) {
			return "", i
		}
		return s[i+2 : i+2+j], i + 3 + j
	}
	j := i + 1
	for j < len(s) && isWordByte(s[j]) {
		j++
	}
	return s[i+1 : j], j
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isWord(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return false
		}
	}
	return s != ""
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// splitHostPort splits "[v6]:p", "host:p" or a bare host; explicit reports
// whether a port was written, even one that is not a valid number (port 0).
func splitHostPort(a string) (host string, port int, explicit bool) {
	if strings.HasPrefix(a, "[") {
		if i := strings.IndexByte(a, ']'); i > 0 {
			if rest := a[i+1:]; strings.HasPrefix(rest, ":") {
				return a[1:i], portNum(rest[1:]), true
			}
			return a[1:i], 0, false
		}
		return a, 0, false
	}
	if strings.Count(a, ":") == 1 {
		i := strings.IndexByte(a, ':')
		return a[:i], portNum(a[i+1:]), true
	}
	return a, 0, false
}

func portNum(s string) int {
	if s == "" || len(s) > 5 {
		return 0
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
	}
	n, _ := strconv.Atoi(s)
	if n < 1 || n > 65535 {
		return 0
	}
	return n
}

func listenPort(a string) (int, bool) {
	var p int
	switch {
	case strings.HasPrefix(a, "["):
		i := strings.IndexByte(a, ']')
		switch {
		case i < 0:
			return 0, false
		case i == len(a)-1:
			return 80, true
		case a[i+1] != ':':
			return 0, false
		}
		p = portNum(a[i+2:])
	case strings.Trim(a, "0123456789") == "":
		p = portNum(a)
	case strings.Contains(a, ":"):
		p = portNum(a[strings.LastIndexByte(a, ':')+1:])
	default:
		return 80, true
	}
	return p, p > 0
}

// serverNames normalizes one server_name argument. Regex, variable and IP
// names cannot become a route domain; ".x" is nginx shorthand for x and *.x.
func serverNames(a string) []string {
	n := strings.ToLower(strings.TrimSuffix(a, "."))
	ip := strings.TrimSuffix(strings.TrimPrefix(n, "["), "]")
	if n == "" || n == "_" || strings.HasPrefix(n, "~") || strings.Contains(n, "$") || net.ParseIP(ip) != nil {
		return nil
	}
	if strings.HasPrefix(n, ".") {
		return []string{n[1:], "*" + n}
	}
	return []string{n}
}

func primary(l *loc) bool { return (l.kind == locPrefix || l.kind == locExact) && !l.internal }

// kept drops locations that only restate another route of the same server:
// a prefix or exact location under a shorter prefix with the same upstream
// (SWAG's /radarr/api under /radarr), and regex, named or internal locations
// whose upstream a plain location already reaches (SWAG's (/app)?/api).
func kept(locs []loc) []*loc {
	var out []*loc
	for i := range locs {
		l := &locs[i]
		if l.target == nil {
			continue
		}
		drop := false
		for j := range locs {
			p := &locs[j]
			if j == i || p.target == nil || !primary(p) || p.target.id != l.target.id {
				continue
			}
			if !primary(l) || p.kind == locPrefix && strings.HasPrefix(l.path, p.path) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, l)
		}
	}
	return out
}

func portSig(s *server) string {
	if len(s.ports) == 0 {
		return "unix"
	}
	parts := make([]string, len(s.ports))
	for i, p := range s.ports {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, "+")
}

// display is the domain a route is shown under. SWAG writes "radarr.*" and
// keeps subfolder apps in the catch-all server; base_domain names both.
func display(name, base string) (string, bool) {
	switch {
	case name == "_":
		return base, base != ""
	case base != "" && strings.HasSuffix(name, ".*"):
		return strings.TrimSuffix(name, "*") + base, true
	}
	return name, true
}

type emit struct {
	s      *server
	domain string
	l      *loc
	key    string
}

// routes keys each route on server_name and location only, never on an
// address, port or file, so certbot's 80-to-443 edit or moving a server block
// keeps its history. Twin server blocks for the same name are told apart by
// their ports, with the TLS one keeping the plain key.
func routes(servers []server, base string) []domain.Route {
	var ems []emit
	byBase := map[string][]int{}
	var order []string
	for i := range servers {
		s := &servers[i]
		locs := kept(s.locs)
		seen := map[string]bool{}
		for _, n := range s.names {
			dom, ok := display(n, base)
			if !ok {
				continue
			}
			for _, l := range locs {
				if seen[dom+"\x00"+l.label] {
					continue
				}
				seen[dom+"\x00"+l.label] = true
				key := "nginx:" + n + ":" + l.label
				if _, ok := byBase[key]; !ok {
					order = append(order, key)
				}
				byBase[key] = append(byBase[key], len(ems))
				ems = append(ems, emit{s: s, domain: dom, l: l, key: key})
			}
		}
	}
	for _, key := range order {
		idx := byBase[key]
		if len(idx) < 2 {
			continue
		}
		sort.SliceStable(idx, func(a, b int) bool {
			sa, sb := ems[idx[a]].s, ems[idx[b]].s
			if sa.tls != sb.tls {
				return sa.tls
			}
			if pa, pb := portSig(sa), portSig(sb); pa != pb {
				return pa < pb
			}
			return sa.ordinal < sb.ordinal
		})
		used := map[string]bool{key: true}
		for _, e := range idx[1:] {
			k := key + ":" + portSig(ems[e].s)
			for n := 2; used[k]; n++ {
				k = key + ":" + portSig(ems[e].s) + ":" + strconv.Itoa(n)
			}
			used[k] = true
			ems[e].key = k
		}
	}
	var out []domain.Route
	for _, e := range ems {
		ms := e.l.target.members
		for i, m := range ms {
			key := e.key
			if len(ms) > 1 {
				key += ":" + strconv.Itoa(i)
			}
			out = append(out, domain.Route{Key: key, Domain: e.domain, PathPrefix: e.l.label, UpstreamHost: m.host, UpstreamPort: m.port, TLS: e.s.tls, Status: "unknown"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return slices.CompactFunc(out, func(a, b domain.Route) bool { return a.Key == b.Key })
}
