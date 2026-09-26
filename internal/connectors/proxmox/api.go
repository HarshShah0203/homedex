package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
)

const (
	apiPrefix      = "/api2/json"
	requestTimeout = 15 * time.Second
)

var httpStatusText = http.StatusText

type api struct {
	client *http.Client
	base   string
	auth   string
}

// newAPI builds a client of its own for one scan: TLS verified as configured,
// redirects never followed (a redirect would resend the token wherever it
// points), and idle connections closed when the scan is done.
func newAPI(s settings) (*api, func()) {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       tlsConfig(s),
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConnsPerHost:   defaultGuestWorkers,
		IdleConnTimeout:       30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	client := connectors.Client(requestTimeout)
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &api{client: client, base: s.base, auth: s.auth}, transport.CloseIdleConnections
}

// pveBool decodes a schema boolean. Proxmox emits them from Perl integers, so
// they arrive as 0 and 1, not true and false; both are accepted.
type pveBool bool

func (b *pveBool) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "1", "true", `"1"`:
		*b = true
	case "0", "false", `"0"`, `""`, "null":
		*b = false
	default:
		return errors.New("proxmox: a boolean field is not 0, 1, true or false")
	}
	return nil
}

// resource allow-lists what is read from cluster/resources. Tags, pools, HA
// state, locks and every utilisation counter are never decoded.
type resource struct {
	Type     string  `json:"type"`
	Node     string  `json:"node"`
	VMID     int64   `json:"vmid"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Template pveBool `json:"template"`
}

type clusterMember struct {
	Type string `json:"type"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// guestInterface is one interface from the QEMU agent or the LXC endpoint.
// Hardware addresses and the agent's statistics are never decoded.
type guestInterface struct {
	Name        string `json:"name"`
	IPAddresses *[]struct {
		IPAddress string `json:"ip-address"`
	} `json:"ip-addresses"`
	// Before pve-container 5.2.6 a container reported only the last address
	// of each family, as a CIDR.
	Inet  string `json:"inet"`
	Inet6 string `json:"inet6"`
}

// forbiddenError is a 403, which the caller may treat as a missing privilege.
type forbiddenError struct{ what string }

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("Proxmox API returned 403 Forbidden for %s: the token lacks a privilege this read needs", e.what)
}

// get reads one API path and returns its "data" member, which is null or
// absent only when the caller says it may be. Errors name the path and a
// status rebuilt from the code alone: Proxmox puts its error text in both the
// reason phrase and the body's "message", and neither is ever kept.
func (a *api) get(ctx context.Context, path, what string) (json.RawMessage, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	err := connectors.GetJSON(ctx, a.client, a.base+apiPrefix+path, &envelope,
		connectors.WithHeader("Authorization", a.auth), connectors.WithHeader("Accept", "application/json"))
	var se *connectors.StatusError
	var untrusted *untrustedError
	var ue *url.Error
	switch {
	case errors.As(err, &se) && se.StatusCode == http.StatusForbidden:
		return nil, &forbiddenError{what}
	case errors.As(err, &se):
		return nil, describeStatus(se.StatusCode, what)
	case errors.As(err, &untrusted):
		return nil, untrusted
	case ctx.Err() != nil:
		return nil, fmt.Errorf("read Proxmox %s: %w", what, ctx.Err())
	case errors.As(err, &ue):
		return nil, fmt.Errorf("read Proxmox %s: %w", what, ue.Err)
	case err != nil:
		return nil, fmt.Errorf("decode Proxmox %s: %w", what, err)
	}
	return envelope.Data, nil
}

// list decodes a response whose data must be an array. A 200 without one (a
// captive proxy, the wrong port) must not read as an empty cluster, which
// would mark every guest gone.
func (a *api) list(ctx context.Context, path, what string, out any) error {
	data, err := a.get(ctx, path, what)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 || string(bytes.TrimSpace(data)) == "null" {
		return fmt.Errorf("Proxmox response for %s has no data list", what)
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Proxmox %s: %w", what, err)
	}
	return nil
}

// access is what the token may read, from its own effective privileges.
type access struct {
	guests bool // VM.Audit somewhere: cluster/resources lists guests
	agent  bool // a privilege the guest agent's network-get-interfaces accepts
}

