package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/imageref"
	"github.com/HarshShah0203/homedex/internal/store"
)

const (
	whoamiOld = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	whoamiNew = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	whoamiV3  = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	nginxNow  = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	nginxNext = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
)

func imageLab(whoamiOldDigests, nginxDigests []string) domain.Snapshot {
	svc := func(key, name, image, tag string, digests []string) domain.Service {
		return domain.Service{Key: key, HostKey: "docker:nas", Name: name, Kind: "container", Image: image, Tag: tag, Digest: "sha256:id-" + name, State: "running", RepoDigests: digests}
	}
	return domain.Snapshot{
		Hosts: []domain.Host{{Key: "docker:nas", Name: "nas", Kind: "docker"}},
		Services: []domain.Service{
			svc("docker:nas:whoami-old", "whoami-old", "traefik/whoami", "v1.10.0", whoamiOldDigests),
			svc("docker:nas:whoami-new", "whoami-new", "traefik/whoami", "v1.10.0", []string{"traefik/whoami@" + whoamiNew}),
			svc("docker:nas:web", "web", "nginx", "alpine", nginxDigests),
			svc("docker:nas:local", "local", "myapp", "dev", nil),
			svc("docker:nas:cache", "cache", "redis", whoamiNew, []string{"redis@" + whoamiNew}),
		},
	}
}

type imageChange struct {
	entityID int64
	summary  string
	diff     map[string]any
}

