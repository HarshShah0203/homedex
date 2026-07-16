package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/HarshShah0203/homedex/internal/resolve"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestFakeLabSnapshotIsDeterministicAndSecretFree(t *testing.T) {
	snapshot := fakeLabSnapshot()
	if len(snapshot.Hosts) != 3 || len(snapshot.Services) != 12 || len(snapshot.Ports) != 16 || len(snapshot.Routes) != 10 || len(snapshot.Certs) != 4 || len(snapshot.Domains) != 1 {
		t.Fatalf("unexpected fixture counts: hosts=%d services=%d ports=%d routes=%d certs=%d domains=%d",
			len(snapshot.Hosts), len(snapshot.Services), len(snapshot.Ports), len(snapshot.Routes), len(snapshot.Certs), len(snapshot.Domains))
	}

	keys := map[string]bool{}
	checkKey := func(kind, key string) {
		t.Helper()
		if key == "" {
			t.Fatalf("%s has an empty natural key", kind)
		}
		compound := kind + ":" + key
		if keys[compound] {
			t.Fatalf("duplicate %s key %q", kind, key)
		}
		keys[compound] = true
	}
	for _, item := range snapshot.Hosts {
		checkKey("host", item.NaturalKey())
	}
	secretLike := regexp.MustCompile(`(?i)(secret|token|passw|api.?key|auth)`)
	for _, item := range snapshot.Services {
		checkKey("service", item.NaturalKey())
		for key := range item.RawLabels {
			if secretLike.MatchString(key) {
				t.Fatalf("fixture service %q contains secret-like label %q", item.Name, key)
			}
		}
	}
	if got := snapshot.Services[11].RawLabels["homedex.demo.example-egress"]; got != "203.0.113.10" {
		t.Fatalf("external-IP masking fixture = %q, want documentation address", got)
	}
	for _, item := range snapshot.Ports {
		checkKey("port", item.NaturalKey())
	}
	for _, item := range snapshot.Routes {
		checkKey("route", item.NaturalKey())
	}
	for _, item := range snapshot.Certs {
		checkKey("cert", item.NaturalKey())
	}
	for _, item := range snapshot.Domains {
		checkKey("domain", item.NaturalKey())
	}
}

func TestFakeLabRoutesExerciseResolutionOutcomes(t *testing.T) {
	snapshot := fakeLabSnapshot()
	const connectorID = 1
	inv := resolve.Inventory{}
	for _, host := range snapshot.Hosts {
		inv.Hosts = append(inv.Hosts, resolve.Host{Ref: resolve.EntityRef{ConnectorID: connectorID, Key: host.NaturalKey()}, Name: host.Name, Address: host.Address})
	}
	for _, service := range snapshot.Services {
		inv.Services = append(inv.Services, resolve.Service{
			Ref:      resolve.EntityRef{ConnectorID: connectorID, Key: service.NaturalKey()},
			HostRef:  resolve.EntityRef{ConnectorID: connectorID, Key: service.HostKey},
			Name:     service.Name,
			Networks: service.Networks,
		})
	}
	for _, port := range snapshot.Ports {
		inv.Ports = append(inv.Ports, resolve.Port{
			ServiceRef:    resolve.EntityRef{ConnectorID: connectorID, Key: port.ServiceKey},
			HostRef:       resolve.EntityRef{ConnectorID: connectorID, Key: port.HostKey},
			Number:        port.Number,
			ContainerPort: port.ContainerPort,
			Published:     port.Published,
		})
	}
	routes := resolve.Routes(snapshot.Routes, inv)
	statuses := map[string]int{}
	confidences := map[string]int{}
	for _, item := range routes {
		statuses[item.Status]++
		confidences[item.ResolveConfidence]++
	}
	if statuses["ok"] != 9 || statuses["broken"] != 1 {
		t.Fatalf("route statuses = %#v, want 9 ok and 1 broken", statuses)
	}
	if confidences["high"] != 8 || confidences["medium"] != 1 || confidences["none"] != 1 {
		t.Fatalf("route confidences = %#v", confidences)
	}
}

func TestSeedCreatesQueryableInventory(t *testing.T) {
	dataDir := t.TempDir()
	stats, err := seed(context.Background(), dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Services != 12 || stats.Routes != 10 {
		t.Fatalf("stats = %#v", stats)
	}
	if _, err = seed(context.Background(), dataDir, false); err == nil {
		t.Fatal("second seed without --reset unexpectedly succeeded")
	}

	st, err := store.Open(context.Background(), filepath.Join(dataDir, "homedex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	assertCount(t, st, "services", 12)
	assertCount(t, st, "ports", 16)
	assertCount(t, st, "routes", 10)
	var broken, medium, enabled int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM routes WHERE status='broken'`).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM routes WHERE resolve_confidence='medium'`).Scan(&medium); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT enabled FROM connectors WHERE name='Fake lab fixture'`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if broken != 1 || medium != 1 || enabled != 0 {
		t.Fatalf("broken=%d medium=%d connector enabled=%d", broken, medium, enabled)
	}
	info, err := os.Stat(filepath.Join(dataDir, "instance.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("instance key mode = %o, want 600", info.Mode().Perm())
	}
}

func assertCount(t *testing.T, st *store.Store, table string, want int) {
	t.Helper()
	var got int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
