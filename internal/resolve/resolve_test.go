package resolve

import (
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

func ref(connectorID int64, key string) EntityRef {
	return EntityRef{ConnectorID: connectorID, Key: key}
}

func service(connectorID int64, key string, host EntityRef, name string, networks ...domain.ServiceNetwork) Service {
	return Service{Ref: ref(connectorID, key), HostRef: host, Name: name, Networks: networks}
}

func port(service, host EntityRef, number, containerPort int, published bool) Port {
	return Port{ServiceRef: service, HostRef: host, Number: number, ContainerPort: containerPort, Published: published}
}

func TestRoutesAllConfidencePaths(t *testing.T) {
	host := ref(1, "host:nas")
	inv := Inventory{
		Hosts: []Host{{Ref: host, Address: "10.0.0.2"}},
		Services: []Service{
			service(1, "ip", host, "immich", domain.ServiceNetwork{Name: "apps", IP: "172.20.0.8"}),
			service(1, "alias", host, "jellyfin", domain.ServiceNetwork{Name: "media", Aliases: []string{"media-player"}}),
			service(1, "published", host, "vaultwarden"),
			service(1, "unknown-port", host, "changedetection", domain.ServiceNetwork{Name: "apps", Aliases: []string{"watcher"}}),
		},
		Ports: []Port{
			port(ref(1, "ip"), host, 2283, 2283, false),
			port(ref(1, "alias"), host, 8096, 8096, false),
			port(ref(1, "published"), host, 9443, 443, true),
		},
	}
	routes := []domain.Route{
		{Key: "ip", UpstreamHost: "172.20.0.8", UpstreamPort: 2283},
		{Key: "name", UpstreamHost: "jellyfin", UpstreamPort: 8096},
		{Key: "alias", UpstreamHost: "media-player", UpstreamPort: 8096},
		{Key: "unknown-port", UpstreamHost: "watcher", UpstreamPort: 5000},
		{Key: "published", UpstreamHost: "10.0.0.2", UpstreamPort: 9443},
		{Key: "localhost", UpstreamHost: "localhost", UpstreamPort: 9443},
		{Key: "dead", UpstreamHost: "gone", UpstreamPort: 9999},
	}
	got := Routes(routes, inv)
	want := []struct {
		key        string
		connector  int64
		confidence string
		status     string
	}{
		{"ip", 1, "high", "ok"}, {"alias", 1, "high", "ok"}, {"alias", 1, "high", "ok"},
		// changedetection exposes no ports, so its port cannot be verified: the
		// alias match still resolves but must not claim high confidence.
		{"unknown-port", 1, "medium", "ok"}, {"published", 1, "medium", "ok"},
		{"published", 1, "medium", "ok"}, {"", 0, "none", "broken"},
	}
	for i, w := range want {
		if got[i].ResolvedServiceKey != w.key || got[i].ResolvedServiceConnectorID != w.connector || got[i].ResolveConfidence != w.confidence || got[i].Status != w.status {
			t.Errorf("route %s = %#v, want service=%d/%q confidence=%q status=%q", got[i].Key, got[i], w.connector, w.key, w.confidence, w.status)
		}
	}
}

func TestDuplicateNetworkIPUsesProxySourceIdentity(t *testing.T) {
	hostOne := ref(1, "docker:nas")
	hostTwo := ref(2, "docker:nas")
	serviceOne := ref(1, "container:abc123")
	serviceTwo := ref(2, "container:abc123")
	inv := Inventory{
		Hosts: []Host{{Ref: hostOne}, {Ref: hostTwo}},
		Services: []Service{
			service(1, serviceOne.Key, hostOne, "immich", domain.ServiceNetwork{Name: "apps", IP: "172.20.0.8"}),
			service(2, serviceTwo.Key, hostTwo, "immich", domain.ServiceNetwork{Name: "apps", IP: "172.20.0.8"}),
		},
		Ports: []Port{port(serviceOne, hostOne, 2283, 2283, false), port(serviceTwo, hostTwo, 2283, 2283, false)},
	}

	got := Routes([]domain.Route{{
		UpstreamHost: "172.20.0.8", UpstreamPort: 2283,
		ProxyHostConnectorID: hostTwo.ConnectorID, ProxyHostKey: hostTwo.Key,
	}}, inv)[0]
	if got.ResolvedServiceConnectorID != 2 || got.ResolvedServiceKey != serviceTwo.Key || got.ResolveConfidence != "high" || got.Status != "ok" {
		t.Fatalf("proxy-scoped duplicate IP route = %#v", got)
	}

	ambiguous := Routes([]domain.Route{{UpstreamHost: "172.20.0.8", UpstreamPort: 2283}}, inv)[0]
	if ambiguous.ResolvedServiceConnectorID != 0 || ambiguous.ResolvedServiceKey != "" || ambiguous.ResolveConfidence != "none" || ambiguous.Status != "broken" {
		t.Fatalf("ambiguous duplicate IP route = %#v", ambiguous)
	}
}

func TestDuplicateNetworkIPUsesProxyNetworkIdentity(t *testing.T) {
	host := ref(1, "docker:nas")
	inv := Inventory{
		Services: []Service{
			service(1, "container:one", host, "one", domain.ServiceNetwork{Name: "frontend", IP: "172.20.0.8"}),
			service(1, "container:two", host, "two", domain.ServiceNetwork{Name: "backend", IP: "172.20.0.8"}),
		},
		Ports: []Port{
			port(ref(1, "container:one"), host, 2283, 2283, false),
			port(ref(1, "container:two"), host, 2283, 2283, false),
		},
	}
	got := Routes([]domain.Route{{UpstreamHost: "172.20.0.8", UpstreamPort: 2283, ProxyNetworks: []string{"backend"}}}, inv)[0]
	if got.ResolvedServiceKey != "container:two" || got.ResolveConfidence != "high" {
		t.Fatalf("network-scoped duplicate IP route = %#v", got)
	}
}

func TestRouteFollowsRecreatedContainerNaturalKey(t *testing.T) {
	host := ref(1, "host")
	r := domain.Route{UpstreamHost: "app", UpstreamPort: 8080}
	old := Inventory{Services: []Service{service(1, "container:old", host, "app")}}
	newInv := Inventory{Services: []Service{service(1, "container:new", host, "app")}}
	if got := Routes([]domain.Route{r}, old)[0].ResolvedServiceKey; got != "container:old" {
		t.Fatal(got)
	}
	if got := Routes([]domain.Route{r}, newInv)[0].ResolvedServiceKey; got != "container:new" {
		t.Fatal(got)
	}
}

func TestZeroPortServiceNotHighOnPortAlone(t *testing.T) {
	host := ref(1, "host")
	// grafana matches by name but exposes no port rows, so the requested port is
	// unverifiable. The match may stand on the name, but must not be high.
	inv := Inventory{Services: []Service{service(1, "app", host, "grafana")}}
	got := Routes([]domain.Route{{UpstreamHost: "grafana", UpstreamPort: 3000}}, inv)[0]
	if got.ResolvedServiceKey != "app" || got.Status != "ok" {
		t.Fatalf("zero-port name match should still resolve: %#v", got)
	}
	if got.ResolveConfidence != "medium" {
		t.Fatalf("zero-port match confidence = %q, want medium (not high)", got.ResolveConfidence)
	}

	// A sibling service with a matching port row must still resolve at high
	// confidence, proving the fix does not drop legitimate verified matches.
	withPort := Inventory{
		Services: []Service{service(1, "app", host, "grafana")},
		Ports:    []Port{port(ref(1, "app"), host, 3000, 3000, false)},
	}
	verified := Routes([]domain.Route{{UpstreamHost: "grafana", UpstreamPort: 3000}}, withPort)[0]
	if verified.ResolveConfidence != "high" || verified.Status != "ok" {
		t.Fatalf("verified port match = %#v, want high/ok", verified)
	}
}

func TestNameMatchRequiresKnownPort(t *testing.T) {
	host := ref(1, "host")
	inv := Inventory{Services: []Service{service(1, "app", host, "app")}, Ports: []Port{port(ref(1, "app"), host, 8080, 8080, false)}}
	got := Routes([]domain.Route{{UpstreamHost: "app", UpstreamPort: 9090}}, inv)[0]
	if got.Status != "broken" {
		t.Fatalf("unexpected match: %#v", got)
	}
}

func TestAmbiguousNameDoesNotGuess(t *testing.T) {
	inv := Inventory{Services: []Service{service(1, "replica-1", ref(1, "host"), "web"), service(2, "replica-2", ref(2, "host"), "web")}}
	got := Routes([]domain.Route{{UpstreamHost: "web", UpstreamPort: 80}}, inv)[0]
	if got.Status != "broken" || got.ResolvedServiceKey != "" {
		t.Fatalf("ambiguous route guessed: %#v", got)
	}
}

func TestLocalhostUsesProxyHostPerspective(t *testing.T) {
	hostOne, hostTwo := ref(1, "host"), ref(2, "host")
	inv := Inventory{
		Hosts:    []Host{{Ref: hostOne}, {Ref: hostTwo}},
		Services: []Service{service(1, "app", hostOne, "app"), service(2, "app", hostTwo, "app")},
		Ports:    []Port{port(ref(1, "app"), hostOne, 8080, 80, true), port(ref(2, "app"), hostTwo, 8080, 80, true)},
	}
	got := Routes([]domain.Route{{UpstreamHost: "localhost", UpstreamPort: 8080, ProxyHostConnectorID: 2, ProxyHostKey: "host"}}, inv)[0]
	if got.ResolvedServiceConnectorID != 2 || got.ResolvedServiceKey != "app" || got.ResolveConfidence != "medium" {
		t.Fatalf("route=%#v", got)
	}
}
