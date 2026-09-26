package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

var _ connectors.Connector = (*Connector)(nil)

const (
	testTokenID = "homedex@pve!inventory"
	testSecret  = "5f0b3c1e-9a4d-4e7b-8c21-0d6f3a9b7e42"
	wantAuth    = "PVEAPIToken=" + testTokenID + "=" + testSecret
	// Proxmox writes its die() text into the reason phrase and, since
	// libpve-http-server-perl 5.2.0, the body's "message". Neither may reach
	// an error Homedex stores.
	leakyReason  = "QEMU guest agent is not running LEAKY-REASON"
	leakyMessage = "LEAKY-BODY-MESSAGE"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func body(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_, _ = w.Write([]byte(s))
	}
}

// rawStatus answers with a reason phrase and body message of Proxmox's
// choosing, which net/http's own server would never send.
func rawStatus(code int, reason string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		defer conn.Close()
		payload := fmt.Sprintf(`{"data":null,"message":"%s %s\n"}`, reason, leakyMessage)
		fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Type: application/json;charset=UTF-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", code, reason, len(payload), payload)
		_ = buf.Flush()
	}
}

// never is the set of paths the connector must not request, whatever the
// token could technically read. Under /access only the token's own
// permissions may be read.
var never = []string{"/config", "/pending", "/cloudinit", "/agent/exec", "/agent/file-read", "/agent/get-users", "/agent/get-fsinfo", "/agent/ping", "/monitor", "/storage", "/vncproxy", "/termproxy", "/status/current"}

type fakePVE struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
}

func (f *fakePVE) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// newPVE serves the routes given, keyed by path and query, over TLS with a
// self-signed certificate as Proxmox does by default. Anything that is not a
// GET with exactly the token header, or not a listed route, fails the test.
// Unless the routes say otherwise, the token holds PVEAuditor on PVE 9.
func newPVE(t *testing.T, routes map[string]http.HandlerFunc) *fakePVE {
	t.Helper()
	if _, ok := routes[pathPerms]; !ok {
		routes[pathPerms] = body(fixture(t, "access-permissions.json"))
	}
	f := &fakePVE{}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		f.mu.Lock()
		f.calls = append(f.calls, key)
		f.mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("%s %s: the connector must only GET", r.Method, key)
		}
		for _, bad := range never {
			if strings.Contains(r.URL.Path, bad) {
				t.Errorf("requested forbidden path %s", key)
			}
		}
		if strings.Contains(r.URL.Path, "/access/") && r.URL.Path != pathPerms {
			t.Errorf("requested access path %s beyond the token's own permissions", key)
		}
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("%s: Authorization = %q", key, got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if h, ok := routes[key]; ok {
			h(w, r)
			return
		}
		t.Errorf("unexpected request %s", key)
		http.NotFound(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func fingerprintOf(srv *httptest.Server) string {
	sum := sha256.Sum256(srv.Certificate().Raw)
	return formatFingerprint(sum[:])
}

func cfg(kv ...string) connectors.Config {
	c := connectors.Config{}
	for i := 0; i+1 < len(kv); i += 2 {
		b, _ := json.Marshal(kv[i+1])
		c[kv[i]] = b
	}
	return c
}

func pinned(srv *httptest.Server, kv ...string) connectors.Config {
	return cfg(append([]string{"url", srv.URL, "token_id", testTokenID, "token_secret", testSecret, "fingerprint", fingerprintOf(srv)}, kv...)...)
}

const (
	pathPerms  = "/api2/json/access/permissions"
	pathNodes  = "/api2/json/cluster/resources?type=node"
	pathGuests = "/api2/json/cluster/resources?type=vm"
	pathStatus = "/api2/json/cluster/status"
)

func agentPath(node string, vmid int) string {
	return fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
}

func lxcPath(node string, vmid int) string {
	return fmt.Sprintf("/api2/json/nodes/%s/lxc/%d/interfaces", node, vmid)
}

func clusterRoutes(t *testing.T) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		pathNodes:              body(fixture(t, "cluster-resources-node.json")),
		pathGuests:             body(fixture(t, "cluster-resources-vm.json")),
		pathStatus:             body(fixture(t, "cluster-status-cluster.json")),
		agentPath("pve1", 101): body(fixture(t, "qemu-agent-network.json")),
		agentPath("pve2", 105): rawStatus(500, leakyReason),
		lxcPath("pve1", 201):   body(fixture(t, "lxc-interfaces-v9.json")),
		lxcPath("pve2", 202):   body(fixture(t, "lxc-interfaces-legacy.json")),
		lxcPath("pve2", 204):   body(fixture(t, "lxc-interfaces-stopped.json")),
	}
}

