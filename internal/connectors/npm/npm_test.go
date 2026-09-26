package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func npmConfig(url string) connectors.Config {
	return connectors.Config{"url": json.RawMessage(`"` + url + `"`), "email": json.RawMessage(`"a@b.c"`), "password": json.RawMessage(`"npm-password"`)}
}

// The token POST carries the NPM password, so a redirect must not resend it
// anywhere, and Validate must fail exactly as Scan does instead of passing a
// source every scan then rejects.
func TestTokenPasswordIsNotSentThroughARedirect(t *testing.T) {
	var landed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed.Add(1)
		_, _ = w.Write([]byte(`{"token":"t"}`))
	}))
	defer elsewhere.Close()
	// Both servers listen on 127.0.0.1; naming the target localhost makes the
	// redirect leave the configured host.
	other := strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other+r.URL.Path, code)
		}))
		want := fmt.Sprintf("NPM token API returned %d %s; Homedex does not follow this redirect with credentials, check the URL", code, http.StatusText(code))
		if err := New().Validate(context.Background(), npmConfig(srv.URL)); err == nil || err.Error() != want {
			t.Errorf("%d: Validate = %v, want %q", code, err, want)
		}
		if _, err := New().Scan(context.Background(), npmConfig(srv.URL)); err == nil || err.Error() != want {
			t.Errorf("%d: Scan = %v, want %q", code, err, want)
		}
		srv.Close()
	}
	if n := landed.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests carrying the NPM password", n)
	}
}

func TestTokenRejectsAFailedOrMalformedAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		write func(http.ResponseWriter)
		want  string
	}{
		"401":       {func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) }, "NPM token API returned 401 Unauthorized"},
		"malformed": {func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{"token":`)) }, ""},
		"html":      {func(w http.ResponseWriter) { _, _ = w.Write([]byte(`<html>login</html>`)) }, ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/tokens" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("%s: request %s %s %q", name, r.Method, r.URL.Path, r.Header.Get("Content-Type"))
			}
			tc.write(w)
		}))
		err := New().Validate(context.Background(), npmConfig(srv.URL))
		if err == nil || (tc.want != "" && err.Error() != tc.want) {
			t.Errorf("%s: Validate = %v, want %q", name, err, tc.want)
		}
		srv.Close()
	}
}
