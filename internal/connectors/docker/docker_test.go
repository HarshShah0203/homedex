package docker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/go-connections/nat"
)

type fakeAPI struct {
	list        []types.Container
	inspect     types.ContainerJSON
	active, max atomic.Int32
}

func (f *fakeAPI) ContainerList(context.Context, container.ListOptions) ([]types.Container, error) {
	return f.list, nil
}
func (f *fakeAPI) ContainerInspect(_ context.Context, id string) (types.ContainerJSON, error) {
	n := f.active.Add(1)
	for {
		m := f.max.Load()
		if n <= m || f.max.CompareAndSwap(m, n) {
			break
		}
	}
	defer f.active.Add(-1)
	time.Sleep(5 * time.Millisecond)
	// ContainerJSON contains pointer-backed embedded fields. Deep-copy the fixture
	// so concurrent inspectors do not mutate the same ContainerJSONBase through
	// the promoted ID field.
	b, _ := json.Marshal(f.inspect)
	var x types.ContainerJSON
	_ = json.Unmarshal(b, &x)
	x.ID = id
	return x, nil
}
func (*fakeAPI) Info(context.Context) (system.Info, error) {
	return system.Info{Name: "nas", OperatingSystem: "Ubuntu 24.04", Architecture: "x86_64"}, nil
}
func (*fakeAPI) ServerVersion(context.Context) (types.Version, error) {
	return types.Version{Version: "27.5.1"}, nil
}
func (*fakeAPI) Close() error { return nil }
func fixture[T any](t *testing.T, name string) T {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	var v T
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func raw(v string) json.RawMessage { return json.RawMessage(v) }
func TestScanMapsRecordedInspectWithoutEnvironment(t *testing.T) {
	list := fixture[[]types.Container](t, "containers.json")
	inspect := fixture[types.ContainerJSON](t, "inspect.json")
	api := &fakeAPI{list: list, inspect: inspect}
	c := New()
	c.newClient = func(Config) (API, error) { return api, nil }
	snap, e := c.Scan(context.Background(), connectors.Config{"endpoint": raw(`"unix:///var/run/docker.sock"`)})
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Services) != 1 || snap.Services[0].Name != "jellyfin" || snap.Services[0].Stack != "media" || snap.Services[0].State != "running" || snap.Services[0].Health != "healthy" {
		t.Fatalf("unexpected service: %#v", snap.Services)
	}
	if snap.Services[0].RestartPolicy != "unless-stopped" || snap.Services[0].Networks[0].IP != "172.22.0.10" {
		t.Fatalf("missing inspect metadata: %#v", snap.Services[0])
	}
	if len(snap.Ports) != 2 {
		t.Fatalf("unexpected ports: %#v", snap.Ports)
	}
	encoded, _ := json.Marshal(snap)
	if strings.Contains(string(encoded), "never-ingest-me") || strings.Contains(string(encoded), "DATABASE_PASSWORD") {
		t.Fatal("Config.Env leaked into snapshot")
	}
}
func TestInspectConcurrencyIsCappedAtEight(t *testing.T) {
	inspect := fixture[types.ContainerJSON](t, "inspect.json")
	api := &fakeAPI{inspect: inspect}
	for i := 0; i < 24; i++ {
		api.list = append(api.list, types.Container{ID: string(rune('a' + i)), Image: "app:latest"})
	}
	c := New()
	c.newClient = func(Config) (API, error) { return api, nil }
	if _, e := c.Scan(context.Background(), connectors.Config{}); e != nil {
		t.Fatal(e)
	}
	if api.max.Load() > 8 || api.max.Load() < 2 {
		t.Fatalf("maximum inspect concurrency=%d, want 2..8", api.max.Load())
	}
}
func TestDecodeAcceptsSupportedModes(t *testing.T) {
	for _, endpoint := range []string{"unix:///var/run/docker.sock", "tcp://nas:2375", "https://nas:2376", "ssh://homedex@nas"} {
		if _, e := decode(connectors.Config{"endpoint": raw(`"` + endpoint + `"`)}); e != nil {
			t.Errorf("%s: %v", endpoint, e)
		}
	}
	if _, e := decode(connectors.Config{"endpoint": raw(`"ftp://nas"`)}); e == nil {
		t.Fatal("unsupported endpoint accepted")
	}
}
func TestMapContainerIncludesHostConfigBindingsAndExposedPorts(t *testing.T) {
	in := fixture[types.ContainerJSON](t, "inspect.json")
	in.NetworkSettings.Ports = nil
	in.HostConfig.PortBindings = nat.PortMap{nat.Port("8080/tcp"): []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "18080"}}}
	in.Config.ExposedPorts = nat.PortSet{nat.Port("9000/tcp"): struct{}{}}
	_, ports := mapContainer("host", types.Container{ID: "abc"}, in)
	if len(ports) != 2 {
		t.Fatalf("ports=%#v", ports)
	}
	byContainer := map[int]domain.Port{}
	for _, p := range ports {
		byContainer[p.ContainerPort] = p
	}
	if byContainer[8080].Number != 18080 || !byContainer[8080].Published || byContainer[9000].Published {
		t.Fatalf("ports=%#v", ports)
	}
}