func assertClean(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, secret := range []string{testSecret, "PVEAPIToken", "LEAKY-REASON", leakyMessage, "guest agent is not running"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
}

func TestScanReportsNodesAndGuestsOfACluster(t *testing.T) {
	srv := newPVE(t, clusterRoutes(t))
	snap, err := New().Scan(context.Background(), pinned(srv.Server))
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Key, Name, Kind, Address, OS, Parent, Power string
		Aliases                                     []string
		NameUnread, AddressesUnread                 bool
	}
	var got []row
	for _, h := range snap.Hosts {
		got = append(got, row{h.Key, h.Name, h.Kind, h.Address, h.OS, h.ParentKey, h.PowerState, h.Aliases, h.NameUnread, h.AddressesUnread})
	}
	want := []row{
		{Key: "lxc:201", Name: "dns", Kind: "lxc", Address: "192.0.2.53", Aliases: []string{"198.51.100.53"}, Parent: "node:pve1", Power: "running"},
		{Key: "lxc:202", Name: "proxy", Kind: "lxc", Address: "192.0.2.80", Parent: "node:pve2", Power: "running"},
		{Key: "lxc:203", Name: "old-wiki", Kind: "lxc", Parent: "node:pve2", Power: "stopped"},
		// Running, but the container endpoint answered {"data":null}.
		{Key: "lxc:204", Name: "media", Kind: "lxc", Parent: "node:pve2", Power: "running", AddressesUnread: true},
		{Key: "node:pve1", Name: "pve1", Kind: "proxmox-node", Address: "192.0.2.11", OS: "Proxmox VE", Power: "online"},
		{Key: "node:pve2", Name: "pve2", Kind: "proxmox-node", Address: "192.0.2.12", OS: "Proxmox VE", Power: "online"},
		{Key: "node:pve3", Name: "pve3", Kind: "proxmox-node", Address: "192.0.2.13", OS: "Proxmox VE", Power: "offline"},
		{Key: "node:pve4", Name: "pve4", Kind: "proxmox-node", OS: "Proxmox VE", Power: "unknown"},
		// eth0 before eth1; docker0, tailscale0, loopback, link-local and,
		// with IPv4 present, IPv6 are left out.
		{Key: "qemu:101", Name: "web", Kind: "vm", Address: "192.0.2.21", Aliases: []string{"198.51.100.21"}, Parent: "node:pve1", Power: "running"},
		{Key: "qemu:102", Name: "backup", Kind: "vm", Parent: "node:pve2", Power: "stopped"},
		{Key: "qemu:103", Name: "build", Kind: "vm", Parent: "node:pve1", Power: "paused", AddressesUnread: true},
		// No RRD data: no name, status unknown, nothing read.
		{Key: "qemu:104", Name: "VM 104", Kind: "vm", Parent: "node:pve3", Power: "unknown", NameUnread: true, AddressesUnread: true},
		// The agent answered 500: unread, and the scan goes on.
		{Key: "qemu:105", Name: "noagent", Kind: "vm", Parent: "node:pve2", Power: "running", AddressesUnread: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts:\n got %+v\nwant %+v", got, want)
	}
	for _, h := range snap.Hosts {
		if h.Key == "qemu:9000" {
			t.Fatal("template listed as a guest")
		}
	}
	if len(snap.Services)+len(snap.Ports)+len(snap.Routes)+len(snap.Certs)+len(snap.Domains) != 0 {
		t.Fatalf("snapshot carries more than hosts: %+v", snap)
	}
	for _, p := range srv.requested() {
		for _, idle := range []string{"/102/", "/103/", "/104/", "/203/", "/9000/"} {
			if strings.Contains(p, idle) {
				t.Errorf("asked a guest that is not running: %s", p)
			}
		}
	}

	// A second scan is identical: keys come from type and VMID, never names or IPs.
	again, err := New().Scan(context.Background(), pinned(srv.Server))
	if err != nil || !reflect.DeepEqual(again, snap) {
		t.Fatalf("rescan differs: %v", err)
	}
}

func TestScanStandaloneNode(t *testing.T) {
	srv := newPVE(t, map[string]http.HandlerFunc{
		pathNodes:           body(`{"data":[{"id":"node/pve","type":"node","node":"pve","status":"online"}]}`),
		pathGuests:          body(`{"data":[{"id":"lxc/100","type":"lxc","vmid":100,"node":"pve","name":"pihole","status":"running","template":0}]}`),
		pathStatus:          body(fixture(t, "cluster-status-standalone.json")),
		lxcPath("pve", 100): body(`{"data":[{"name":"eth0","hwaddr":"bc:24:11:00:01:00","inet6":"2001:db8:20::100/64"}]}`),
	})
	snap, err := New().Scan(context.Background(), pinned(srv.Server))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Hosts) != 2 || snap.Hosts[0].Key != "lxc:100" || snap.Hosts[1].Key != "node:pve" {
		t.Fatalf("hosts = %+v", snap.Hosts)
	}
	// An IPv6-only container keeps its global address.
	if ct, n := snap.Hosts[0], snap.Hosts[1]; ct.Address != "2001:db8:20::100" || ct.ParentKey != "node:pve" || n.Address != "198.51.100.2" || n.AddressesUnread {
		t.Fatalf("container %+v node %+v", ct, n)
	}
}

