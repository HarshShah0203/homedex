package caddy

import (
	"context"
	"encoding/json"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestScanRecursesIntoSubroutes(t *testing.T) {
	b, _ := os.ReadFile("testdata/config.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) }))
	defer srv.Close()
	snap, e := New().Scan(context.Background(), connectors.Config{"URL": json.RawMessage(`"` + srv.URL + `"`)})
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Routes) != 1 {
		t.Fatalf("routes=%d %#v", len(snap.Routes), snap.Routes)
	}
	r := snap.Routes[0]
	if r.Domain != "media.example.com" || r.PathPrefix != "/jellyfin/" || r.UpstreamHost != "jellyfin" || r.UpstreamPort != 8096 {
		t.Fatalf("unexpected route: %#v", r)
	}
	// The natural key must be stable: it keys on host+path only and must not
	// embed the dial (here jellyfin:8096) so a raw-IP dial cannot make the key
	// churn on container recreates.
	if r.Key != "caddy:media.example.com:/jellyfin/" {
		t.Fatalf("unexpected key %q", r.Key)
	}
}
