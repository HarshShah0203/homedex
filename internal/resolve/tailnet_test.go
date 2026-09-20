package resolve

import (
	"reflect"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

var (
	dockerNAS = ref(1, "docker:nas")
	deviceNAS = ref(2, "tailscale:n1")
)

func tailnetDevice(r EntityRef, id int64, name, address string, aliases ...string) Host {
	return Host{ID: id, Ref: r, Kind: domain.HostKindTailscale, Name: name, Address: address, Aliases: aliases}
}

// tailnetInventory is one machine seen twice: by Docker on the LAN, and as a
// tailnet device. Only the Docker view carries the published port.
func tailnetInventory(machines ...Host) Inventory {
	hosts := append([]Host{
		{ID: 1, Ref: dockerNAS, Kind: "docker", Name: "nas", Address: "10.0.0.2"},
		tailnetDevice(deviceNAS, 2, "NAS", "100.64.0.5", "fd7a:115c:a1e0::5", "nas.tail1234.ts.net", "nas"),
	}, machines...)
	return Inventory{
		Hosts:    hosts,
		Services: []Service{service(1, "container:app", dockerNAS, "app")},
		Ports:    []Port{port(ref(1, "container:app"), dockerNAS, 8080, 80, true)},
	}
}

func TestTailnetUpstreamsResolveOnLinkedMachine(t *testing.T) {
	inv := tailnetInventory()
	for _, upstream := range []string{"100.64.0.5", "NAS.tail1234.ts.net.", "nas", "[fd7a:115c:a1e0:0:0:0:0:5]", "fd7a:115c:a1e0::5"} {
		got := Routes([]domain.Route{{UpstreamHost: upstream, UpstreamPort: 8080}}, inv)[0]
		if got.ResolvedServiceKey != "container:app" || got.ResolvedServiceConnectorID != 1 || got.ResolveConfidence != "medium" || got.Status != "ok" {
			t.Errorf("%s:8080 = %#v, want container:app medium/ok", upstream, got)
		}
	}
	if got := Routes([]domain.Route{{UpstreamHost: "100.64.0.5", UpstreamPort: 9999}}, inv)[0]; got.Status != "broken" {
		t.Errorf("unpublished port resolved: %#v", got)
	}
	// The LAN address keeps resolving exactly as before.
	if got := Routes([]domain.Route{{UpstreamHost: "10.0.0.2", UpstreamPort: 8080}}, inv)[0]; got.ResolvedServiceKey != "container:app" {
		t.Errorf("LAN address route = %#v", got)
	}
}

func TestTailnetLinkRequiresUniqueMachine(t *testing.T) {
	route := domain.Route{UpstreamHost: "nas.tail1234.ts.net", UpstreamPort: 8080}
	for name, inv := range map[string]Inventory{
		"two machines named nas": tailnetInventory(Host{ID: 3, Ref: ref(3, "ssh:nas"), Kind: "ssh", Name: "nas", Address: "10.0.0.9"}),
		"two devices named nas":  tailnetInventory(tailnetDevice(ref(2, "tailscale:n2"), 3, "nas", "100.64.0.6", "nas-1.tail1234.ts.net")),
		"no machine named nas": {
			Hosts:    []Host{{ID: 1, Ref: dockerNAS, Kind: "docker", Name: "storage", Address: "10.0.0.2"}, tailnetDevice(deviceNAS, 2, "NAS", "100.64.0.5", "nas.tail1234.ts.net", "nas")},
			Services: []Service{service(1, "container:app", dockerNAS, "app")},
			Ports:    []Port{port(ref(1, "container:app"), dockerNAS, 8080, 80, true)},
		},
	} {
		if got := Routes([]domain.Route{route}, inv)[0]; got.Status != "broken" || got.ResolvedServiceKey != "" {
			t.Errorf("%s: route guessed a machine: %#v", name, got)
		}
	}
	// The short name is what links, so a LAN domain suffix does not matter.
	inv := tailnetInventory()
	inv.Hosts[0].Name = "nas.localdomain"
	if got := Routes([]domain.Route{route}, inv)[0]; got.ResolvedServiceKey != "container:app" || got.ResolveConfidence != "medium" {
		t.Errorf("nas.localdomain did not link: %#v", got)
	}
}

func TestMachineHostIDs(t *testing.T) {
	hosts := append(tailnetInventory().Hosts,
		tailnetDevice(ref(2, "tailscale:n3"), 3, "laptop", "100.64.0.9", "laptop.tail1234.ts.net", "laptop"),
		Host{ID: 4, Ref: ref(4, "ssh:router"), Kind: "ssh", Name: "router", Address: "10.0.0.1"},
	)
	for hostname, want := range map[string][]int64{
		"nas.tail1234.ts.net":    {1},
		"100.64.0.5":             {1},
		"fd7a:115c:a1e0:0::5":    {1},
		"nas":                    {1},
		"10.0.0.2":               {1},
		"laptop.tail1234.ts.net": nil, // no machine behind it
		"ROUTER.":                {4},
		"10.0.0.1":               {4},
		"":                       nil,
	} {
		if got := MachineHostIDs(hosts, hostname); !reflect.DeepEqual(got, want) {
			t.Errorf("MachineHostIDs(%q) = %v, want %v", hostname, got, want)
		}
	}
}

func TestLoopbackListenerResolvesOnlyOnProxyHost(t *testing.T) {
	web, db := ref(1, "ssh:web"), ref(2, "ssh:db")
	inv := Inventory{
		Hosts:    []Host{{ID: 1, Ref: web, Kind: "ssh", Name: "web"}, {ID: 2, Ref: db, Kind: "ssh", Name: "db"}},
		Services: []Service{service(1, "ssh:web:proc:gitea", web, "gitea"), service(2, "ssh:db:proc:postgres", db, "postgres")},
		Ports: []Port{
			{ServiceRef: ref(1, "ssh:web:proc:gitea"), HostRef: web, Number: 3000, ContainerPort: 3000, HostIP: "127.0.0.1"},
			{ServiceRef: ref(2, "ssh:db:proc:postgres"), HostRef: db, Number: 5432, ContainerPort: 5432, HostIP: "::1"},
		},
	}
	onWeb := func(host string, port int) domain.Route {
		return Routes([]domain.Route{{UpstreamHost: host, UpstreamPort: port, ProxyHostConnectorID: web.ConnectorID, ProxyHostKey: web.Key}}, inv)[0]
	}
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		if got := onWeb(host, 3000); got.ResolvedServiceKey != "ssh:web:proc:gitea" || got.ResolveConfidence != "medium" || got.Status != "ok" {
			t.Errorf("%s:3000 from web = %#v, want gitea medium/ok", host, got)
		}
	}
	for name, got := range map[string]domain.Route{
		"another host's loopback":   onWeb("localhost", 5432),
		"bridge gateway":            onWeb("host.docker.internal", 3000),
		"proxy host unknown":        Routes([]domain.Route{{UpstreamHost: "localhost", UpstreamPort: 3000}}, inv)[0],
		"loopback on the LAN route": onWeb("10.0.0.2", 3000),
	} {
		if got.Status != "broken" {
			t.Errorf("%s resolved: %#v", name, got)
		}
	}
}
