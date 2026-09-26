package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	registryconnector "github.com/HarshShah0203/homedex/internal/connectors/registry"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/imageref"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestServiceListReportsImageUpdateStatus(t *testing.T) {
	const (
		current = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		older   = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	docker, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	registry, _ := st.CreateConnector(ctx, "registry", "Image updates", nil)
	sshHost, _ := st.CreateConnector(ctx, "ssh", "SSH host", nil)
	svc := func(name, image, tag string, digests ...string) domain.Service {
		return domain.Service{Key: "docker:nas:" + name, HostKey: "docker:nas", Name: name, Kind: "container", Image: image, Tag: tag, State: "running", RepoDigests: digests}
	}
	applier := engine.New(st, nil)
	// An SSH host lists containers without registry digests; even when a
	// Docker-found container runs the same tag, they show no status.
	if _, _, err = applier.Apply(ctx, sshHost, domain.Snapshot{
		Hosts:    []domain.Host{{Key: "ssh:media", Name: "media", Kind: "ssh"}},
		Services: []domain.Service{{Key: "ssh:media:ssh-stale", HostKey: "ssh:media", Name: "ssh-stale", Kind: "container", Image: "nginx", Tag: "alpine", State: "running"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, docker, domain.Snapshot{
		Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{
			svc("current", "traefik/whoami", "v1.10.0", "traefik/whoami@"+current),
			svc("stale", "nginx", "alpine", "nginx@"+older),
			svc("local", "myapp", "dev"),
			svc("pinned", "redis", current, "redis@"+current),
			svc("private", "registry.lab.example/app", "1", "registry.lab.example/app@"+current),
			svc("unchecked", "grafana/grafana", "12.0.2", "grafana/grafana@"+current),
		},
	}); err != nil {
		t.Fatal(err)
	}
	checked := time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC)
	if _, _, err = applier.Apply(ctx, registry, domain.Snapshot{ImageUpdates: []domain.ImageUpdate{
		{Ref: "traefik/whoami:v1.10.0", Lookup: imageref.LookupResolved, RemoteDigest: current, CheckedAt: checked},
		{Ref: "nginx:alpine", Lookup: imageref.LookupResolved, RemoteDigest: current, CheckedAt: checked},
		{Ref: "myapp:dev", Lookup: imageref.LookupUnknown, Reason: "The registry has no manifest for this tag.", CheckedAt: checked},
		{Ref: "redis@" + current, Lookup: imageref.LookupPinned, CheckedAt: checked},
		{Ref: "registry.lab.example/app:1", Lookup: imageref.LookupUnknown, Reason: "The registry requires credentials for this image; private registries are not checked.", CheckedAt: checked},
	}}); err != nil {
		t.Fatal(err)
	}
	handler := New(st, NewBroker(), Config{NoAuth: true})
	type listed struct {
		Name          string  `json:"name"`
		State         string  `json:"state"`
		UpdateStatus  *string `json:"update_status"`
		CheckedAt     *string `json:"update_checked_at"`
		Reason        string  `json:"update_reason"`
		LatestDigest  string  `json:"latest_digest"`
		RunningDigest string  `json:"running_digest"`
	}
	fetch := func() []listed {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/services", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var list struct {
			Items []listed `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		return list.Items
	}
	want := map[string]string{"current": imageref.UpToDate, "stale": imageref.UpdateAvailable, "local": imageref.Unknown, "pinned": imageref.Pinned, "private": imageref.Unknown}
	for _, item := range fetch() {
		if item.Name == "unchecked" || item.Name == "ssh-stale" {
			if item.UpdateStatus != nil || item.CheckedAt != nil {
				t.Fatalf("%s reported %v at %v", item.Name, *item.UpdateStatus, item.CheckedAt)
			}
			continue
		}
		if item.UpdateStatus == nil || *item.UpdateStatus != want[item.Name] {
			t.Fatalf("%s status = %v, want %s", item.Name, item.UpdateStatus, want[item.Name])
		}
		if item.CheckedAt == nil || *item.CheckedAt != checked.Format(time.RFC3339Nano) {
			t.Fatalf("%s checked at = %v", item.Name, item.CheckedAt)
		}
		if *item.UpdateStatus == imageref.Unknown && item.Reason == "" {
			t.Fatalf("%s unknown without a reason", item.Name)
		}
		if item.Name == "stale" && (item.LatestDigest != current || item.RunningDigest != older) {
			t.Fatalf("stale digests latest=%s running=%s", item.LatestDigest, item.RunningDigest)
		}
	}

	// Recreating the container on the new image is reflected as soon as Docker
	// is rescanned, without waiting for the next registry check.
	if _, _, err = applier.Apply(ctx, docker, domain.Snapshot{
		Hosts:    []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{svc("stale", "nginx", "alpine", "nginx@"+current)},
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range fetch() {
		switch {
		case item.Name == "stale" && (item.UpdateStatus == nil || *item.UpdateStatus != imageref.UpToDate):
			t.Fatalf("recreated container status = %v", item.UpdateStatus)
		// A container that is gone runs nothing, so it has nothing to update.
		case item.State == "gone" && item.UpdateStatus != nil:
			t.Fatalf("gone container %s reported %s", item.Name, *item.UpdateStatus)
		}
	}

	// A reference the next check no longer covers (here, skipped by the
	// source) is retired and stops reporting, rather than keeping a status
	// nothing checks any more.
	if _, _, err = applier.Apply(ctx, registry, domain.Snapshot{ImageUpdates: []domain.ImageUpdate{
		{Ref: "traefik/whoami:v1.10.0", Lookup: imageref.LookupResolved, RemoteDigest: current, CheckedAt: checked},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, docker, domain.Snapshot{
		Hosts:    []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{svc("stale", "nginx", "alpine", "nginx@"+older), svc("current", "traefik/whoami", "v1.10.0", "traefik/whoami@"+current)},
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range fetch() {
		switch item.Name {
		case "stale":
			if item.UpdateStatus != nil {
				t.Fatalf("retired lookup still reported: %s", *item.UpdateStatus)
			}
		case "current":
			if item.UpdateStatus == nil || *item.UpdateStatus != imageref.UpToDate {
				t.Fatalf("current lookup = %v", item.UpdateStatus)
			}
		}
	}

	// Deleting the source deletes what it found: no badge or filter is left
	// claiming a status nothing checks.
	if err = store.NewConnectorConfigs(st, nil).Delete(ctx, registry); err != nil {
		t.Fatal(err)
	}
	if _, _, err = applier.Apply(ctx, docker, domain.Snapshot{
		Hosts:    []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{svc("stale", "nginx", "alpine", "nginx@"+older), svc("current", "traefik/whoami", "v1.10.0", "traefik/whoami@"+current)},
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range fetch() {
		if item.UpdateStatus != nil || item.CheckedAt != nil {
			t.Fatalf("%s still reports %v (checked %v) after the source was deleted", item.Name, item.UpdateStatus, item.CheckedAt)
		}
	}
}

// recordingTransport answers every request itself, as a registry that wants a
// token would, and records which hosts were asked. Nothing reaches the network.
type recordingTransport struct {
	mu    sync.Mutex
	hosts []string
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.hosts = append(rt.hosts, req.Method+" "+req.URL.Host+req.URL.Path)
	rt.mu.Unlock()
	return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

// Testing an Image updates source before saving it contacts the registries
// the deployed containers use, exactly as a saved source's test and scans do:
// a lab whose egress allows only ghcr.io and lscr.io can add the source.
func TestUnsavedRegistrySourceTestUsesTheDeployedImages(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	docker, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	applier := engine.New(st, nil)
	if _, _, err = applier.Apply(ctx, docker, domain.Snapshot{
		Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{
			{Key: "docker:nas:ha", HostKey: "docker:nas", Name: "ha", Kind: "container", Image: "ghcr.io/home-assistant/home-assistant", Tag: "stable", State: "running"},
			{Key: "docker:nas:sonarr", HostKey: "docker:nas", Name: "sonarr", Kind: "container", Image: "lscr.io/linuxserver/sonarr", Tag: "latest", State: "running"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{7}, 32))
	configs := store.NewConnectorConfigs(st, box)
	kinds := connectors.NewRegistry()
	if err = kinds.Register(registryconnector.New()); err != nil {
		t.Fatal(err)
	}
	handler := New(st, NewBroker(), Config{NoAuth: true, ConnectorConfigs: configs, Registry: kinds, Runner: engine.NewRunner(st, configs, kinds, applier)})

	rt := &recordingTransport{}
	saved := http.DefaultTransport
	http.DefaultTransport = rt
	defer func() { http.DefaultTransport = saved }()
	for _, body := range []string{`{"kind":"registry","name":"Image updates","config":{"exclude":[]}}`, `{"kind":"registry","name":"Image updates"}`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/connectors/test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.hosts) == 0 {
		t.Fatal("no registry was contacted")
	}
	for _, got := range rt.hosts {
		if got != "GET ghcr.io/v2/" && got != "GET lscr.io/v2/" {
			t.Fatalf("unsaved test contacted %s; want only the containers' registries (ghcr.io, lscr.io)", got)
		}
	}
}
