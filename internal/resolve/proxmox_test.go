package resolve

import (
	"reflect"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

func pveHost(id int64, key, kind, name, address string, aliases ...string) Host {
	return Host{ID: id, Ref: ref(9, key), Kind: kind, Name: name, Address: address, Aliases: aliases}
}

// A Docker source on a unix socket reports its host with no address, and the
// proxy URL names the VM's LAN IP. Before Proxmox hosts were second views, the
// VM was the only host at that IP, so the proxy was scoped to a host that runs
// no services and every container-name route turned broken.
func TestProxmoxGuestNeverScopesAProxyAwayFromItsServices(t *testing.T) {
	app := ref(1, "docker:app")
	docker := Host{ID: 1, Ref: app, Kind: "docker", Name: "app"}
	inv := Inventory{
		Services: []Service{service(1, "docker:app:whoami", app, "whoami", domain.ServiceNetwork{Name: "proxy", IP: "172.18.0.5"})},
		Ports:    []Port{port(ref(1, "docker:app:whoami"), app, 80, 80, false)},
	}

	unlinked := pveHost(2, "qemu:101", domain.HostKindVM, "app-vm", "192.0.2.50")
	inv.Hosts = []Host{docker, unlinked}
	if ids := MachineHostIDs(inv.Hosts, "192.0.2.50"); ids != nil {
		t.Fatalf("an unlinked VM scoped the proxy: MachineHostIDs = %v, want none", ids)
	}
	if got := Routes([]domain.Route{{UpstreamHost: "whoami", UpstreamPort: 80}}, inv)[0]; got.Status != "ok" || got.ResolvedServiceKey != "docker:app:whoami" {
		t.Fatalf("container-name route = %#v, want ok", got)
	}

	// The same guest named like the Docker host links to it by name, so the
	// proxy is scoped to the machine that runs the services.
	linked := pveHost(2, "qemu:101", domain.HostKindVM, "app", "192.0.2.50")
	inv.Hosts = []Host{docker, linked}
	ids := MachineHostIDs(inv.Hosts, "192.0.2.50")
	if !reflect.DeepEqual(ids, []int64{1}) {
		t.Fatalf("linked VM: MachineHostIDs = %v, want the Docker host [1]", ids)
	}
	route := domain.Route{UpstreamHost: "whoami", UpstreamPort: 80, ProxyHostConnectorID: app.ConnectorID, ProxyHostKey: app.Key}
	if got := Routes([]domain.Route{route}, inv)[0]; got.Status != "ok" || got.ResolvedServiceKey != "docker:app:whoami" {
		t.Fatalf("scoped container-name route = %#v, want ok", got)
	}
}

// A Docker host and a VM at one address used to be two hosts for one proxy
// URL, which left the proxy unscoped. The VM now stands for the Docker host.
func TestProxmoxGuestAtTheDockerAddressKeepsTheProxyScoped(t *testing.T) {
	hosts := []Host{
		{ID: 1, Ref: ref(1, "docker:nas"), Kind: "docker", Name: "nas", Address: "192.0.2.10"},
		pveHost(2, "qemu:102", domain.HostKindVM, "storage", "192.0.2.10"),
		pveHost(3, "lxc:201", domain.HostKindLXC, "dns", "192.0.2.53"),
		pveHost(4, "node:pve1", domain.HostKindProxmoxNode, "pve1", "192.0.2.2"),
		{ID: 5, Ref: ref(3, "ssh:192.0.2.2"), Kind: "ssh", Name: "pve1", Address: "192.0.2.2"},
	}
	for hostname, want := range map[string][]int64{
		"192.0.2.10": {1},
		"storage":    {1},
		"192.0.2.53": nil, // no machine behind the container
		"pve1":       {5},
		"192.0.2.2":  {5},
	} {
		if got := MachineHostIDs(hosts, hostname); !reflect.DeepEqual(got, want) {
			t.Errorf("MachineHostIDs(%q) = %v, want %v", hostname, got, want)
		}
	}
}

// A second NIC that only the guest agent reports lets a route to that IP
// resolve on the machine the Docker source reports for the same guest.
func TestRouteToAGuestIPResolvesOnTheLinkedMachine(t *testing.T) {
	nas := ref(1, "docker:nas")
	inv := Inventory{
		Hosts: []Host{
			{ID: 1, Ref: nas, Kind: "docker", Name: "nas", Address: "192.0.2.10"},
			pveHost(2, "qemu:102", domain.HostKindVM, "nas", "192.0.2.10", "198.51.100.7"),
		},
		Services: []Service{service(1, "docker:nas:app", nas, "app")},
		Ports:    []Port{port(ref(1, "docker:nas:app"), nas, 8080, 80, true)},
	}
	if got := Routes([]domain.Route{{UpstreamHost: "198.51.100.7", UpstreamPort: 8080}}, inv)[0]; got.Status != "ok" || got.ResolvedServiceKey != "docker:nas:app" || got.ResolveConfidence != "medium" {
		t.Fatalf("route to the guest's second IP = %#v, want docker:nas:app medium/ok", got)
	}
	// A guest the Docker source does not report resolves nothing: it has no ports.
	inv.Hosts[1] = pveHost(2, "qemu:102", domain.HostKindVM, "other", "192.0.2.99", "198.51.100.7")
	if got := Routes([]domain.Route{{UpstreamHost: "198.51.100.7", UpstreamPort: 8080}}, inv)[0]; got.Status != "broken" {
		t.Fatalf("unlinked guest resolved: %#v", got)
	}
}

// One machine is often both a Proxmox guest and a tailnet device. The two
// views link to it independently; before, a VM sharing the device's short name
// made the name ambiguous and unlinked the device.
func TestTailnetLinkSurvivesAProxmoxGuestOfTheSameName(t *testing.T) {
	docker := Host{ID: 1, Ref: dockerNAS, Kind: "docker", Name: "nas", Address: "10.0.0.2"}
	device := tailnetDevice(deviceNAS, 2, "nas", "100.64.0.5", "nas.tail1234.ts.net")
	vm := pveHost(3, "qemu:102", domain.HostKindVM, "nas", "10.0.0.3")
	want := map[EntityRef]EntityRef{deviceNAS: dockerNAS}
	if got := linkMachines([]Host{docker, device}); !reflect.DeepEqual(got, want) {
		t.Fatalf("links without the VM = %v, want %v", got, want)
	}
	want[vm.Ref] = dockerNAS
	if got := linkMachines([]Host{docker, device, vm}); !reflect.DeepEqual(got, want) {
		t.Fatalf("links with the VM = %v, want %v", got, want)
	}
	// Two Proxmox views claiming one machine on equal evidence is ambiguous for
	// them, and still says nothing about the tailnet device.
	twin := pveHost(4, "lxc:103", domain.HostKindLXC, "nas", "10.0.0.4")
	if got := linkMachines([]Host{docker, device, vm, twin}); !reflect.DeepEqual(got, map[EntityRef]EntityRef{deviceNAS: dockerNAS}) {
		t.Fatalf("links with two guests named nas = %v, want only the device", got)
	}
	// A guest at the machine's address outranks a namesake guest.
	atAddress := pveHost(5, "qemu:104", domain.HostKindVM, "files", "10.0.0.2")
	got := linkMachines([]Host{docker, vm, atAddress})
	if got[atAddress.Ref] != dockerNAS {
		t.Fatalf("address claim lost: %v", got)
	}
	if _, ok := got[vm.Ref]; ok {
		t.Fatalf("name claim linked beside an address claim: %v", got)
	}
}