// errNoGuestAudit stops a scan that would read an empty guest list as every
// guest gone: without VM.Audit, cluster/resources silently omits every guest.
var errNoGuestAudit = errors.New("the Proxmox API token holds no VM.Audit privilege, so it cannot see any guest. Grant the PVEAuditor role on / to both the user and the token: a privilege-separated token gets only what both hold")

// permissions reads the token's own effective privileges, which any token may
// do. Only the privilege names are looked at; the ACL paths and propagate
// flags are neither kept nor reported.
func (a *api) permissions(ctx context.Context) (access, error) {
	data, err := a.get(ctx, "/access/permissions", "access/permissions")
	if err != nil {
		return access{}, err
	}
	var paths map[string]map[string]json.RawMessage
	if len(bytes.TrimSpace(data)) == 0 || string(bytes.TrimSpace(data)) == "null" {
		return access{}, errors.New("Proxmox response for access/permissions has no privileges")
	}
	if err = json.Unmarshal(data, &paths); err != nil {
		return access{}, fmt.Errorf("decode Proxmox access/permissions: %w", err)
	}
	var out access
	for _, privs := range paths {
		for name := range privs {
			switch name {
			case "VM.Audit":
				out.guests = true
			// PVE 9 splits agent access into VM.GuestAgent.*; PVE 8 gated every
			// agent command behind VM.Monitor, which Homedex never asks for but
			// uses when an operator granted it.
			case "VM.GuestAgent.Audit", "VM.GuestAgent.Unrestricted", "VM.Monitor":
				out.agent = true
			}
		}
	}
	if !out.guests {
		return access{}, errNoGuestAudit
	}
	return out, nil
}

func (a *api) nodes(ctx context.Context) ([]node, error) {
	var rows []resource
	if err := a.list(ctx, "/cluster/resources?type=node", "cluster/resources (nodes)", &rows); err != nil {
		return nil, err
	}
	nodes := toNodes(rows)
	if len(nodes) == 0 {
		return nil, errNoNodes
	}
	return nodes, nil
}

func (a *api) guests(ctx context.Context) ([]guest, error) {
	var rows []resource
	if err := a.list(ctx, "/cluster/resources?type=vm", "cluster/resources (guests)", &rows); err != nil {
		return nil, err
	}
	return toGuests(rows), nil
}

// nodeAddresses reads each node's address from cluster/status. That needs
// Sys.Audit on "/", and without it the scan goes on with nodes whose
// addresses are unread (read=false) rather than failing.
func (a *api) nodeAddresses(ctx context.Context) (map[string]string, bool, error) {
	var rows []clusterMember
	err := a.list(ctx, "/cluster/status", "cluster/status", &rows)
	var forbidden *forbiddenError
	if errors.As(err, &forbidden) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := map[string]string{}
	for _, r := range rows {
		if r.Type != "node" || !nodeNameRE.MatchString(r.Name) {
			continue
		}
		if ip, err := netip.ParseAddr(strings.TrimSpace(r.IP)); err == nil {
			out[r.Name] = ip.Unmap().WithZone("").String()
		}
	}
	return out, true, nil
}

// guestInterfaces reads one running guest's interfaces through the node the
// same cluster/resources listing placed it on. Every failure means "unread".
func (a *api) guestInterfaces(ctx context.Context, g guest) guestIPs {
	base := "/nodes/" + url.PathEscape(g.node) + "/" + g.kind + "/" + strconv.FormatInt(g.vmid, 10)
	var ifaces []guestInterface
	switch g.kind {
	case "qemu":
		data, err := a.get(ctx, base+"/agent/network-get-interfaces", "guest agent")
		if err != nil {
			return guestIPs{}
		}
		var reply struct {
			Result *[]guestInterface `json:"result"`
		}
		if json.Unmarshal(data, &reply) != nil || reply.Result == nil {
			return guestIPs{}
		}
		ifaces = *reply.Result
	case "lxc":
		data, err := a.get(ctx, base+"/interfaces", "container interfaces")
		// {"data":null} is how Proxmox answers for a container it cannot
		// enter: stopped since the listing, or on another node of a PVE 8
		// cluster older than pve-container 5.2.0. Nothing is known.
		if err != nil || len(bytes.TrimSpace(data)) == 0 || string(bytes.TrimSpace(data)) == "null" || json.Unmarshal(data, &ifaces) != nil {
			return guestIPs{}
		}
	default:
		return guestIPs{}
	}
	address, aliases := selectAddresses(ifaces)
	return guestIPs{address: address, aliases: aliases, known: true}
}