func TestBooleansAcceptIntegersAndJSONBooleans(t *testing.T) {
	srv := newPVE(t, map[string]http.HandlerFunc{
		pathNodes:  body(`{"data":[{"type":"node","node":"pve","status":"online"}]}`),
		pathStatus: body(`{"data":[]}`),
		pathGuests: body(`{"data":[
			{"type":"qemu","vmid":100,"node":"pve","name":"tpl","status":"stopped","template":true},
			{"type":"qemu","vmid":101,"node":"pve","name":"vm","status":"stopped","template":false},
			{"type":"lxc","vmid":102,"node":"pve","name":"ct-tpl","status":"stopped","template":1},
			{"type":"lxc","vmid":103,"node":"pve","name":"ct","status":"stopped"}]}`),
	})
	snap, err := New().Scan(context.Background(), pinned(srv.Server))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, h := range snap.Hosts {
		keys = append(keys, h.Key)
	}
	if !reflect.DeepEqual(keys, []string{"lxc:103", "node:pve", "qemu:101"}) {
		t.Fatalf("hosts = %v, want the templates left out", keys)
	}
	var b pveBool
	if err := json.Unmarshal([]byte(`2`), &b); err == nil {
		t.Fatal("2 decoded as a boolean")
	}
}

func TestClusterStatusForbiddenLeavesNodeAddressesUnread(t *testing.T) {
	routes := clusterRoutes(t)
	routes[pathStatus] = rawStatus(403, "Permission check failed (/, Sys.Audit)")
	snap, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range snap.Hosts {
		if h.Kind == domain.HostKindProxmoxNode && (h.Address != "" || !h.AddressesUnread) {
			t.Fatalf("node %+v, want its address unread", h)
		}
	}
}

func TestGuestFailuresNeverFailTheScan(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"403 on PVE 8":           rawStatus(403, "Permission check failed (/vms/101, VM.Monitor)"),
		"500 agent not running":  rawStatus(500, leakyReason),
		"595 node unreachable":   rawStatus(595, "Connection refused LEAKY-REASON"),
		"596 TLS to node":        rawStatus(596, "Connection timed out LEAKY-REASON"),
		"599 other":              rawStatus(599, "Too many redirections LEAKY-REASON"),
		"malformed JSON":         body(`{"data":{"result":[`),
		"no result":              body(`{"data":{}}`),
		"result is not a list":   body(`{"data":{"result":{"name":"eth0"}}}`),
		"interfaces are strings": body(`{"data":{"result":["eth0"]}}`),
	} {
		t.Run(name, func(t *testing.T) {
			routes := clusterRoutes(t)
			routes[agentPath("pve1", 101)] = handler
			snap, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server))
			if err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			for _, h := range snap.Hosts {
				switch h.Key {
				case "qemu:101":
					if !h.AddressesUnread || h.Address != "" {
						t.Fatalf("failed guest %+v, want unread", h)
					}
				case "lxc:201":
					if h.Address != "192.0.2.53" {
						t.Fatalf("another guest lost its address: %+v", h)
					}
				}
			}
		})
	}
}

func TestMalformedClusterResponsesFailTheScan(t *testing.T) {
	for _, path := range []string{pathNodes, pathGuests, pathStatus} {
		for name, payload := range map[string]string{
			"null data":       `{"data":null}`,
			"no data":         `{"errors":{}}`,
			"object data":     `{"data":{"node":"pve1"}}`,
			"vmid string":     `{"data":[{"type":"qemu","vmid":"101","node":"pve1","status":"running"}]}`,
			"bad template":    `{"data":[{"type":"qemu","vmid":101,"node":"pve1","status":"running","template":"yes"}]}`,
			"invalid JSON":    `{"data":[`,
			"HTML login page": `<!DOCTYPE html><html><title>Proxmox Virtual Environment</title></html>`,
		} {
			if path == pathStatus && (name == "vmid string" || name == "bad template") {
				continue // cluster/status has neither field
			}
			t.Run(path+" "+name, func(t *testing.T) {
				routes := clusterRoutes(t)
				routes[path] = body(payload)
				_, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server))
				if err == nil {
					t.Fatal("malformed response accepted")
				}
				assertClean(t, err)
			})
		}
	}
}

