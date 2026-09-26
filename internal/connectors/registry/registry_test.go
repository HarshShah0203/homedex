package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/imageref"
)

const (
	indexDigest    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	manifestDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	anonToken      = "anonymous-pull-token"
)

// fakeRegistry records every request it receives so tests can assert what
// was (and was not) contacted.
type fakeRegistry struct {
	*httptest.Server
	mu  sync.Mutex
	log []string
}

func (f *fakeRegistry) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func newRegistry(t *testing.T, handler http.HandlerFunc) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.log = append(f.log, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if strings.Contains(r.URL.Path, "/blobs/") {
			t.Errorf("blob requested: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "" && auth != "Bearer "+anonToken {
			t.Errorf("unexpected credentials sent: %q", auth)
		}
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// hubLike answers like Docker Hub: manifests need an anonymous bearer token
// from the registry's own /token endpoint; tags maps "path:tag" to a digest.
func hubLike(t *testing.T, tags map[string]string, tokenRequests *atomic.Int32) *fakeRegistry {
	var f *fakeRegistry
	f = newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenRequests.Add(1)
			if r.Header.Get("Authorization") != "" {
				t.Errorf("token request carried credentials")
			}
			scope := r.URL.Query().Get("scope")
			if !strings.HasPrefix(scope, "repository:") || !strings.HasSuffix(scope, ":pull") || r.URL.Query().Get("service") != "registry.test" {
				t.Errorf("token request scope=%q service=%q", scope, r.URL.Query().Get("service"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": anonToken, "expires_in": 300})
			return
		}
		name, tag, ok := manifestPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+anonToken {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="registry.test",scope="repository:%s:pull"`, f.URL, name))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for _, want := range []string{"application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.docker.distribution.manifest.v2+json", "application/vnd.oci.image.manifest.v1+json"} {
			if !strings.Contains(r.Header.Get("Accept"), want) {
				t.Errorf("Accept %q lacks %s", r.Header.Get("Accept"), want)
			}
		}
		digest, ok := tags[name+":"+tag]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
	})
	return f
}

func manifestPath(path string) (string, string, bool) {
	rest, ok := strings.CutPrefix(path, "/v2/")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, "/manifests/")
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+len("/manifests/"):], true
}

func connectorFor(registries map[string]string) *Connector {
	return &Connector{
		baseURL:  func(domain string) string { return registries[domain] },
		insecure: true,
		now:      func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) },
	}
}

func cfgWith(images []string, extra map[string]any) connectors.Config {
	cfg := connectors.Config{}
	b, _ := json.Marshal(images)
	cfg["images"] = b
	for k, v := range extra {
		b, _ = json.Marshal(v)
		cfg[k] = b
	}
	return cfg
}

func byRef(t *testing.T, snap domain.Snapshot) map[string]domain.ImageUpdate {
	t.Helper()
	out := map[string]domain.ImageUpdate{}
	for _, u := range snap.ImageUpdates {
		if _, dup := out[u.Ref]; dup {
			t.Fatalf("duplicate result for %s", u.Ref)
		}
		out[u.Ref] = u
	}
	return out
}

func TestScanResolvesTagsThroughAnAnonymousTokenChallenge(t *testing.T) {
	var tokens atomic.Int32
	hub := hubLike(t, map[string]string{"library/nginx:1.27": indexDigest, "library/nginx:1.26": manifestDigest}, &tokens)
	c := connectorFor(map[string]string{"docker.io": hub.URL})
	// Two spellings of one tag share one lookup; a second tag of the same
	// repository reuses the cached pull token.
	snap, err := c.Scan(context.Background(), cfgWith([]string{"nginx:1.27", "docker.io/library/nginx:1.27", "nginx:1.26"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(t, snap)
	for _, ref := range []string{"nginx:1.27", "docker.io/library/nginx:1.27"} {
		if u := got[ref]; u.Lookup != imageref.LookupResolved || u.RemoteDigest != indexDigest || u.Transient || u.CheckedAt.IsZero() {
			t.Fatalf("%s = %+v", ref, u)
		}
	}
	if u := got["nginx:1.26"]; u.Lookup != imageref.LookupResolved || u.RemoteDigest != manifestDigest {
		t.Fatalf("nginx:1.26 = %+v", u)
	}
	if n := tokens.Load(); n != 1 {
		t.Fatalf("token requests = %d, want 1", n)
	}
	heads := 0
	for _, req := range hub.requests() {
		if strings.HasPrefix(req, "GET /v2/") {
			t.Fatalf("manifest fetched with GET although HEAD answered: %s", req)
		}
		if strings.HasPrefix(req, "HEAD ") {
			heads++
		}
	}
	// One unauthenticated and one authenticated HEAD per distinct tag.
	if heads != 4 {
		t.Fatalf("HEAD requests = %d (%v), want 4", heads, hub.requests())
	}
}

func TestScanMapsRegistryAnswersToOutcomes(t *testing.T) {
	var limitedHits atomic.Int32
	open := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		_, tag, _ := manifestPath(r.URL.Path)
		switch tag {
		case "gone":
			w.WriteHeader(http.StatusNotFound)
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
		case "broken":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "private":
			w.Header().Set("WWW-Authenticate", `Basic realm="Registry"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "teapot":
			w.WriteHeader(http.StatusTeapot)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	limited := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		limitedHits.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := connectorFor(map[string]string{"open.test": open.URL, "limited.test": limited.URL})
	snap, err := c.Scan(context.Background(), cfgWith([]string{
		"open.test/app:gone", "open.test/app:forbidden", "open.test/app:broken", "open.test/app:private", "open.test/app:teapot",
		"limited.test/a:1", "limited.test/b:1", "limited.test/c:1",
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(t, snap)
	want := map[string]bool{ // ref -> transient
		"open.test/app:gone": false, "open.test/app:forbidden": false, "open.test/app:private": false, "open.test/app:teapot": false,
		"open.test/app:broken": true,
	}
	for ref, isTransient := range want {
		u := got[ref]
		if u.Lookup != imageref.LookupUnknown || u.Transient != isTransient || u.Reason == "" || u.RemoteDigest != "" {
			t.Errorf("%s = %+v", ref, u)
		}
	}
	if !strings.Contains(got["open.test/app:private"].Reason, "credentials") {
		t.Errorf("private reason = %q", got["open.test/app:private"].Reason)
	}
	// After a 429 the rest of that registry's tags are skipped, not hammered.
	for _, ref := range []string{"limited.test/a:1", "limited.test/b:1", "limited.test/c:1"} {
		if u := got[ref]; u.Lookup != imageref.LookupUnknown || !u.Transient {
			t.Errorf("%s = %+v", ref, u)
		}
	}
	if n := limitedHits.Load(); n > concurrency {
		t.Fatalf("rate-limited registry received %d requests", n)
	}
}

func TestScanFallsBackToGetOnlyWhenHeadCannotAnswer(t *testing.T) {
	reg := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		_, tag, _ := manifestPath(r.URL.Path)
		switch {
		case tag == "no-head" && r.Method == http.MethodHead:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case tag == "no-head":
			w.Header().Set("Docker-Content-Digest", indexDigest)
		case tag == "headless-digest" && r.Method == http.MethodHead:
			// 200 without the header: GET must supply it.
		case tag == "headless-digest":
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write([]byte(`{"schemaVersion":2}`))
		case tag == "malformed":
			w.Header().Set("Docker-Content-Digest", "sha256:not-a-digest")
			_, _ = w.Write([]byte(`{"schemaVersion":2}`))
		case tag == "wrong-length":
			w.Header().Set("Docker-Content-Digest", "sha256:abc123")
		}
	})
	c := connectorFor(map[string]string{"reg.test": reg.URL})
	snap, err := c.Scan(context.Background(), cfgWith([]string{"reg.test/x:no-head", "reg.test/x:headless-digest", "reg.test/x:malformed", "reg.test/x:wrong-length"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(t, snap)
	if u := got["reg.test/x:no-head"]; u.Lookup != imageref.LookupResolved || u.RemoteDigest != indexDigest {
		t.Fatalf("no-head = %+v", u)
	}
	if u := got["reg.test/x:headless-digest"]; u.Lookup != imageref.LookupResolved || u.RemoteDigest != manifestDigest {
		t.Fatalf("headless-digest = %+v", u)
	}
	// A malformed header is never replaced by hashing the body: unknown, not a guess.
	for _, ref := range []string{"reg.test/x:malformed", "reg.test/x:wrong-length"} {
		if u := got[ref]; u.Lookup != imageref.LookupUnknown || u.RemoteDigest != "" || u.Transient {
			t.Fatalf("%s = %+v", ref, u)
		}
	}
}

func TestScanRejectsMalformedChallengesAndTokens(t *testing.T) {
	var base string
	reg := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			switch r.URL.Query().Get("scope") {
			case "repository:bad-json:pull":
				_, _ = w.Write([]byte(`{"token":`))
			case "repository:spaced:pull":
				_ = json.NewEncoder(w).Encode(map[string]string{"token": "two words"})
			case "repository:denied:pull":
				w.WriteHeader(http.StatusUnauthorized)
			case "repository:access:pull":
				_ = json.NewEncoder(w).Encode(map[string]string{"access_token": anonToken})
			default:
				t.Errorf("unexpected token scope %q", r.URL.Query().Get("scope"))
			}
			return
		}
		name, _, _ := manifestPath(r.URL.Path)
		if r.Header.Get("Authorization") == "Bearer "+anonToken {
			w.Header().Set("Docker-Content-Digest", indexDigest)
			return
		}
		switch name {
		case "unterminated":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+base+`/token`)
		case "no-realm":
			w.Header().Set("WWW-Authenticate", `Bearer service="registry.test"`)
		case "userinfo":
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://user:pass@127.0.0.1/token"`)
		case "negotiate":
			w.Header().Set("WWW-Authenticate", `Negotiate`)
		case "empty":
		case "huge":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+base+`/token",x="`+strings.Repeat("a", maxChallengeBytes)+`"`)
		default:
			// Asks for push as well: only pull may ever be requested.
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+base+`/token",service="registry.test",scope="repository:`+name+`:pull,push"`)
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	base = reg.URL
	c := connectorFor(map[string]string{"reg.test": reg.URL})
	refs := []string{"reg.test/unterminated:1", "reg.test/no-realm:1", "reg.test/userinfo:1", "reg.test/negotiate:1", "reg.test/empty:1", "reg.test/huge:1", "reg.test/bad-json:1", "reg.test/spaced:1", "reg.test/denied:1", "reg.test/access:1"}
	snap, err := c.Scan(context.Background(), cfgWith(refs, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(t, snap)
	for _, ref := range refs[:len(refs)-1] {
		// Broken JSON from a token service is a hiccup, kept transient so a
		// known answer survives it; everything else is a definite unknown.
		wantTransient := ref == "reg.test/bad-json:1"
		if u := got[ref]; u.Lookup != imageref.LookupUnknown || u.Transient != wantTransient || u.Reason == "" {
			t.Errorf("%s = %+v", ref, u)
		}
	}
	// Some token services answer access_token instead of token.
	if u := got["reg.test/access:1"]; u.Lookup != imageref.LookupResolved || u.RemoteDigest != indexDigest {
		t.Fatalf("access_token = %+v", u)
	}
}

func TestScanRefusesPlainHTTPTokenServicesOutsideTests(t *testing.T) {
	reg := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="http://127.0.0.1:1/token",service="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := connectorFor(map[string]string{"reg.test": reg.URL})
	c.insecure = false
	snap, err := c.Scan(context.Background(), cfgWith([]string{"reg.test/app:1"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if u := snap.ImageUpdates[0]; u.Lookup != imageref.LookupUnknown || !strings.Contains(u.Reason, "token service") {
		t.Fatalf("plain-HTTP realm accepted: %+v", u)
	}
}

func TestScanFollowsARedirectAndAuthenticatesWhereItLands(t *testing.T) {
	var tokens atomic.Int32
	mirror := hubLike(t, map[string]string{"prod/images/pause:3.9": indexDigest}, &tokens)
	front := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("token leaked to the redirecting registry")
		}
		http.Redirect(w, r, mirror.URL+"/v2/prod/images/pause/manifests/3.9", http.StatusTemporaryRedirect)
	})
	c := connectorFor(map[string]string{"k8s.test": front.URL})
	snap, err := c.Scan(context.Background(), cfgWith([]string{"k8s.test/pause:3.9"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if u := snap.ImageUpdates[0]; u.Lookup != imageref.LookupResolved || u.RemoteDigest != indexDigest {
		t.Fatalf("redirected lookup = %+v", u)
	}
	if n := len(front.requests()); n != 1 {
		t.Fatalf("front registry requests = %v", front.requests())
	}
}

func TestScanDecidesPinnedAndUnlookupableReferencesWithoutNetwork(t *testing.T) {
	reg := newRegistry(t, func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected request %s", r.URL.Path) })
	c := connectorFor(map[string]string{"docker.io": reg.URL, "reg.test": reg.URL})
	snap, err := c.Scan(context.Background(), cfgWith([]string{
		"nginx@" + indexDigest, "nginx:1.27@" + indexDigest, "a1b2c3d4e5f6", "sha256:" + strings.Repeat("a", 64), "bad reference!", "reg.test/private/app:1",
	}, map[string]any{"exclude": []string{"reg.test/private/", "  "}}))
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(t, snap)
	for _, ref := range []string{"nginx@" + indexDigest, "nginx:1.27@" + indexDigest} {
		if got[ref].Lookup != imageref.LookupPinned {
			t.Errorf("%s = %+v", ref, got[ref])
		}
	}
	for _, ref := range []string{"a1b2c3d4e5f6", "sha256:" + strings.Repeat("a", 64), "bad reference!"} {
		if u := got[ref]; u.Lookup != imageref.LookupUnknown || u.Reason == "" {
			t.Errorf("%s = %+v", ref, u)
		}
	}
	if _, ok := got["reg.test/private/app:1"]; ok {
		t.Fatal("excluded reference was reported")
	}
}

func TestScanBoundsConcurrencyAndTagCount(t *testing.T) {
	var inflight, peak atomic.Int32
	reg := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		inflight.Add(-1)
		w.Header().Set("Docker-Content-Digest", indexDigest)
	})
	c := connectorFor(map[string]string{"reg.test": reg.URL})
	var refs []string
	for i := 0; i < maxImages+5; i++ {
		refs = append(refs, fmt.Sprintf("reg.test/app%03d:1", i))
	}
	snap, err := c.Scan(context.Background(), cfgWith(refs, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ImageUpdates) != len(refs) {
		t.Fatalf("results = %d", len(snap.ImageUpdates))
	}
	resolved, skipped := 0, 0
	for _, u := range snap.ImageUpdates {
		switch u.Lookup {
		case imageref.LookupResolved:
			resolved++
		case imageref.LookupUnknown:
			skipped++
		}
	}
	if resolved != maxImages || skipped != 5 {
		t.Fatalf("resolved=%d skipped=%d", resolved, skipped)
	}
	if p := peak.Load(); p > concurrency || p < 2 {
		t.Fatalf("peak concurrency = %d, want 2..%d", p, concurrency)
	}
	if n := len(reg.requests()); n != maxImages {
		t.Fatalf("requests = %d, want one HEAD per tag", n)
	}
}

func TestScanCancellationAbortsButADeadlineKeepsPartialResults(t *testing.T) {
	release := make(chan struct{})
	reg := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	c := connectorFor(map[string]string{"reg.test": reg.URL})
	refs := []string{"reg.test/a:1", "reg.test/b:1", "reg.test/c:1", "reg.test/d:1", "reg.test/e:1", "reg.test/f:1"}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := c.Scan(ctx, cfgWith(refs, nil)); err == nil {
		t.Fatal("cancelled scan returned no error")
	}

	ctx, cancelDeadline := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelDeadline()
	snap, err := c.Scan(ctx, cfgWith(refs, nil))
	if err != nil {
		t.Fatalf("deadline failed the scan: %v", err)
	}
	if len(snap.ImageUpdates) != len(refs) {
		t.Fatalf("results = %+v", snap.ImageUpdates)
	}
	for _, u := range snap.ImageUpdates {
		if u.Lookup != imageref.LookupUnknown || !u.Transient {
			t.Fatalf("timed-out lookup = %+v", u)
		}
	}
}

func TestScanFailsWhenNoRegistryCanBeReached(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	reachable := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", indexDigest)
	})
	c := connectorFor(map[string]string{"down.test": closedURL, "up.test": reachable.URL})
	if _, err := c.Scan(context.Background(), cfgWith([]string{"down.test/a:1", "down.test/b:1", "nginx@" + indexDigest}, nil)); err == nil || !strings.Contains(err.Error(), "no registry could be reached for 2 image tags") {
		t.Fatalf("unreachable registries: %v", err)
	}
	// One reachable registry is enough for the scan to count: the unreachable
	// tags are transient unknowns that keep their previous answers.
	snap, err := c.Scan(context.Background(), cfgWith([]string{"down.test/a:1", "up.test/b:1"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(t, snap)
	if u := got["down.test/a:1"]; u.Lookup != imageref.LookupUnknown || !u.Transient {
		t.Fatalf("unreachable tag = %+v", u)
	}
	if u := got["up.test/b:1"]; u.Lookup != imageref.LookupResolved {
		t.Fatalf("reachable tag = %+v", u)
	}

	// A registry that accepts the connection but never answers is named as a
	// timeout, bounded by timeout_seconds.
	stall := make(chan struct{})
	slow := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stall:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(stall) })
	c = connectorFor(map[string]string{"slow.test": slow.URL, "up.test": reachable.URL})
	started := time.Now()
	snap, err = c.Scan(context.Background(), cfgWith([]string{"slow.test/a:1", "up.test/b:1"}, map[string]any{"timeout_seconds": 1}))
	if err != nil {
		t.Fatal(err)
	}
	if u := byRef(t, snap)["slow.test/a:1"]; !u.Transient || !strings.Contains(u.Reason, "timeout") {
		t.Fatalf("stalled registry = %+v", u)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout not enforced: %s", elapsed)
	}
}

func TestValidatePingsTheRegistryAnonymously(t *testing.T) {
	ok := newRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/" || r.Method != http.MethodGet {
			t.Errorf("unexpected validate request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	down := newRegistry(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	c := connectorFor(map[string]string{"docker.io": ok.URL, "down.test": down.URL})
	if err := c.Validate(context.Background(), connectors.Config{}); err != nil {
		t.Fatalf("no images yet: %v", err)
	}
	// Docker Hub is preferred when containers use it.
	if err := c.Validate(context.Background(), cfgWith([]string{"down.test/app:1", "nginx:1.27"}, nil)); err != nil {
		t.Fatalf("hub preferred: %v", err)
	}
	if err := c.Validate(context.Background(), cfgWith([]string{"down.test/app:1"}, nil)); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("down registry validated: %v", err)
	}
	for _, bad := range []map[string]any{{"timeout_seconds": 0.5}, {"timeout_seconds": 90}, {"timeout_seconds": -1}, {"exclude": "nginx"}} {
		cfg := cfgWith(nil, bad)
		if err := c.Validate(context.Background(), cfg); err == nil {
			t.Errorf("config %v accepted", bad)
		}
	}
}

func TestParseChallenge(t *testing.T) {
	scheme, params, ok := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull,push"`)
	if !ok || scheme != "bearer" || params["realm"] != "https://auth.docker.io/token" || params["service"] != "registry.docker.io" || params["scope"] != "repository:library/nginx:pull,push" {
		t.Fatalf("scheme=%q params=%v ok=%v", scheme, params, ok)
	}
	if _, params, ok = parseChallenge(`Bearer realm="a\"b", service=plain`); !ok || params["realm"] != `a"b` || params["service"] != "plain" {
		t.Fatalf("escapes/tokens: %v %v", params, ok)
	}
	for _, bad := range []string{"", `Bearer realm="open`, `Bearer =x`} {
		if _, _, ok := parseChallenge(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if got := pullScope("repository:library/nginx:pull,push", "x"); got != "repository:library/nginx:pull" {
		t.Fatalf("pullScope = %q", got)
	}
	if got := pullScope("registry:catalog:*", "library/nginx"); got != "repository:library/nginx:pull" {
		t.Fatalf("pullScope fallback = %q", got)
	}
}
