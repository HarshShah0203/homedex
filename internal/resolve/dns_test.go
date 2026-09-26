package resolve

import (
	"reflect"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

var (
	dockerLAN = ref(1, "docker:nas")
	dnsNAS    = ref(5, "pihole:192.0.2.10")
)

func dnsView(r EntityRef, id int64, address string, aliases ...string) Host {
	name := address
	if len(aliases) > 0 {
		name = aliases[0]
	}
	return Host{ID: id, Ref: r, Kind: domain.HostKindDNS, Name: name, Address: address, Aliases: aliases}
}

// dnsInventory is one machine Docker reports at 192.0.2.10, publishing 8080,
// and the names a local resolver answers with that address.
func dnsInventory(hosts ...Host) Inventory {
	return Inventory{
		Hosts: append([]Host{
			{ID: 1, Ref: dockerLAN, Kind: "docker", Name: "storage", Address: "192.0.2.10"},
			dnsView(dnsNAS, 5, "192.0.2.10", "nas.home.arpa", "media.home.arpa"),
		}, hosts...),
		Services: []Service{service(1, "container:jellyfin", dockerLAN, "jellyfin")},
		Ports:    []Port{port(ref(1, "container:jellyfin"), dockerLAN, 8096, 8096, true)},
	}
}

func resolvedOn(inv Inventory, upstream string, port int) domain.Route {
	return Routes([]domain.Route{{UpstreamHost: upstream, UpstreamPort: port}}, inv)[0]
}

func TestDNSNameUpstreamResolvesOnLinkedMachine(t *testing.T) {
	inv := dnsInventory()
	for _, upstream := range []string{"nas.home.arpa", "MEDIA.home.arpa.", "192.0.2.10"} {
		got := resolvedOn(inv, upstream, 8096)
		if got.ResolvedServiceKey != "container:jellyfin" || got.ResolvedServiceConnectorID != 1 || got.ResolveConfidence != "medium" || got.Status != "ok" {
			t.Errorf("%s:8096 = %#v, want container:jellyfin medium/ok", upstream, got)
		}
	}
	if got := resolvedOn(inv, "nas.home.arpa", 9999); got.Status != "broken" {
		t.Errorf("unpublished port resolved: %#v", got)
	}
	// A record spelling the address as an IPv4-mapped IPv6 address still links:
	// addresses compare canonically.
	inv = dnsInventory()
	inv.Hosts[1].Address = "::ffff:192.0.2.10"
	if got := resolvedOn(inv, "nas.home.arpa", 8096); got.ResolvedServiceKey != "container:jellyfin" {
		t.Errorf("mapped IPv4 record did not link: %#v", got)
	}
	// Without the view, the name means nothing.
	inv = dnsInventory()
	inv.Hosts = inv.Hosts[:1]
	if got := resolvedOn(inv, "nas.home.arpa", 8096); got.Status != "broken" {
		t.Errorf("name resolved with no DNS view: %#v", got)
	}
}

func TestDNSViewLinksByAddressOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		machine string // the Docker host's address
		view    Host
	}{
		// The record's name is the machine's host name, but its address is no
		// machine's: a name is never evidence for a DNS view.
		"name only": {"192.0.2.10", dnsView(dnsNAS, 5, "192.0.2.99", "storage", "files.home.arpa")},
		// The view's address must be an IP, even when a machine is reached at
		// that exact name.
		"address is a name": {"storage.lan", dnsView(dnsNAS, 5, "storage.lan", "files.home.arpa")},
		// 127.0.0.1 answers the asking client and 0.0.0.0 blocks the name; neither
		// is the Docker host whose address happens to be loopback.
		"loopback":    {"127.0.0.1", dnsView(dnsNAS, 5, "127.0.0.1", "files.home.arpa")},
		"unspecified": {"0.0.0.0", dnsView(dnsNAS, 5, "0.0.0.0", "files.home.arpa")},
	} {
		inv := dnsInventory()
		inv.Hosts[0].Address = tc.machine
		inv.Hosts[1] = tc.view
		if links := linkMachines(inv.Hosts); len(links) != 0 {
			t.Errorf("%s: linked %v", name, links)
		}
		if got := resolvedOn(inv, "files.home.arpa", 8096); got.Status != "broken" {
			t.Errorf("%s: files.home.arpa:8096 resolved: %#v", name, got)
		}
		if ids := MachineHostIDs(inv.Hosts, "files.home.arpa"); ids != nil {
			t.Errorf("%s: MachineHostIDs = %v, want none", name, ids)
		}
	}
}