func TestOversizedBodyFails(t *testing.T) {
	routes := clusterRoutes(t)
	routes[pathGuests] = func(w http.ResponseWriter, _ *http.Request) {
		row := `{"type":"qemu","vmid":101,"node":"pve1","name":"web","status":"running","template":0},`
		_, _ = w.Write([]byte(`{"data":[`))
		for written := 0; written <= connectors.MaxResponseBytes; written += len(row) {
			_, _ = w.Write([]byte(row))
		}
		_, _ = w.Write([]byte(`{"type":"node","node":"pve1"}]}`))
	}
	if _, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server)); err == nil {
		t.Fatal("a body over the size cap was accepted")
	}
}

func TestZeroNodesIsAnError(t *testing.T) {
	routes := clusterRoutes(t)
	routes[pathNodes] = body(`{"data":[]}`)
	if _, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server)); !errors.Is(err, errNoNodes) {
		t.Fatalf("zero nodes: err = %v", err)
	}
	if err := New().Validate(context.Background(), pinned(newPVE(t, routes).Server)); !errors.Is(err, errNoNodes) {
		t.Fatalf("zero nodes on test: err = %v", err)
	}
}

func TestStatusErrorsNeverEchoUpstreamText(t *testing.T) {
	for _, tc := range []struct {
		code int
		want string
	}{
		{401, "rejected the API token (401 Unauthorized)"},
		{403, "403 Forbidden for cluster/resources (nodes)"},
		{404, "404 Not Found for cluster/resources (nodes)"},
		{302, "does not follow redirects"},
		{500, "500 Internal Server Error for cluster/resources (nodes)"},
		{595, "could not reach the node"},
	} {
		routes := clusterRoutes(t)
		routes[pathNodes] = rawStatus(tc.code, leakyReason)
		err := New().Validate(context.Background(), pinned(newPVE(t, routes).Server))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: err = %v, want %q", tc.code, err, tc.want)
		}
		assertClean(t, err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("the token followed a redirect")
		}
	}))
	defer other.Close()
	routes := clusterRoutes(t)
	routes[pathNodes] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	}
	err := New().Validate(context.Background(), pinned(newPVE(t, routes).Server))
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v", err)
	}
	if elsewhere.Load() != 0 {
		t.Fatal("redirect target contacted")
	}
}

func TestCancellationFailsTheScanPromptly(t *testing.T) {
	routes := clusterRoutes(t)
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 8)
	routes[agentPath("pve1", 101)] = func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	srv := newPVE(t, routes)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	begin := time.Now()
	_, err := New().Scan(ctx, pinned(srv.Server))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(begin) > 5*time.Second {
		t.Fatalf("cancelled scan took %s", time.Since(begin))
	}
}

// Guests the budget does not reach keep their stored addresses, the scan
// still succeeds, and no more than the configured number of guest calls run
// at once. A stopped guest listed after them still reads as having no
// addresses, rather than keeping the ones it had while it ran.
func TestGuestFanOutIsBoundedInTimeAndConcurrency(t *testing.T) {
	var inFlight, peak atomic.Int32
	slow := func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}
	var guests []string
	routes := map[string]http.HandlerFunc{
		pathNodes:  body(`{"data":[{"type":"node","node":"pve1","status":"online"}]}`),
		pathStatus: body(`{"data":[]}`),
	}
	// Nine calls fit the budget; the rest are left over when it runs out.
	for vmid := 100; vmid < 124; vmid++ {
		guests = append(guests, fmt.Sprintf(`{"type":"qemu","vmid":%d,"node":"pve1","name":"vm%d","status":"running","template":0}`, vmid, vmid))
		routes[agentPath("pve1", vmid)] = slow
	}
	guests = append(guests, `{"type":"qemu","vmid":200,"node":"pve1","name":"off","status":"stopped","template":0}`)
	routes[pathGuests] = body(`{"data":[` + strings.Join(guests, ",") + `]}`)
	c := &Connector{guestTimeout: 250 * time.Millisecond, guestWorkers: 3, guestBudget: 600 * time.Millisecond}
	begin := time.Now()
	snap, err := c.Scan(context.Background(), pinned(newPVE(t, routes).Server))
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Fatalf("scan took %s, want the guest budget to bound it", took)
	}
	if p := peak.Load(); p > 3 || p == 0 {
		t.Fatalf("peak concurrent guest calls = %d, want 1..3", p)
	}
	var stopped bool
	for _, h := range snap.Hosts {
		switch {
		case h.Key == "qemu:200":
			stopped = true
			if h.AddressesUnread || h.Address != "" || len(h.Aliases) != 0 {
				t.Fatalf("stopped guest after the budget ran out: unread=%v address=%q aliases=%v, want no addresses", h.AddressesUnread, h.Address, h.Aliases)
			}
		case h.Kind == domain.HostKindVM && !h.AddressesUnread:
			t.Fatalf("guest %s read although every agent call timed out", h.Key)
		}
	}
	if !stopped {
		t.Fatal("stopped guest qemu:200 missing from the scan")
	}
	// The engine's own deadline also bounds the budget.
	ctx, cancel := context.WithTimeout(context.Background(), budgetMargin+700*time.Millisecond)
	defer cancel()
	c.guestBudget = time.Minute
	if _, err = c.Scan(ctx, pinned(newPVE(t, routes).Server)); err != nil {
		t.Fatalf("scan under a near deadline failed instead of leaving guests unread: %v", err)
	}
}