func imageChanges(t *testing.T, st *store.Store, runID int64) []imageChange {
	t.Helper()
	rows, err := st.DB().Query(`SELECT entity_id,summary,diff FROM changes WHERE entity_type='image' AND scan_run_id=? ORDER BY id`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []imageChange
	for rows.Next() {
		var c imageChange
		var diff string
		if err = rows.Scan(&c.entityID, &c.summary, &diff); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(diff), &c.diff); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func TestImageUpdatesFileOneChangePerNewDigestAndNeverRepeat(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dockerID, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	registryID, _ := st.CreateConnector(ctx, "registry", "Image updates", nil)
	a := New(st, nil)
	if _, _, err = a.Apply(ctx, dockerID, imageLab([]string{"traefik/whoami@" + whoamiOld}, []string{"nginx@" + nginxNow})); err != nil {
		t.Fatal(err)
	}
	checked := time.Date(2026, 9, 26, 6, 0, 0, 0, time.UTC)
	lookups := func(whoami, nginx domain.ImageUpdate) domain.Snapshot {
		return domain.Snapshot{ImageUpdates: []domain.ImageUpdate{
			whoami, nginx,
			{Ref: "myapp:dev", Lookup: imageref.LookupUnknown, Reason: "The registry requires credentials for this image; private registries are not checked.", CheckedAt: checked},
			{Ref: "redis@" + whoamiNew, Lookup: imageref.LookupPinned, CheckedAt: checked},
		}}
	}
	resolved := func(ref, digest string) domain.ImageUpdate {
		return domain.ImageUpdate{Ref: ref, Lookup: imageref.LookupResolved, RemoteDigest: digest, CheckedAt: checked}
	}

	// whoami-old runs an older build of v1.10.0 than the registry serves now.
	run, n, err := a.Apply(ctx, registryID, lookups(resolved("traefik/whoami:v1.10.0", whoamiNew), resolved("nginx:alpine", nginxNow)))
	if err != nil {
		t.Fatal(err)
	}
	got := imageChanges(t, st, run)
	if n != 1 || len(got) != 1 || got[0].summary != "Update available for traefik/whoami:v1.10.0" {
		t.Fatalf("first registry scan: n=%d changes=%+v", n, got)
	}
	if d := got[0].diff["digest"].(map[string]any); d["before"] != imageref.Short(whoamiOld) || d["after"] != imageref.Short(whoamiNew) || got[0].diff["services"] != float64(1) {
		t.Fatalf("diff = %v", got[0].diff)
	}
	var entityRef string
	if err = st.DB().QueryRow(`SELECT image_ref FROM image_updates WHERE id=?`, got[0].entityID).Scan(&entityRef); err != nil || entityRef != "traefik/whoami:v1.10.0" {
		t.Fatalf("change entity = %q, %v", entityRef, err)
	}

	// The same answer again is not news, and neither is a rate limit: the last
	// good answer is kept, with the failure noted.
	if _, n, err = a.Apply(ctx, registryID, lookups(resolved("traefik/whoami:v1.10.0", whoamiNew), resolved("nginx:alpine", nginxNow))); err != nil || n != 0 {
		t.Fatalf("repeat scan: n=%d err=%v", n, err)
	}
	limited := domain.ImageUpdate{Ref: "traefik/whoami:v1.10.0", Lookup: imageref.LookupUnknown, Reason: "The registry rate limited this check (HTTP 429).", Transient: true, CheckedAt: checked.Add(24 * time.Hour)}
	if _, n, err = a.Apply(ctx, registryID, lookups(limited, resolved("nginx:alpine", nginxNow))); err != nil || n != 0 {
		t.Fatalf("rate-limited scan: n=%d err=%v", n, err)
	}
	var lookup, remote, reason, checkedAt string
	if err = st.DB().QueryRow(`SELECT lookup,remote_digest,reason,checked_at FROM image_updates WHERE image_ref='traefik/whoami:v1.10.0'`).Scan(&lookup, &remote, &reason, &checkedAt); err != nil {
		t.Fatal(err)
	}
	if lookup != imageref.LookupResolved || remote != whoamiNew || !strings.HasPrefix(reason, "Last check failed:") || checkedAt != checked.Format(time.RFC3339Nano) {
		t.Fatalf("transient failure replaced the last answer: %s %s %q %s", lookup, remote, reason, checkedAt)
	}

	// A new release behind which both whoami containers now sit: one entry.
	run, n, err = a.Apply(ctx, registryID, lookups(resolved("traefik/whoami:v1.10.0", whoamiV3), resolved("nginx:alpine", nginxNow)))
	if err != nil {
		t.Fatal(err)
	}
	if got = imageChanges(t, st, run); n != 1 || len(got) != 1 || got[0].diff["services"] != float64(2) {
		t.Fatalf("second release: n=%d changes=%+v", n, got)
	}

	// nginx was recreated on the new image before the registry was asked: the
	// registry's new digest is already running, so there is nothing to report.
	if _, _, err = a.Apply(ctx, dockerID, imageLab([]string{"traefik/whoami@" + whoamiOld}, []string{"nginx@" + nginxNext})); err != nil {
		t.Fatal(err)
	}
	if _, n, err = a.Apply(ctx, registryID, lookups(resolved("traefik/whoami:v1.10.0", whoamiV3), resolved("nginx:alpine", nginxNext))); err != nil || n != 0 {
		t.Fatalf("already-current image filed a change: n=%d err=%v", n, err)
	}

	// References no container runs any more are dropped.
	if _, _, err = a.Apply(ctx, registryID, domain.Snapshot{ImageUpdates: []domain.ImageUpdate{resolved("traefik/whoami:v1.10.0", whoamiV3)}}); err != nil {
		t.Fatal(err)
	}
	var refs int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM image_updates`).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("image_updates rows = %d, %v", refs, err)
	}
	// A Docker scan never owns lookups, so it never removes them.
	if _, _, err = a.Apply(ctx, dockerID, imageLab([]string{"traefik/whoami@" + whoamiOld}, []string{"nginx@" + nginxNext})); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM image_updates`).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("docker scan touched image_updates: %d, %v", refs, err)
	}
	var total int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM changes WHERE entity_type='image'`).Scan(&total); err != nil || total != 2 {
		t.Fatalf("image change entries = %d, want 2", total)
	}
}

func TestImageUpdatesRejectMalformedLookups(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	registryID, _ := st.CreateConnector(ctx, "registry", "Image updates", nil)
	a := New(st, nil)
	if _, _, err = a.Apply(ctx, registryID, domain.Snapshot{ImageUpdates: []domain.ImageUpdate{{Ref: " "}}}); err == nil {
		t.Fatal("empty reference accepted")
	}
	if _, _, err = a.Apply(ctx, registryID, domain.Snapshot{ImageUpdates: []domain.ImageUpdate{
		{Ref: "nginx:1", Lookup: imageref.LookupResolved, RemoteDigest: "sha256:short"},
		{Ref: "nginx:2", Lookup: "bogus", RemoteDigest: whoamiNew},
	}}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DB().Query(`SELECT lookup,remote_digest,reason FROM image_updates ORDER BY image_ref`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var lookup, remote, reason string
		if err = rows.Scan(&lookup, &remote, &reason); err != nil {
			t.Fatal(err)
		}
		if lookup != imageref.LookupUnknown || remote != "" {
			t.Fatalf("malformed lookup stored as %s %q (%s)", lookup, remote, reason)
		}
	}
}

// Filling repo_digests for the first time after an upgrade, or a pull that
// only adds a second recorded digest, must not reach the change feed.
func TestRepoDigestsAreStoredButNeverDiffed(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dockerID, _ := st.CreateConnector(ctx, "docker", "Docker", nil)
	a := New(st, nil)
	if _, _, err = a.Apply(ctx, dockerID, imageLab(nil, nil)); err != nil {
		t.Fatal(err)
	}
	_, n, err := a.Apply(ctx, dockerID, imageLab([]string{"traefik/whoami@" + whoamiOld}, []string{"nginx@" + nginxNow, "nginx@" + nginxNow, "docker.io/library/nginx@" + nginxNext}))
	if err != nil || n != 0 {
		t.Fatalf("repo digests produced changes: n=%d err=%v", n, err)
	}
	var stored string
	if err = st.DB().QueryRow(`SELECT repo_digests FROM services WHERE natural_key='docker:nas:web'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `["docker.io/library/nginx@`+nginxNext+`","nginx@`+nginxNow+`"]` {
		t.Fatalf("repo_digests = %s", stored)
	}
	if err = st.DB().QueryRow(`SELECT repo_digests FROM services WHERE natural_key='docker:nas:local'`).Scan(&stored); err != nil || stored != "[]" {
		t.Fatalf("empty repo_digests = %q, %v", stored, err)
	}
}

