// Package proxmox reads a Proxmox VE cluster, or a single node, through its
// REST API and reports each node, QEMU VM and LXC container as a host: guests
// keyed by VMID and linked to the node they run on, with the IPs their guest
// agent or container reports. It is read-only: every request is a GET of the
// token's own access/permissions, cluster/resources, cluster/status and, for
// running guests only, the QEMU agent's network-get-interfaces or the
// container's interface list. Guest configs, consoles, every other agent
// command, storage, users and ACLs are never requested.
package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

const (
	// A guest agent call costs up to about 8 s on the server (a 3 s guest-ping,
	// then the default 5 s QMP timeout), and agent calls run in pvedaemon,
	// which has 3 workers per node by default.
	defaultGuestTimeout = 10 * time.Second
	defaultGuestWorkers = 4
	// The engine gives a whole scan 60 s. Guests not read within this budget
	// keep their stored addresses instead of failing the scan.
	defaultGuestBudget = 40 * time.Second
	budgetMargin       = 5 * time.Second
)

type Connector struct {
	guestTimeout time.Duration
	guestWorkers int
	guestBudget  time.Duration
}

func New() *Connector {
	return &Connector{guestTimeout: defaultGuestTimeout, guestWorkers: defaultGuestWorkers, guestBudget: defaultGuestBudget}
}