func TestTLSPinningAndTrust(t *testing.T) {
	srv := newPVE(t, clusterRoutes(t))
	fp := fingerprintOf(srv.Server)
	base := func(kv ...string) connectors.Config {
		return cfg(append([]string{"url", srv.URL, "token_id", testTokenID, "token_secret", testSecret}, kv...)...)
	}
	for name, pin := range map[string]string{
		"upper with colons":  fp,
		"lower no colons":    strings.ToLower(strings.ReplaceAll(fp, ":", "")),
		"openssl output":     "SHA256 Fingerprint=" + fp,
		"surrounding spaces": "  " + fp + "\n",
	} {
		if err := New().Validate(context.Background(), base("fingerprint", pin)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	wrong := strings.Repeat("AB:", 31) + "AB"
	err := New().Validate(context.Background(), base("fingerprint", wrong))
	if err == nil || !strings.Contains(err.Error(), "does not match the pinned fingerprint") || !strings.Contains(err.Error(), fp) {
		t.Fatalf("wrong pin: %v", err)
	}

	// Unpinned, the self-signed certificate is refused, and the error shows
	// the fingerprint to compare and paste.
	err = New().Validate(context.Background(), base())
	if err == nil || !strings.Contains(err.Error(), "not trusted") || !strings.Contains(err.Error(), fp) {
		t.Fatalf("unpinned: %v", err)
	}
	assertClean(t, err)

	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	if err = New().Validate(context.Background(), base("ca_pem", caPEM)); err != nil {
		t.Fatalf("configured CA: %v", err)
	}
	// The CA does not stand in for the host name the certificate names.
	named := cfg("url", strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), "token_id", testTokenID, "token_secret", testSecret, "ca_pem", caPEM)
	if err = New().Validate(context.Background(), named); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("CA with a host name the certificate lacks: %v", err)
	}
}

func TestPlainHTTPOnlyOnLoopback(t *testing.T) {
	plain := httptest.NewServer(newPVE(t, clusterRoutes(t)).Config.Handler)
	defer plain.Close()
	if err := New().Validate(context.Background(), cfg("url", plain.URL, "token_id", testTokenID, "token_secret", testSecret)); err != nil {
		t.Fatalf("loopback http: %v", err)
	}
	if _, err := decode(cfg("url", "http://pve.lab.example:8006", "token_id", testTokenID, "token_secret", testSecret)); err == nil || !strings.Contains(err.Error(), "cleartext") {
		t.Fatalf("remote http: %v", err)
	}
}