func TestRunnerHandsTheRegistryTheDeployedReferences(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	defer st.Close()
	now := "2026-01-01T00:00:00Z"
	for _, row := range [][4]string{
		{"a", "traefik/whoami", "v1.10.0", "running"},
		{"b", "traefik/whoami", "v1.10.0", "exited"},
		{"c", "nginx", "", "running"},
		{"d", "redis", whoamiNew, "running"},
		{"e", "old/app", "1", "gone"},
		{"f", "", "", "running"},
	} {
		if _, err := st.DB().Exec(`INSERT INTO services(name,kind,image,tag,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES(?, 'container',?,?,?,?,?,?,?,?)`, row[0], row[1], row[2], row[3], now, now, row[0], now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().Exec(`INSERT INTO services(name,kind,image,tag,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES('manual','manual','manual/app','1','active',?,?,'manual:x',?,?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Store: st}
	cfg := connectors.Config{"images": json.RawMessage(`["attacker.example/extra:1"]`)}
	runner.addImageTargets(ctx, "registry", cfg)
	var got []string
	_ = json.Unmarshal(cfg["images"], &got)
	want := []string{"nginx", "redis@" + whoamiNew, "traefik/whoami:v1.10.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("images = %v, want %v", got, want)
	}
	other := connectors.Config{}
	runner.addImageTargets(ctx, "tlsprobe", other)
	if _, ok := other["images"]; ok {
		t.Fatal("images injected into another connector kind")
	}
}
