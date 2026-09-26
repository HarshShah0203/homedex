package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestHostListCarriesAliasesAndReportedLastSeen(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	docker, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	tailnet, _ := st.CreateConnector(ctx, "tailscale", "Tailnet", nil)
	applier := engine.New(st, nil)
	if _, _, err = applier.Apply(ctx, docker, domain.Snapshot{Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker", Address: "10.0.0.2"}}}); err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	if _, _, err = applier.Apply(ctx, tailnet, domain.Snapshot{Hosts: []domain.Host{{Key: "tailscale:n1", Name: "NAS", Kind: domain.HostKindTailscale, Address: "100.64.0.5", Aliases: []string{"nas.tail1234.ts.net", "fd7a:115c:a1e0::5"}, ReportedLastSeen: &seen}}}); err != nil {
		t.Fatal(err)
	}
	handler := New(st, NewBroker(), Config{NoAuth: true})
	get := func(path string, out any) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	var hosts struct {
		Items []struct {
			Kind             string          `json:"kind"`
			Aliases          json.RawMessage `json:"aliases"`
			ReportedLastSeen *string         `json:"reported_last_seen"`
		} `json:"items"`
	}
	get("/api/hosts", &hosts)
	byKind := map[string]int{}
	for i, h := range hosts.Items {
		byKind[h.Kind] = i
	}
	dockerHost, device := hosts.Items[byKind["docker"]], hosts.Items[byKind["tailscale"]]
	if string(dockerHost.Aliases) != "[]" || dockerHost.ReportedLastSeen != nil {
		t.Fatalf("docker host aliases=%s reported=%v, want [] and null", dockerHost.Aliases, dockerHost.ReportedLastSeen)
	}
	var aliases []string
	_ = json.Unmarshal(device.Aliases, &aliases)
	if !reflect.DeepEqual(aliases, []string{"fd7a:115c:a1e0::5", "nas.tail1234.ts.net"}) || device.ReportedLastSeen == nil || *device.ReportedLastSeen != "2026-09-20T08:00:00Z" {
		t.Fatalf("tailnet host aliases=%s reported=%v", device.Aliases, device.ReportedLastSeen)
	}

	var found struct {
		Items []struct {
			EntityType string `json:"entity_type"`
			Title      string `json:"title"`
		} `json:"items"`
	}
	get("/api/search?q=nas.tail1234.ts.net", &found)
	if len(found.Items) != 1 || found.Items[0].EntityType != "host" || found.Items[0].Title != "NAS" {
		t.Fatalf("search by MagicDNS name = %+v", found.Items)
	}
}

func TestHostListAndDetailCarryPowerStateAndParent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pve, _ := st.CreateConnector(ctx, "proxmox", "Proxmox", nil)
	if _, _, err = engine.New(st, nil).Apply(ctx, pve, domain.Snapshot{Hosts: []domain.Host{
		{Key: "node:pve1", Name: "pve1", Kind: domain.HostKindProxmoxNode, Address: "192.0.2.2", PowerState: "online"},
		{Key: "qemu:101", Name: "web", Kind: domain.HostKindVM, Address: "192.0.2.21", PowerState: "stopped", ParentKey: "node:pve1"},
	}}); err != nil {
		t.Fatal(err)
	}
	handler := New(st, NewBroker(), Config{NoAuth: true})
	get := func(path string, out any) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	type host struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		PowerState string `json:"power_state"`
		ParentID   *int64 `json:"parent_id"`
		Parent     string `json:"parent"`
	}
	var hosts struct {
		Items []host `json:"items"`
	}
	get("/api/hosts", &hosts)
	byName := map[string]host{}
	for _, h := range hosts.Items {
		byName[h.Name] = h
	}
	node, guest := byName["pve1"], byName["web"]
	if node.PowerState != "online" || node.ParentID != nil || node.Parent != "" {
		t.Fatalf("node = %+v", node)
	}
	if guest.PowerState != "stopped" || guest.ParentID == nil || *guest.ParentID != node.ID || guest.Parent != "pve1" {
		t.Fatalf("guest = %+v, want stopped on pve1 (%d)", guest, node.ID)
	}
	var detail struct {
		Entity map[string]any `json:"entity"`
	}
	get("/api/entities/host/"+strconv.FormatInt(guest.ID, 10), &detail)
	if detail.Entity["power_state"] != "stopped" || detail.Entity["parent_name"] != "pve1" || detail.Entity["parent_id"] != float64(node.ID) {
		t.Fatalf("host detail = %v", detail.Entity)
	}
	var archive struct {
		Hosts []host `json:"hosts"`
	}
	get("/api/export/json", &archive)
	exported := false
	for _, h := range archive.Hosts {
		if h.Name == "web" {
			exported = h.PowerState == "stopped" && h.Parent == "pve1" && h.ParentID != nil && *h.ParentID == node.ID
		}
	}
	if !exported {
		t.Fatalf("exported hosts = %+v, want web stopped on pve1", archive.Hosts)
	}
}
