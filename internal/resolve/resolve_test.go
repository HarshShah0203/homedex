package resolve

import (
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

func TestRoutesAllConfidencePaths(t *testing.T) {
	inv := Inventory{Hosts: []domain.Host{{Key: "host:nas", Address: "10.0.0.2"}}, Services: []domain.Service{{Key: "ip", Name: "immich", Networks: []domain.ServiceNetwork{{Name: "apps", IP: "172.20.0.8"}}}, {Key: "alias", Name: "jellyfin", Networks: []domain.ServiceNetwork{{Name: "media", Aliases: []string{"media-player"}}}}, {Key: "published", Name: "vaultwarden"}, {Key: "unknown-port", Name: "changedetection", Networks: []domain.ServiceNetwork{{Aliases: []string{"watcher"}}}}}, Ports: []domain.Port{{ServiceKey: "ip", ContainerPort: 2283}, {ServiceKey: "alias", ContainerPort: 8096}, {ServiceKey: "published", HostKey: "host:nas", Number: 9443, ContainerPort: 443, Published: true}}}
	routes := []domain.Route{{Key: "ip", UpstreamHost: "172.20.0.8", UpstreamPort: 2283}, {Key: "name", UpstreamHost: "jellyfin", UpstreamPort: 8096}, {Key: "alias", UpstreamHost: "media-player", UpstreamPort: 8096}, {Key: "unknown-port", UpstreamHost: "watcher", UpstreamPort: 5000}, {Key: "published", UpstreamHost: "10.0.0.2", UpstreamPort: 9443}, {Key: "localhost", UpstreamHost: "localhost", UpstreamPort: 9443}, {Key: "dead", UpstreamHost: "gone", UpstreamPort: 9999}}
	got := Routes(routes, inv)
	want := []struct{ key, confidence, status string }{{"ip", "high", "ok"}, {"alias", "high", "ok"}, {"alias", "high", "ok"}, {"unknown-port", "high", "ok"}, {"published", "medium", "ok"}, {"published", "medium", "ok"}, {"", "none", "broken"}}
	for i, w := range want {
		if got[i].ResolvedServiceKey != w.key || got[i].ResolveConfidence != w.confidence || got[i].Status != w.status {
			t.Errorf("route %s = %#v, want service=%q confidence=%q status=%q", got[i].Key, got[i], w.key, w.confidence, w.status)
		}
	}
}
func TestRouteFollowsRecreatedContainerNaturalKey(t *testing.T) {
	r := domain.Route{UpstreamHost: "app", UpstreamPort: 8080}
	old := Inventory{Services: []domain.Service{{Key: "container:old", Name: "app"}}}
	newInv := Inventory{Services: []domain.Service{{Key: "container:new", Name: "app"}}}
	if got := Routes([]domain.Route{r}, old)[0].ResolvedServiceKey; got != "container:old" {
		t.Fatal(got)
	}
	if got := Routes([]domain.Route{r}, newInv)[0].ResolvedServiceKey; got != "container:new" {
		t.Fatal(got)
	}
}
func TestNameMatchRequiresKnownPort(t *testing.T) {
	inv := Inventory{Services: []domain.Service{{Key: "app", Name: "app"}}, Ports: []domain.Port{{ServiceKey: "app", ContainerPort: 8080}}}
	got := Routes([]domain.Route{{UpstreamHost: "app", UpstreamPort: 9090}}, inv)[0]
	if got.Status != "broken" {
		t.Fatalf("unexpected match: %#v", got)
	}
}
func TestAmbiguousNameDoesNotGuess(t *testing.T) {
	inv := Inventory{Services: []domain.Service{{Key: "replica-1", Name: "web"}, {Key: "replica-2", Name: "web"}}}
	got := Routes([]domain.Route{{UpstreamHost: "web", UpstreamPort: 80}}, inv)[0]
	if got.Status != "broken" || got.ResolvedServiceKey != "" {
		t.Fatalf("ambiguous route guessed: %#v", got)
	}
}
func TestLocalhostUsesProxyHostPerspective(t *testing.T) {
	inv := Inventory{Hosts: []domain.Host{{Key: "one"}, {Key: "two"}}, Services: []domain.Service{{Key: "app-one"}, {Key: "app-two"}}, Ports: []domain.Port{{ServiceKey: "app-one", HostKey: "one", Number: 8080, Published: true}, {ServiceKey: "app-two", HostKey: "two", Number: 8080, Published: true}}}
	got := Routes([]domain.Route{{UpstreamHost: "localhost", UpstreamPort: 8080, ProxyHostKey: "two"}}, inv)[0]
	if got.ResolvedServiceKey != "app-two" || got.ResolveConfidence != "medium" {
		t.Fatalf("route=%#v", got)
	}
}
