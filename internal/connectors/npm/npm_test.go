package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestScanRefreshesJWTAndParsesLocationsAndCertificates(t *testing.T) {
	var tokens, hosts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tokens":
			n := tokens.Add(1)
			_, _ = fmt.Fprintf(w, `{"token":"token-%d"}`, n)
		case "/api/nginx/proxy-hosts":
			if hosts.Add(1) == 1 {
				w.WriteHeader(401)
				return
			}
			b, _ := os.ReadFile("testdata/proxy-hosts.json")
			_, _ = w.Write(b)
		case "/api/nginx/certificates":
			b, _ := os.ReadFile("testdata/certificates.json")
			_, _ = w.Write(b)
		}
	}))
	defer srv.Close()
	raw := connectors.Config{"url": json.RawMessage(`"` + srv.URL + `"`), "email": json.RawMessage(`"reader@example.com"`), "password": json.RawMessage(`"secret"`)}
	snap, e := New().Scan(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	if tokens.Load() != 2 {
		t.Fatalf("token requests=%d, want refresh", tokens.Load())
	}
	if len(snap.Routes) != 2 || snap.Routes[1].PathPrefix != "/socket" || snap.Routes[1].UpstreamHost != "paperless-ws" || !snap.Routes[0].TLS || !snap.Routes[1].TLS {
		t.Fatalf("routes: %#v", snap.Routes)
	}
	if len(snap.Certs) != 1 || snap.Certs[0].NotAfter.IsZero() {
		t.Fatalf("certs: %#v", snap.Certs)
	}
}
