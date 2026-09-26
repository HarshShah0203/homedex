package traefik

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestScanRecordedMultiHostRouter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "routers.json"
		if r.URL.Path == "/api/http/services" {
			name = "services.json"
		}
		if r.URL.Path == "/api/version" {
			_, _ = w.Write([]byte(`{"Version":"3.3"}`))
			return
		}
		b, _ := os.ReadFile("testdata/" + name)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	c := New()
	snap, e := c.Scan(context.Background(), connectors.Config{"URL": json.RawMessage(`"` + srv.URL + `"`)})
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Routes) != 3 {
		t.Fatalf("routes=%d", len(snap.Routes))
	}
	immich := 0
	for _, r := range snap.Routes {
		// The natural key must be stable across container recreates: it must
		// not embed the (volatile) upstream host/IP. files.example.com's
		// upstream is 10.0.0.5, immich's is immich-server — neither may leak.
		if strings.Contains(r.Key, r.UpstreamHost) {
			t.Fatalf("route key %q leaks upstream host %q", r.Key, r.UpstreamHost)
		}
		switch r.Domain {
		case "photos.example.com", "gallery.example.com":
			// The router references service "immich" while the API names it
			// "immich@docker"; resolution must bridge the provider suffix.
			immich++
			if r.PathPrefix != "/api" || r.UpstreamHost != "immich-server" || r.UpstreamPort != 2283 || !r.TLS {
				t.Fatalf("unexpected immich route: %#v", r)
			}
		case "files.example.com":
			if r.PathPrefix != "/" || r.UpstreamHost != "10.0.0.5" || r.UpstreamPort != 8443 || r.TLS {
				t.Fatalf("unexpected files route: %#v", r)
			}
		default:
			t.Fatalf("unexpected domain: %#v", r)
		}
	}
	if immich != 2 {
		t.Fatalf("immich routes=%d", immich)
	}
}

// A Traefik API protected by basic auth or a header credential must never
// hand that credential to wherever a redirect points; an unprotected one still
// follows redirects as before.
func TestCredentialIsNotSentThroughARedirect(t *testing.T) {
	var mu sync.Mutex
	var landed []string
	arrivals := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), landed...)
	}
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, basic := r.BasicAuth()
		mu.Lock()
		landed = append(landed, fmt.Sprintf("header=%t basic=%t", r.Header.Get("X-Api-Key") != "", basic))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"Version":"3.3"}`))
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	for name, extra := range map[string]string{
		"header":     `,"header":"X-Api-Key","header_value":"traefik-key"`,
		"basic auth": `,"username":"homedex","password":"traefik-password"`,
	} {
		cfg := connectors.Config{}
		if err := json.Unmarshal([]byte(`{"url":"`+srv.URL+`"`+extra+`}`), &cfg); err != nil {
			t.Fatal(err)
		}
		err := New().Validate(context.Background(), cfg)
		if err == nil || err.Error() != "Traefik API returned 302 Found; Homedex does not follow a redirect with credentials, check the URL" {
			t.Errorf("%s: Validate = %v, want the redirect refused", name, err)
		}
	}
	if got := arrivals(); len(got) != 0 {
		t.Fatalf("the redirect target received credentialed requests: %v", got)
	}
	if err := New().Validate(context.Background(), connectors.Config{"url": json.RawMessage(`"` + srv.URL + `"`)}); err != nil {
		t.Fatalf("an unauthenticated API behind a redirect: %v", err)
	}
	if got := arrivals(); len(got) != 1 || got[0] != "header=false basic=false" {
		t.Fatalf("the unauthenticated redirect landed as %v", got)
	}
}
