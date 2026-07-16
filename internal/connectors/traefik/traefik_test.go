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
	if len(snap.Routes) != 2 {
		t.Fatalf("routes=%d", len(snap.Routes))
	}
	for _, r := range snap.Routes {
		if r.PathPrefix != "/api" || r.UpstreamHost != "immich-server" || r.UpstreamPort != 2283 || !r.TLS {
			t.Fatalf("unexpected route: %#v", r)
		}
	}
}