func TestDecodeConfig(t *testing.T) {
	ok := func(kv ...string) settings {
		t.Helper()
		s, err := decode(cfg(append([]string{"token_id", testTokenID, "token_secret", testSecret}, kv...)...))
		if err != nil {
			t.Fatalf("%v: %v", kv, err)
		}
		return s
	}
	for in, want := range map[string]string{
		"pve.lab.example":                           "https://pve.lab.example:8006",
		"https://pve.lab.example:8006/":             "https://pve.lab.example:8006",
		"https://pve.lab.example:8006/#v1:0:18:4::": "https://pve.lab.example:8006",
		"https://pve.lab.example/api2/json":         "https://pve.lab.example:8006",
		"https://[2001:db8::2]":                     "https://[2001:db8::2]:8006",
		"https://192.0.2.11:443":                    "https://192.0.2.11:443",
	} {
		if got := ok("url", in).base; got != want {
			t.Errorf("url %q -> %q, want %q", in, got, want)
		}
	}
	if s := ok("url", "pve.lab.example"); s.auth != wantAuth {
		t.Errorf("auth header = %q", s.auth)
	}
	// A user named by email in an LDAP, AD or OpenID realm: the realm starts
	// after the last '@'.
	for _, id := range []string{"a@b.example@oidc!tok", "svc.homedex@example.com@authentik!inventory"} {
		s, err := decode(cfg("url", "pve.lab.example", "token_id", id, "token_secret", testSecret))
		if err != nil {
			t.Errorf("token ID %q: %v", id, err)
		} else if want := "PVEAPIToken=" + id + "=" + testSecret; s.auth != want {
			t.Errorf("token ID %q: auth header = %q", id, s.auth)
		}
	}
	// Loopback spelled another way is reduced to "localhost", the one name
	// the proxy settings and the hosts file know.
	for in, want := range map[string]string{
		"http://LOCALHOST:8006":   "http://localhost:8006",
		"http://localhost.:8006":  "http://localhost:8006",
		"https://LocalHost.":      "https://localhost:8006",
		"http://[::1]:8006":       "http://[::1]:8006",
		"http://127.0.0.1:8006":   "http://127.0.0.1:8006",
		"https://PVE.lab.example": "https://PVE.lab.example:8006",
	} {
		if got := ok("url", in).base; got != want {
			t.Errorf("url %q -> %q, want %q", in, got, want)
		}
	}
	for name, kv := range map[string][]string{
		"no url":             {"token_id", testTokenID, "token_secret", testSecret},
		"path":               {"url", "https://pve.lab.example:8006/pve2/", "token_id", testTokenID, "token_secret", testSecret},
		"credentials in url": {"url", "https://root:" + testSecret + "@pve.lab.example", "token_id", testTokenID, "token_secret", testSecret},
		"query":              {"url", "https://pve.lab.example?x=1", "token_id", testTokenID, "token_secret", testSecret},
		"ftp":                {"url", "ftp://pve.lab.example", "token_id", testTokenID, "token_secret", testSecret},
		"no token id":        {"url", "pve", "token_secret", testSecret},
		"user not token":     {"url", "pve", "token_id", "homedex@pve", "token_secret", testSecret},
		"whole header":       {"url", "pve", "token_id", wantAuth, "token_secret", testSecret},
		"bad token id":       {"url", "pve", "token_id", "homedex!inventory", "token_secret", testSecret},
		"bang in user":       {"url", "pve", "token_id", "a!b@pve!inventory", "token_secret", testSecret},
		"@ in token name":    {"url", "pve", "token_id", "homedex@pve!inv@entory", "token_secret", testSecret},
		"no secret":          {"url", "pve", "token_id", testTokenID},
		"spaced secret":      {"url", "pve", "token_id", testTokenID, "token_secret", testSecret + " extra"},
		"short fingerprint":  {"url", "pve", "token_id", testTokenID, "token_secret", testSecret, "fingerprint", "AB:CD"},
		"pin and CA":         {"url", "pve", "token_id", testTokenID, "token_secret", testSecret, "fingerprint", strings.Repeat("AB", 32), "ca_pem", "x"},
		"pin over http":      {"url", "http://127.0.0.1:8006", "token_id", testTokenID, "token_secret", testSecret, "fingerprint", strings.Repeat("AB", 32)},
		"CA not PEM":         {"url", "pve", "token_id", testTokenID, "token_secret", testSecret, "ca_pem", "not a certificate"},
		"private key as CA":  {"url", "pve", "token_id", testTokenID, "token_secret", testSecret, "ca_pem", "-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----"},
	} {
		_, err := decode(cfg(kv...))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		assertClean(t, err)
		if strings.Contains(err.Error(), "5f0b3c1e") {
			t.Errorf("%s: error quotes the secret: %v", name, err)
		}
	}
}

func TestSelectAddresses(t *testing.T) {
	list := func(ips ...string) *[]struct {
		IPAddress string `json:"ip-address"`
	} {
		out := make([]struct {
			IPAddress string `json:"ip-address"`
		}, len(ips))
		for i, ip := range ips {
			out[i].IPAddress = ip
		}
		return &out
	}
	for name, tc := range map[string]struct {
		in      []guestInterface
		address string
		aliases []string
	}{
		"nothing":                     {},
		"loopback only":               {in: []guestInterface{{Name: "lo", IPAddresses: list("127.0.0.1", "::1")}}},
		"windows":                     {in: []guestInterface{{Name: "Ethernet", IPAddresses: list("fe80::1%12", "192.0.2.40")}, {Name: "Loopback Pseudo-Interface 1", IPAddresses: list("127.0.0.1", "::1")}}, address: "192.0.2.40"},
		"sorted by name":              {in: []guestInterface{{Name: "ens19", IPAddresses: list("198.51.100.9")}, {Name: "ens18", IPAddresses: list("192.0.2.9", "192.0.2.8")}}, address: "192.0.2.8", aliases: []string{"192.0.2.9", "198.51.100.9"}},
		"ipv6 only":                   {in: []guestInterface{{Name: "eth0", IPAddresses: list("fe80::2", "2001:db8::9", "fd00::9")}}, address: "2001:db8::9", aliases: []string{"fd00::9"}},
		"mapped v4":                   {in: []guestInterface{{Name: "eth0", IPAddresses: list("::ffff:192.0.2.7")}}, address: "192.0.2.7"},
		"legacy CIDRs":                {in: []guestInterface{{Name: "eth0", Inet: "192.0.2.80/24", Inet6: "fe80::1/64"}}, address: "192.0.2.80"},
		"empty list wins over legacy": {in: []guestInterface{{Name: "eth0", IPAddresses: list(), Inet: "192.0.2.80/24"}}},
		"overlays and bridges": {in: []guestInterface{
			{Name: "wg0", IPAddresses: list("10.8.0.2")}, {Name: "zt5u4abc", IPAddresses: list("10.147.17.2")}, {Name: "cni0", IPAddresses: list("10.42.0.1")},
			{Name: "br-1a2b", IPAddresses: list("172.18.0.1")}, {Name: "kube-ipvs0", IPAddresses: list("10.43.0.1")}, {Name: "virbr0", IPAddresses: list("192.168.122.1")},
			{Name: "eth0", IPAddresses: list("192.0.2.30", "169.254.1.1", "224.0.0.1", "0.0.0.0", "not-an-ip")},
		}, address: "192.0.2.30"},
	} {
		address, aliases := selectAddresses(tc.in)
		if address != tc.address || !reflect.DeepEqual(aliases, tc.aliases) {
			t.Errorf("%s: %q %v, want %q %v", name, address, aliases, tc.address, tc.aliases)
		}
	}
}