func (*Connector) Kind() string { return "proxmox" }

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	s, err := decode(raw)
	if err != nil {
		return err
	}
	a, done := newAPI(s)
	defer done()
	if _, err = a.permissions(ctx); err != nil {
		return err
	}
	if _, err = a.nodes(ctx); err != nil {
		return err
	}
	_, err = a.guests(ctx)
	return err
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	s, err := decode(raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	a, done := newAPI(s)
	defer done()
	may, err := a.permissions(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	nodes, err := a.nodes(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	addresses, addressesRead, err := a.nodeAddresses(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	guests, err := a.guests(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	ips := c.readGuests(ctx, a, guests, may.agent)
	// A cancelled scan is a failed scan; only the guest budget running out
	// leaves guests unread.
	if err = ctx.Err(); err != nil {
		return domain.Snapshot{}, err
	}
	return snapshot(nodes, addresses, addressesRead, guests, ips), nil
}

// node and guest are the validated rows of cluster/resources.
type node struct {
	name   string
	status string
}

type guest struct {
	kind   string // "qemu" or "lxc"
	vmid   int64
	node   string
	name   string // "" when Proxmox has no name for it yet
	status string
}

var (
	nodeNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	statusRE   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

const maxVMID = 999999999

// state keeps a status verbatim when it is a plain word, and says "unknown"
// otherwise, never a guess such as "running".
func state(s string) string {
	s = strings.TrimSpace(s)
	if !statusRE.MatchString(s) {
		return "unknown"
	}
	return s
}

func printableName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 253 || !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl) {
		return ""
	}
	return s
}

func toNodes(rows []resource) []node {
	var out []node
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Type != "node" || !nodeNameRE.MatchString(r.Node) || seen[r.Node] {
			continue
		}
		seen[r.Node] = true
		out = append(out, node{name: r.Node, status: state(r.Status)})
	}
	return out
}

// toGuests keeps QEMU VMs and LXC containers, drops templates, and ignores
// every other type (storage, pools, SDN, and whatever a later release adds).
// A row without RRD data has no name or template flag and status "unknown";
// it is kept, since its VMID still names a guest that exists.
func toGuests(rows []resource) []guest {
	var out []guest
	seen := map[string]bool{}
	for _, r := range rows {
		if (r.Type != "qemu" && r.Type != "lxc") || bool(r.Template) || r.VMID < 1 || r.VMID > maxVMID || !nodeNameRE.MatchString(r.Node) {
			continue
		}
		key := r.Type + ":" + strconv.FormatInt(r.VMID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, guest{kind: r.Type, vmid: r.VMID, node: r.Node, name: printableName(r.Name), status: state(r.Status)})
	}
	return out
}

// guestIPs is what one guest reported. known is false when nothing could be
// read for a reason expected to pass, so the stored addresses are kept.
type guestIPs struct {
	address string
	aliases []string
	known   bool
}

// readGuests asks each running guest for its interfaces, a few at a time. A
// stopped guest has no addresses. Any other state, a failed call (an agent
// that is not installed or not answering, a 59x from a node the entry node
// cannot reach) or the budget running out leaves the guest unread, never the
// scan failed. Without an agent privilege (PVEAuditor on PVE 8) VMs are not
// asked at all, since every call would answer 403.
func (c *Connector) readGuests(ctx context.Context, a *api, guests []guest, agent bool) []guestIPs {
	out := make([]guestIPs, len(guests))
	budget, timeout, workers := c.guestBudget, c.guestTimeout, c.guestWorkers
	if budget <= 0 {
		budget = defaultGuestBudget
	}
	if timeout <= 0 {
		timeout = defaultGuestTimeout
	}
	if workers <= 0 {
		workers = defaultGuestWorkers
	}
	deadline := time.Now().Add(budget)
	if d, ok := ctx.Deadline(); ok && d.Add(-budgetMargin).Before(deadline) {
		deadline = d.Add(-budgetMargin)
	}
	fanCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	slots := make(chan struct{}, workers)
	var wg sync.WaitGroup
dispatch:
	for i, g := range guests {
		switch g.status {
		case "stopped":
			out[i] = guestIPs{known: true}
			continue
		case "running":
			if g.kind == "qemu" && !agent {
				continue
			}
		default:
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-fanCtx.Done():
			break dispatch
		}
		wg.Add(1)
		go func(i int, g guest) {
			defer wg.Done()
			defer func() { <-slots }()
			rctx, cancel := context.WithTimeout(fanCtx, timeout)
			defer cancel()
			out[i] = a.guestInterfaces(rctx, g)
		}(i, g)
	}
	wg.Wait()
	return out
}

// excludedInterface names interfaces whose addresses belong to something other
// than the guest's own presence on the network: loopback, container bridges
// and veths, CNI and overlay devices, and tunnels whose addresses another
// source (the Tailscale connector) owns.
func excludedInterface(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "lo" {
		return true
	}
	for _, prefix := range []string{"docker", "br-", "veth", "cni", "flannel", "cali", "cilium", "vxlan", "virbr", "lxcbr", "podman", "kube-", "tailscale", "wg", "zt"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// parseAddr accepts a bare address or the CIDR form the LXC endpoint used
// before pve-container 5.2.6, and never looks at the reported address type:
// the agent says "ipv4"/"ipv6" where LXC says "inet"/"inet6".
func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	a, err := netip.ParseAddr(s)
	if err != nil {
		p, perr := netip.ParsePrefix(s)
		if perr != nil {
			return netip.Addr{}, false
		}
		a = p.Addr()
	}
	a = a.Unmap().WithZone("")
	usable := a.IsValid() && !a.IsUnspecified() && !a.IsLoopback() && !a.IsLinkLocalUnicast() && !a.IsMulticast()
	return a, usable
}

// selectAddresses picks a guest's address and aliases deterministically. IPv6
// counts only for a guest with no IPv4 at all: temporary IPv6 addresses rotate
// daily and would file an alias change each time.
func selectAddresses(ifaces []guestInterface) (string, []string) {
	type found struct {
		iface string
		addr  netip.Addr
	}
	var v4, v6 []found
	for _, ifc := range ifaces {
		if excludedInterface(ifc.Name) {
			continue
		}
		var raw []string
		if ifc.IPAddresses != nil {
			for _, ip := range *ifc.IPAddresses {
				raw = append(raw, ip.IPAddress)
			}
		} else {
			raw = []string{ifc.Inet, ifc.Inet6}
		}
		for _, r := range raw {
			a, ok := parseAddr(r)
			if !ok {
				continue
			}
			if a.Is4() {
				v4 = append(v4, found{ifc.Name, a})
			} else {
				v6 = append(v6, found{ifc.Name, a})
			}
		}
	}
	chosen := v4
	if len(chosen) == 0 {
		chosen = v6
	}
	if len(chosen) == 0 {
		return "", nil
	}
	sort.Slice(chosen, func(i, j int) bool {
		if chosen[i].iface != chosen[j].iface {
			return chosen[i].iface < chosen[j].iface
		}
		return chosen[i].addr.Less(chosen[j].addr)
	})
	address := chosen[0].addr.String()
	var aliases []string
	seen := map[string]bool{address: true}
	for _, f := range chosen[1:] {
		if a := f.addr.String(); !seen[a] {
			seen[a] = true
			aliases = append(aliases, a)
		}
	}
	return address, aliases
}

func guestKey(g guest) string { return g.kind + ":" + strconv.FormatInt(g.vmid, 10) }

func snapshot(nodes []node, addresses map[string]string, addressesRead bool, guests []guest, ips []guestIPs) domain.Snapshot {
	var s domain.Snapshot
	onNode := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		onNode[n.name] = true
		s.Hosts = append(s.Hosts, domain.Host{
			Key:             "node:" + n.name,
			Name:            n.name,
			Kind:            domain.HostKindProxmoxNode,
			Address:         addresses[n.name],
			AddressesUnread: !addressesRead,
			OS:              "Proxmox VE",
			PowerState:      n.status,
		})
	}
	for i, g := range guests {
		h := domain.Host{Key: guestKey(g), Name: g.name, Kind: domain.HostKindVM, PowerState: g.status}
		if g.kind == "lxc" {
			h.Kind = domain.HostKindLXC
		}
		// Without RRD data (a new guest before its first status cycle, or a
		// node that stopped reporting) Proxmox has no name for the guest. A
		// placeholder keeps it listed; the stored name is kept over it.
		if h.Name == "" {
			h.NameUnread = true
			if g.kind == "lxc" {
				h.Name = "CT " + strconv.FormatInt(g.vmid, 10)
			} else {
				h.Name = "VM " + strconv.FormatInt(g.vmid, 10)
			}
		}
		if onNode[g.node] {
			h.ParentKey = "node:" + g.node
		}
		h.Address, h.Aliases, h.AddressesUnread = ips[i].address, ips[i].aliases, !ips[i].known
		s.Hosts = append(s.Hosts, h)
	}
	sort.Slice(s.Hosts, func(i, j int) bool { return s.Hosts[i].Key < s.Hosts[j].Key })
	return s
}

var errNoNodes = errors.New("Proxmox listed no nodes; check that the URL points at a Proxmox VE node and the token belongs to it")

func describeStatus(code int, what string) error {
	switch {
	case code == 401:
		return errors.New("Proxmox rejected the API token (401 Unauthorized): check the token ID and secret, and that neither the token nor its user is disabled or expired")
	case code/100 == 3:
		return fmt.Errorf("Proxmox answered %s with a redirect (%s); Homedex does not follow redirects with the token, so check the URL", what, statusText(code))
	case code == 404:
		return fmt.Errorf("Proxmox API returned 404 Not Found for %s; check that the URL is a Proxmox VE node on port 8006", what)
	case code >= 595 && code <= 599:
		return fmt.Errorf("Proxmox could not reach the node serving %s (%d)", what, code)
	default:
		return fmt.Errorf("Proxmox API returned %s for %s", statusText(code), what)
	}
}

// statusText rebuilds the status from its code: Proxmox writes its error text
// into the reason phrase, and that text is never stored.
func statusText(code int) string {
	if text := httpStatusText(code); text != "" {
		return strconv.Itoa(code) + " " + text
	}
	return strconv.Itoa(code)
}
