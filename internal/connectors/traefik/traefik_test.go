package traefik

import (
	"context"
	"encoding/json"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"net/http"
	"net/http/httptest"
	"os"
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