// A guest's root user chooses its address list. However long it is, one guest
// contributes at most maxGuestAddresses, the same ones on every scan.
func TestGuestAddressesAreCapped(t *testing.T) {
	type ip = struct {
		IPAddress string `json:"ip-address"`
	}
	var eth0, eth1 []ip
	for i := 999; i >= 0; i-- {
		eth1 = append(eth1, ip{fmt.Sprintf("10.1.%d.%d", i/250, i%250+1)})
		eth0 = append(eth0, ip{fmt.Sprintf("10.0.%d.%d", i/250, i%250+1)}, ip{fmt.Sprintf("10.0.%d.%d", i/250, i%250+1)})
	}
	ifaces := []guestInterface{{Name: "eth1", IPAddresses: &eth1}, {Name: "eth0", IPAddresses: &eth0}}
	address, aliases := selectAddresses(ifaces)
	if address != "10.0.0.1" {
		t.Fatalf("address = %q, want the lowest on the first interface", address)
	}
	want := make([]string, 0, maxGuestAddresses-1)
	for i := 2; i <= maxGuestAddresses; i++ {
		want = append(want, fmt.Sprintf("10.0.0.%d", i))
	}
	if !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases = %v, want %v", aliases, want)
	}

	// The same holds end to end, for an agent reply near the body limit.
	var reply []string
	for i := 0; i < 60000; i++ {
		reply = append(reply, fmt.Sprintf(`{"ip-address":"10.%d.%d.%d","ip-address-type":"ipv4","prefix":8}`, i/62500, i/250%250, i%250+1))
	}
	routes := clusterRoutes(t)
	routes[agentPath("pve1", 101)] = body(`{"data":{"result":[{"name":"eth0","ip-addresses":[` + strings.Join(reply, ",") + `]}]}}`)
	snap, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range snap.Hosts {
		if h.Key == "qemu:101" && (h.Address == "" || len(h.Aliases) != maxGuestAddresses-1) {
			t.Fatalf("qemu:101: address %q and %d aliases, want %d addresses in all", h.Address, len(h.Aliases), maxGuestAddresses)
		}
	}
}

