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
	if len(snap.Certs) != 1 || snap.Certs[0].NotAfter.IsZero() || snap.Certs[0].NaturalKey() != "tls:paperless.example.com:443" {
		t.Fatalf("certs: %#v", snap.Certs)
	}
}

// TestScanParsesSpaceSeparatedCertExpiry exercises NPM's real-world date format
// ("2025-08-01 00:00:00", which is NOT RFC3339). Before the fix the RFC3339
// parse failed silently and NotAfter was stored as the zero time.
func TestScanParsesSpaceSeparatedCertExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tokens":
			_, _ = w.Write([]byte(`{"token":"t"}`))
		case "/api/nginx/proxy-hosts":
			_, _ = w.Write([]byte(`[]`))
		case "/api/nginx/certificates":
			_, _ = w.Write([]byte(`[{"id":1,"domain_names":["ex.example.com"],"expires_on":"2025-08-01 00:00:00","provider":"letsencrypt"}]`))
		}
	}))
	defer srv.Close()
	raw := connectors.Config{"url": json.RawMessage(`"` + srv.URL + `"`), "email": json.RawMessage(`"r@example.com"`), "password": json.RawMessage(`"secret"`)}
	snap, e := New().Scan(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Certs) != 1 || snap.Certs[0].NotAfter.IsZero() {
		t.Fatalf("space-separated expiry not parsed: %#v", snap.Certs)
	}
	if got := snap.Certs[0].NotAfter; got.Year() != 2025 || got.Month() != 8 || got.Day() != 1 {
		t.Fatalf("unexpected NotAfter: %v", got)
	}
}

func TestParseExpiryLayouts(t *testing.T) {
	for _, s := range []string{
		"2027-03-04T05:06:07Z", // RFC3339
		"2025-08-01 00:00:00",  // NPM space-separated
		"2025-08-01T00:00:00",  // no zone
		"2025-08-01",           // date only
	} {
		if parseExpiry(s).IsZero() {
			t.Errorf("parseExpiry(%q) returned zero time", s)
		}
	}
	if got := parseExpiry("not-a-date"); !got.IsZero() {
		t.Errorf("parseExpiry(garbage) = %v, want zero", got)
	}
}
