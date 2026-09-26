package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

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
	svc := func(name, image, tag string, digests ...string) domain.Service {
		return domain.Service{Key: "docker:nas:" + name, HostKey: "docker:nas", Name: name, Kind: "container", Image: image, Tag: tag, State: "running", RepoDigests: digests}
	}
	applier := engine.New(st, nil)
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
		if item.Name == "unchecked" {
			if item.UpdateStatus != nil || item.CheckedAt != nil {
				t.Fatalf("unchecked service reported %v at %v", *item.UpdateStatus, item.CheckedAt)
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
}