// With a proxy configured, plain http to loopback still never leaves the
// machine: it carries the token in cleartext. https keeps the proxy.
func TestPlainHTTPNeverUsesAProxy(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		http.Error(w, "proxied", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	plain := httptest.NewServer(newPVE(t, clusterRoutes(t)).Config.Handler)
	defer plain.Close()
	port := plain.URL[strings.LastIndex(plain.URL, ":")+1:]
	for _, host := range []string{"localhost.", "LOCALHOST", "127.0.0.1"} {
		raw := cfg("url", "http://"+host+":"+port, "token_id", testTokenID, "token_secret", testSecret)
		s, err := decode(raw)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		a, done := newAPI(s)
		done()
		if a.client.Transport.(*http.Transport).Proxy != nil {
			t.Errorf("%s: plain http is routed through the environment's proxy", host)
		}
		if err = New().Validate(context.Background(), raw); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	if n := proxied.Load(); n != 0 {
		t.Fatalf("the proxy received %d requests carrying the token in cleartext", n)
	}

	s, err := decode(cfg("url", "https://pve.lab.example:8006", "token_id", testTokenID, "token_secret", testSecret))
	if err != nil {
		t.Fatal(err)
	}
	a, done := newAPI(s)
	done()
	if a.client.Transport.(*http.Transport).Proxy == nil {
		t.Error("https no longer honours the environment's proxy settings")
	}

	for addr, allowed := range map[string]bool{"127.0.0.1:8006": true, "[::1]:8006": true, "127.0.0.9:80": true, "192.0.2.10:8006": false, "[2001:db8::1]:8006": false, "localhost:8006": false} {
		if err := loopbackOnly("tcp", addr, nil); (err == nil) != allowed {
			t.Errorf("dial %s: err = %v, want allowed=%v", addr, err, allowed)
		}
	}
}

func TestStatusIsKeptVerbatimOrUnknown(t *testing.T) {
	for in, want := range map[string]string{"running": "running", "postmigrate": "postmigrate", "io-error": "io-error", "": "unknown", "Running": "unknown", "run\nning": "unknown", strings.Repeat("a", 40): "unknown"} {
		if got := state(in); got != want {
			t.Errorf("state(%q) = %q, want %q", in, got, want)
		}
	}
}

// Without VM.Audit, cluster/resources lists no guests at all, which a scan
// would read as every guest gone. That is refused, on Test and on scans.
func TestTokenWithoutGuestAuditIsRefused(t *testing.T) {
	for name, perms := range map[string]string{
		"no privileges":  `{"data":{}}`,
		"only Sys.Audit": `{"data":{"/":{"Sys.Audit":1},"/nodes":{"Sys.Audit":1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			routes := clusterRoutes(t)
			routes[pathPerms] = body(perms)
			srv := newPVE(t, routes)
			if _, err := New().Scan(context.Background(), pinned(srv.Server)); !errors.Is(err, errNoGuestAudit) {
				t.Fatalf("scan: err = %v", err)
			}
			if err := New().Validate(context.Background(), pinned(srv.Server)); !errors.Is(err, errNoGuestAudit) {
				t.Fatalf("test: err = %v", err)
			}
			for _, p := range srv.requested() {
				if p != pathPerms {
					t.Errorf("read %s after the permission check failed", p)
				}
			}
		})
	}
}

// A VM.Audit granted only on a pool is enough to list that pool's guests.
func TestGuestAuditOnAPoolIsAccepted(t *testing.T) {
	routes := clusterRoutes(t)
	routes[pathPerms] = body(`{"data":{"/pool/lab":{"VM.Audit":1,"VM.GuestAgent.Audit":1},"/":{"Sys.Audit":0}}}`)
	if _, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server)); err != nil {
		t.Fatal(err)
	}
}

// PVEAuditor on PVE 8 has no guest agent privilege: VMs are not asked for
// their addresses (each call would answer 403), containers still are.
func TestNoAgentPrivilegeSkipsGuestAgentCalls(t *testing.T) {
	routes := clusterRoutes(t)
	routes[pathPerms] = body(fixture(t, "access-permissions-pve8.json"))
	srv := newPVE(t, routes)
	snap, err := New().Scan(context.Background(), pinned(srv.Server))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range srv.requested() {
		if strings.Contains(p, "/agent/") {
			t.Errorf("asked a guest agent without the privilege: %s", p)
		}
	}
	for _, h := range snap.Hosts {
		switch h.Key {
		case "qemu:101":
			if !h.AddressesUnread || h.Address != "" {
				t.Fatalf("VM without agent access %+v, want unread", h)
			}
		case "qemu:102":
			if h.AddressesUnread {
				t.Fatalf("stopped VM %+v, want no addresses rather than unread", h)
			}
		case "lxc:201":
			if h.Address != "192.0.2.53" {
				t.Fatalf("container %+v lost its address", h)
			}
		}
	}
}

func TestPermissionReadFailuresFailTheScan(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"401":          rawStatus(401, leakyReason),
		"403":          rawStatus(403, leakyReason),
		"null data":    body(`{"data":null}`),
		"list data":    body(`{"data":[]}`),
		"invalid JSON": body(`{"data":{`),
	} {
		t.Run(name, func(t *testing.T) {
			routes := clusterRoutes(t)
			routes[pathPerms] = handler
			_, err := New().Scan(context.Background(), pinned(newPVE(t, routes).Server))
			if err == nil {
				t.Fatal("scan succeeded without the token's permissions")
			}
			assertClean(t, err)
		})
	}
}

func TestExampleConfigDecodes(t *testing.T) {
	b, err := os.ReadFile("../../../docs/examples/connectors/proxmox.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw connectors.Config
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	s, err := decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.base != "https://pve.lab.example:8006" || !s.https || s.pin != nil || s.roots != nil {
		t.Fatalf("example: base=%q https=%v pinned=%v ca=%v", s.base, s.https, s.pin != nil, s.roots != nil)
	}
}