func TestDNSViewLinkRequiresUniqueMachineAndClaim(t *testing.T) {
	for name, inv := range map[string]Inventory{
		// Docker and SSH both report the machine at that address.
		"two machines at the address": dnsInventory(Host{ID: 2, Ref: ref(2, "ssh:storage"), Kind: "ssh", Name: "storage", Address: "192.0.2.10"}),
		// Two resolvers answer for the same machine: two views of one kind
		// claiming one machine leave both unlinked.
		"two views claim the machine": dnsInventory(dnsView(ref(6, "adguard:192.0.2.10"), 6, "192.0.2.10", "nas.lan")),
		// A view is never a machine, so a tailnet device at the record's address
		// is not something to link to.
		"only a tailnet device at the address": {
			Hosts: []Host{
				tailnetDevice(deviceNAS, 2, "nas", "192.0.2.10"),
				dnsView(dnsNAS, 5, "192.0.2.10", "nas.home.arpa"),
			},
		},
	} {
		if links := linkMachines(inv.Hosts); links[dnsNAS] != (EntityRef{}) {
			t.Errorf("%s: DNS view linked to %v", name, links[dnsNAS])
		}
		if got := resolvedOn(inv, "nas.home.arpa", 8096); got.Status != "broken" || got.ResolvedServiceKey != "" {
			t.Errorf("%s: route guessed a machine: %#v", name, got)
		}
		if ids := MachineHostIDs(inv.Hosts, "nas.home.arpa"); ids != nil {
			t.Errorf("%s: MachineHostIDs = %v, want none", name, ids)
		}
	}
	// Two views of one resolver's records for two different machines each link.
	other := ref(1, "docker:pi")
	inv := dnsInventory(
		Host{ID: 3, Ref: other, Kind: "docker", Name: "pi", Address: "192.0.2.20"},
		dnsView(ref(5, "pihole:192.0.2.20"), 7, "192.0.2.20", "pi.home.arpa"),
	)
	inv.Services = append(inv.Services, service(1, "container:grafana", other, "grafana"))
	inv.Ports = append(inv.Ports, port(ref(1, "container:grafana"), other, 3000, 3000, true))
	if got := resolvedOn(inv, "pi.home.arpa", 3000); got.ResolvedServiceKey != "container:grafana" {
		t.Errorf("pi.home.arpa:3000 = %#v", got)
	}
	if got := resolvedOn(inv, "nas.home.arpa", 8096); got.ResolvedServiceKey != "container:jellyfin" {
		t.Errorf("nas.home.arpa:8096 = %#v", got)
	}
}

// TestTailnetDeviceAndDNSViewShareAMachine: claims are counted per view kind,
// so a machine's tailnet device and its LAN DNS names both stand for it, and
// the DNS view's address claim does not outrank the device's name claim.
func TestTailnetDeviceAndDNSViewShareAMachine(t *testing.T) {
	inv := tailnetInventory(dnsView(dnsNAS, 5, "10.0.0.2", "nas.lan", "photos.lan"))
	links := linkMachines(inv.Hosts)
	if links[deviceNAS] != dockerNAS || links[dnsNAS] != dockerNAS {
		t.Fatalf("links = %v, want the device and the DNS view both on %v", links, dockerNAS)
	}
	for _, upstream := range []string{"nas.tail1234.ts.net", "100.64.0.5", "nas.lan", "photos.lan", "10.0.0.2"} {
		if got := resolvedOn(inv, upstream, 8080); got.ResolvedServiceKey != "container:app" || got.ResolveConfidence != "medium" {
			t.Errorf("%s:8080 = %#v, want container:app medium", upstream, got)
		}
	}
	for _, hostname := range []string{"nas.tail1234.ts.net", "photos.lan", "10.0.0.2"} {
		if ids := MachineHostIDs(inv.Hosts, hostname); !reflect.DeepEqual(ids, []int64{1}) {
			t.Errorf("MachineHostIDs(%q) = %v, want [1]", hostname, ids)
		}
	}
	// The device claims by address too: still one view of each kind.
	inv = tailnetInventory(dnsView(dnsNAS, 5, "10.0.0.2", "nas.lan"))
	inv.Hosts[1].Aliases = append(inv.Hosts[1].Aliases, "10.0.0.2")
	if links := linkMachines(inv.Hosts); links[deviceNAS] != dockerNAS || links[dnsNAS] != dockerNAS {
		t.Fatalf("address claims of two kinds: links = %v", links)
	}
}

func TestMachineHostIDsFollowsDNSViews(t *testing.T) {
	hosts := dnsInventory(
		dnsView(ref(5, "pihole:192.0.2.77"), 6, "192.0.2.77", "printer.home.arpa"),
		Host{ID: 4, Ref: ref(4, "ssh:router"), Kind: "ssh", Name: "router", Address: "192.0.2.1"},
	).Hosts
	for hostname, want := range map[string][]int64{
		"nas.home.arpa":     {1},
		"media.home.arpa.":  {1},
		"192.0.2.10":        {1},
		"storage":           {1},
		"printer.home.arpa": nil, // no machine behind it
		"192.0.2.77":        nil,
		"router":            {4},
	} {
		if got := MachineHostIDs(hosts, hostname); !reflect.DeepEqual(got, want) {
			t.Errorf("MachineHostIDs(%q) = %v, want %v", hostname, got, want)
		}
	}
}
