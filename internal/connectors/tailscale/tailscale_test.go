package tailscale

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

var _ connectors.Connector = (*Connector)(nil)

const (
	testAPIKey       = "tskey-api-kFIXTURE1CNTRL-apiKEYsecretVALUE"
	testClientID     = "kFIXTURECLIENT1CNTRL"
	testClientSecret = "tskey-client-kFIXTURECLIENT1CNTRL-clientSECRETvalue"
	defaultDevices   = "/api/v2/tailnet/-/devices"
	oauthTokenPath   = "/api/v2/oauth/token"
)

// credentials is every secret a test hands the connector or the fake API
// issues. No error text may contain any of them.
var credentials = []string{testAPIKey, testClientSecret, "apiKEYsecretVALUE", "clientSECRETvalue", "access-TOKEN"}

func accessToken(n int32) string { return fmt.Sprintf("access-TOKEN-%d", n) }

func cfg(kv ...string) connectors.Config {
	c := connectors.Config{}
	for i := 0; i+1 < len(kv); i += 2 {
		b, _ := json.Marshal(kv[i+1])
		c[kv[i]] = b
	}
	return c
}

func apiKeyConfig(base string, kv ...string) connectors.Config {
	return cfg(append([]string{"base_url", base, "api_key", testAPIKey}, kv...)...)
}

func oauthConfig(base string, kv ...string) connectors.Config {
	return cfg(append([]string{"base_url", base, "oauth_client_id", testClientID, "oauth_client_secret", testClientSecret}, kv...)...)
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/devices.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func serveBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// newAPI mimics api.tailscale.com: token exchanges and device listings reach
// their handlers, and any other request fails the test.
func newAPI(t *testing.T, token, devices http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == oauthTokenPath && token != nil:
			token(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/v2/tailnet/") && strings.HasSuffix(r.URL.Path, "/devices") && devices != nil:
			devices(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// issueTokens is a token endpoint that checks the client-credentials request
// and hands out access-TOKEN-1, access-TOKEN-2, ...
func issueTokens(t *testing.T, n *atomic.Int32, expiresIn int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("token request method %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("token request Content-Type %q", ct)
		}
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Error("token request carries credentials outside the form body")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		want := map[string]string{"grant_type": "client_credentials", "client_id": testClientID, "client_secret": testClientSecret, "scope": "devices:core:read"}
		for k, v := range want {
			if r.PostForm.Get(k) != v {
				t.Errorf("token form field %s is %q", k, strings.ReplaceAll(r.PostForm.Get(k), testClientSecret, "<secret>"))
			}
		}
		if len(r.PostForm) != len(want) {
			t.Errorf("token form has %d fields, want %d", len(r.PostForm), len(want))
		}
		k := n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":%d}`, accessToken(k), expiresIn)
	}
}

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	t = t.UTC()
	return &t
}

func hostLine(h domain.Host) string {
	seen := "nil"
	if h.ReportedLastSeen != nil {
		seen = h.ReportedLastSeen.Format(time.RFC3339Nano) + "@" + h.ReportedLastSeen.Location().String()
	}
	return fmt.Sprintf("key=%s name=%s kind=%s address=%s os=%s arch=%s notes=%s aliases=%q seen=%s",
		h.Key, h.Name, h.Kind, h.Address, h.OS, h.Arch, h.Notes, h.Aliases, seen)
}

func TestKind(t *testing.T) {
	if got := New().Kind(); got != "tailscale" {
		t.Fatalf("Kind()=%q", got)
	}
}

func TestScanMapsDevicesToHosts(t *testing.T) {
	var listings atomic.Int32
	body := string(fixture(t))
	srv := newAPI(t, nil, func(w http.ResponseWriter, r *http.Request) {
		listings.Add(1)
		if r.Method != http.MethodGet || r.RequestURI != defaultDevices {
			t.Errorf("request %s %s, want GET %s with no query", r.Method, r.RequestURI, defaultDevices)
		}
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("device listing is not authorized with the API access token")
		}
		serveBody(body)(w, r)
	})
	snap, err := New().Scan(context.Background(), apiKeyConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if n := listings.Load(); n != 1 {
		t.Fatalf("device listings=%d, want 1", n)
	}
	if len(snap.Services)+len(snap.Ports)+len(snap.Routes)+len(snap.Certs)+len(snap.Domains) != 0 {
		t.Fatalf("snapshot carries more than hosts: %#v", snap)
	}
	ts := domain.HostKindTailscale
	want := []domain.Host{
		// IPv6-only, no nodeId (falls back to id), lastSeen before 2000.
		{Key: "tailscale:10000000010", Name: "sensor", Kind: ts, Address: "fd7a:115c:a1e0::a", OS: "linux", Aliases: []string{"sensor", "sensor.tail1234.ts.net"}},
		// CIDR-form addresses, zero lastSeen.
		{Key: "tailscale:nGW9CNTRL", Name: "gateway", Kind: ts, Address: "100.64.0.9", OS: "linux", Aliases: []string{"fd7a:115c:a1e0::9", "gateway", "gateway.tail1234.ts.net"}},
		// Hostname case kept, IPv6 canonicalized, first of a duplicate nodeId.
		{Key: "tailscale:nNAS5CNTRL", Name: "NAS", Kind: ts, Address: "100.64.0.5", OS: "linux", Aliases: []string{"fd7a:115c:a1e0::5", "nas", "nas.tail1234.ts.net"}, ReportedLastSeen: at("2026-09-20T10:15:30.123456Z")},
		// Android reports hostname "localhost", which names no machine.
		{Key: "tailscale:nPIX11CNTRL", Name: "pixel", Kind: ts, Address: "100.64.0.11", OS: "android", Aliases: []string{"fd7a:115c:a1e0::b", "pixel", "pixel.tail1234.ts.net"}, ReportedLastSeen: at("2026-09-21T06:00:00Z")},
		// Empty hostname, authorized field absent, lastSeen with an offset.
		{Key: "tailscale:nPRN8CNTRL", Name: "printer", Kind: ts, Address: "100.64.0.8", OS: "linux", Aliases: []string{"printer", "printer.tail1234.ts.net"}, ReportedLastSeen: at("2026-09-18T02:30:00Z")},
		// IPv6 listed first, trailing dot on the MagicDNS name.
		{Key: "tailscale:nWS7CNTRL", Name: "Workstation", Kind: ts, Address: "100.64.0.7", OS: "macOS", Aliases: []string{"fd7a:115c:a1e0::7", "workstation", "workstation.tail1234.ts.net"}, ReportedLastSeen: at("2026-09-19T22:01:02Z")},
	}
	var got, exp []string
	for _, h := range snap.Hosts {
		got = append(got, hostLine(h))
	}
	for _, h := range want {
		exp = append(exp, hostLine(h))
	}
	if strings.Join(got, "\n") != strings.Join(exp, "\n") {
		t.Fatalf("hosts:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(exp, "\n"))
	}
}

func TestScanNeverIngestsKeysUsersOrEndpoints(t *testing.T) {
	body := fixture(t)
	srv := newAPI(t, nil, serveBody(string(body)))
	snap, err := New().Scan(context.Background(), apiKeyConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	dump := fmt.Sprintf("%#v", snap) + string(encoded)
	for _, forbidden := range []string{
		"mkey:", "nodekey:", "tlpub:", // machine, node and tailnet-lock keys
		"owner@example.com", "guest@example.net", // user
		"198.51.100.23", "192.168.1.20", "203.0.113.50", "Frankfurt", // clientConnectivity
		"192.168.1.0/24", "tag:server", "1.76.1", // routes, tags, client version
		"shared-box", "pending-box", "nas-duplicate", "no-address", "100.64.1.", // skipped devices
	} {
		if !bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("fixture does not contain %q, so this test proves nothing about it", forbidden)
		}
		if strings.Contains(dump, forbidden) {
			t.Errorf("snapshot contains %q", forbidden)
		}
	}
}

func TestValidateReadsTheDeviceList(t *testing.T) {
	var exchanges, listings atomic.Int32
	srv := newAPI(t, issueTokens(t, &exchanges, 3600), func(w http.ResponseWriter, r *http.Request) {
		listings.Add(1)
		serveBody(`{"devices":[]}`)(w, r)
	})
	if err := New().Validate(context.Background(), oauthConfig(srv.URL)); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 1 || listings.Load() != 1 {
		t.Fatalf("exchanges=%d listings=%d, want 1 and 1", exchanges.Load(), listings.Load())
	}
}

func TestOAuthExchangeRequestsReadScopeAndCaches(t *testing.T) {
	var exchanges atomic.Int32
	body := string(fixture(t))
	srv := newAPI(t, issueTokens(t, &exchanges, 3600), func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+accessToken(exchanges.Load()) {
			t.Error("device listing is not authorized with the newest access token")
		}
		serveBody(body)(w, r)
	})
	c := New()
	clock := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	raw := oauthConfig(srv.URL)
	scan := func(wantExchanges int32) {
		t.Helper()
		snap, err := c.Scan(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Hosts) != 6 {
			t.Fatalf("hosts=%d, want 6", len(snap.Hosts))
		}
		if n := exchanges.Load(); n != wantExchanges {
			t.Fatalf("exchanges=%d, want %d", n, wantExchanges)
		}
	}
	scan(1)
	scan(1)
	clock = clock.Add(58 * time.Minute)
	scan(1)
	clock = clock.Add(2 * time.Minute)
	scan(2)
	scan(2)
}

func TestTokenLifetimeIsBounded(t *testing.T) {
	for _, tc := range []struct {
		expiresIn int
		lifetime  time.Duration // expires_in minus a minute of slack
	}{
		{3600, 59 * time.Minute},
		{600, 9 * time.Minute},
		{0, 59 * time.Minute},
		{-5, 59 * time.Minute},
		{90000, 59 * time.Minute},
	} {
		t.Run(fmt.Sprint(tc.expiresIn), func(t *testing.T) {
			var exchanges atomic.Int32
			srv := newAPI(t, issueTokens(t, &exchanges, tc.expiresIn), serveBody(`{"devices":[]}`))
			c := New()
			clock := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
			c.now = func() time.Time { return clock }
			raw := oauthConfig(srv.URL)
			for _, step := range []struct {
				advance time.Duration
				want    int32
			}{{0, 1}, {tc.lifetime - time.Second, 1}, {time.Second, 2}} {
				clock = clock.Add(step.advance)
				if _, err := c.Scan(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				if n := exchanges.Load(); n != step.want {
					t.Fatalf("after %s: exchanges=%d, want %d", step.advance, n, step.want)
				}
			}
		})
	}
}

func TestTokenCacheKeyFingerprintsTheSecret(t *testing.T) {
	a, err := decode(oauthConfig("https://api.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.OAuthClientSecret += "-rotated"
	ka, kb := tokenKey(a), tokenKey(b)
	if ka == kb {
		t.Fatal("an edited secret reuses the old secret's cached token")
	}
	for _, k := range []string{ka, kb} {
		if strings.Contains(k, "clientSECRETvalue") {
			t.Fatalf("cache key holds the secret: %q", k)
		}
	}
}

func TestOAuth401RefreshesOnce(t *testing.T) {
	t.Run("a revoked token is replaced", func(t *testing.T) {
		var exchanges, listings atomic.Int32
		srv := newAPI(t, issueTokens(t, &exchanges, 3600), func(w http.ResponseWriter, r *http.Request) {
			if listings.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+accessToken(2) {
				t.Error("retry does not use the fresh access token")
			}
			serveBody(`{"devices":[]}`)(w, r)
		})
		if _, err := New().Scan(context.Background(), oauthConfig(srv.URL)); err != nil {
			t.Fatal(err)
		}
		if exchanges.Load() != 2 || listings.Load() != 2 {
			t.Fatalf("exchanges=%d listings=%d, want 2 and 2", exchanges.Load(), listings.Load())
		}
	})
	t.Run("a persistent 401 gives up after one fresh token", func(t *testing.T) {
		var exchanges, listings atomic.Int32
		srv := newAPI(t, issueTokens(t, &exchanges, 3600), func(w http.ResponseWriter, _ *http.Request) {
			listings.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		})
		_, err := New().Scan(context.Background(), oauthConfig(srv.URL))
		want := "Tailscale rejected the OAuth access token (401 Unauthorized) after a fresh token exchange; confirm the OAuth client still exists"
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v, want %q", err, want)
		}
		if exchanges.Load() != 2 || listings.Load() != 2 {
			t.Fatalf("exchanges=%d listings=%d, want 2 and 2", exchanges.Load(), listings.Load())
		}
	})
	t.Run("an API access token is not retried", func(t *testing.T) {
		var listings atomic.Int32
		srv := newAPI(t, nil, func(w http.ResponseWriter, _ *http.Request) {
			listings.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		})
		_, err := New().Scan(context.Background(), apiKeyConfig(srv.URL))
		want := "Tailscale rejected the API access token (401 Unauthorized); it may have expired (access tokens last at most 90 days) or been revoked"
		if err == nil || err.Error() != want {
			t.Fatalf("err=%v, want %q", err, want)
		}
		if listings.Load() != 1 {
			t.Fatalf("listings=%d, want 1", listings.Load())
		}
	})
}

// echo answers with status and writes every credential it can see into the
// body, as a careless or hostile upstream might.
func echo(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message":"denied: auth=%s secret=%s key=%s"}`, r.Header.Get("Authorization"), r.PostForm.Get("client_secret"), testAPIKey)
	}
}

// rawStatusServer answers every request with the given status line.
// net/http's server cannot set a reason phrase, but a real upstream can, and
// an error built from res.Status would repeat it.
func rawStatusServer(t *testing.T, statusLine string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, req.Body)
				_, _ = io.WriteString(conn, statusLine+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

func closedServerURL() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

func TestErrorsNeverEchoCredentials(t *testing.T) {
	var n atomic.Int32
	tokens := func(t *testing.T) http.HandlerFunc { return issueTokens(t, &n, 3600) }
	for _, tc := range []struct {
		name  string
		oauth bool
		base  func(t *testing.T) string
		want  []string
	}{
		{"devices 403", false, func(t *testing.T) string { return newAPI(t, nil, echo(403)).URL },
			[]string{`403 Forbidden for tailnet "example.com"`, "devices:core:read", "Devices > Core > Read"}},
		{"devices 403 with OAuth", true, func(t *testing.T) string { return newAPI(t, tokens(t), echo(403)).URL },
			[]string{"403 Forbidden", "devices:core:read"}},
		{"devices 404", false, func(t *testing.T) string { return newAPI(t, nil, echo(404)).URL },
			[]string{`tailnet "example.com" is not visible to this credential`, `use "-"`}},
		{"devices 401 with API access token", false, func(t *testing.T) string { return newAPI(t, nil, echo(401)).URL },
			[]string{"API access token", "90 days"}},
		{"devices 401 with OAuth", true, func(t *testing.T) string { return newAPI(t, tokens(t), echo(401)).URL },
			[]string{"OAuth access token", "fresh token exchange"}},
		{"devices 429", false, func(t *testing.T) string { return newAPI(t, nil, echo(429)).URL },
			[]string{"Tailscale API returned 429 Too Many Requests"}},
		{"devices 500", true, func(t *testing.T) string { return newAPI(t, tokens(t), echo(500)).URL },
			[]string{"Tailscale API returned 500 Internal Server Error"}},
		{"devices 307", false, func(t *testing.T) string { return newAPI(t, nil, echo(307)).URL },
			[]string{"redirected (307 Temporary Redirect)", "base_url"}},
		{"devices status line echoes the key", false, func(t *testing.T) string { return rawStatusServer(t, "HTTP/1.1 502 "+testAPIKey) },
			[]string{"Tailscale API returned 502 Bad Gateway"}},
		{"devices body echoes the key", false, func(t *testing.T) string { return newAPI(t, nil, serveBody(`{"devices":`+testAPIKey)).URL },
			[]string{"decode Tailscale device list"}},
		{"devices transport error", false, func(*testing.T) string { return closedServerURL() },
			[]string{"read Tailscale devices"}},
		{"token 400 invalid_scope", true, func(t *testing.T) string { return newAPI(t, echo(400), nil).URL },
			[]string{"Tailscale OAuth token request returned 400 Bad Request", "check the client ID and secret", "devices:core:read"}},
		{"token 401", true, func(t *testing.T) string { return newAPI(t, echo(401), nil).URL },
			[]string{"returned 401 Unauthorized", "devices:core:read"}},
		{"token 307", true, func(t *testing.T) string { return newAPI(t, echo(307), nil).URL },
			[]string{"Tailscale OAuth token endpoint redirected (307 Temporary Redirect)", "check base_url"}},
		{"token status line echoes the secret", true, func(t *testing.T) string { return rawStatusServer(t, "HTTP/1.1 400 "+testClientSecret) },
			[]string{"returned 400 Bad Request"}},
		{"token body echoes the secret", true, func(t *testing.T) string {
			return newAPI(t, serveBody(`{"access_token":`+testClientSecret), nil).URL
		}, []string{"decode Tailscale OAuth token response"}},
		{"token transport error", true, func(*testing.T) string { return closedServerURL() },
			[]string{"Tailscale OAuth token request"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base(t)
			raw := apiKeyConfig(base, "tailnet", "example.com")
			if tc.oauth {
				raw = oauthConfig(base, "tailnet", "example.com")
			}
			c := New()
			for _, run := range []func() error{
				func() error { _, err := c.Scan(context.Background(), raw); return err },
				func() error { return c.Validate(context.Background(), raw) },
			} {
				err := run()
				if err == nil {
					t.Fatal("no error")
				}
				msg := err.Error()
				for _, w := range tc.want {
					if !strings.Contains(msg, w) {
						t.Errorf("error %q does not contain %q", msg, w)
					}
				}
				for _, secret := range credentials {
					if strings.Contains(msg, secret) {
						t.Fatalf("error echoes a credential: %q", msg)
					}
				}
			}
		})
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		serveBody(`{"access_token":"stolen","devices":[]}`)(w, r)
	}))
	defer elsewhere.Close()
	redirect := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+r.URL.Path, code)
		}
	}
	for _, code := range []int{301, 302, 307, 308} {
		t.Run(fmt.Sprintf("token %d", code), func(t *testing.T) {
			srv := newAPI(t, redirect(code), nil)
			_, err := New().Scan(context.Background(), oauthConfig(srv.URL))
			if err == nil || !strings.Contains(err.Error(), "Tailscale OAuth token endpoint redirected") {
				t.Fatalf("err=%v, want a redirect refusal", err)
			}
		})
		t.Run(fmt.Sprintf("devices %d", code), func(t *testing.T) {
			srv := newAPI(t, nil, redirect(code))
			_, err := New().Scan(context.Background(), apiKeyConfig(srv.URL))
			if err == nil || !strings.Contains(err.Error(), "Tailscale API redirected") {
				t.Fatalf("err=%v, want a redirect refusal", err)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests", n)
	}
}

func TestMalformedInput(t *testing.T) {
	oversizedDevices := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":[`)
		chunk := strings.Repeat(`{"nodeId":"nBIGCNTRL","hostname":"big","addresses":["100.64.0.1"]},`, 1024)
		for written := 0; written < connectors.MaxResponseBytes+(1<<20); written += len(chunk) {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	}
	for _, tc := range []struct {
		name    string
		token   http.HandlerFunc
		devices http.HandlerFunc
		want    string
	}{
		{"truncated device list", nil, serveBody(`{"devices":[{`), "decode Tailscale device list"},
		{"devices is a string", nil, serveBody(`{"devices":"x"}`), "decode Tailscale device list"},
		{"device is a number", nil, serveBody(`{"devices":[42]}`), "decode Tailscale device list"},
		{"html", nil, serveBody(`<html>proxy login</html>`), "decode Tailscale device list"},
		{"empty body", nil, serveBody(``), "decode Tailscale device list"},
		{"no devices list", nil, serveBody(`{}`), "Tailscale response has no devices list"},
		{"null devices list", nil, serveBody(`{"devices":null}`), "Tailscale response has no devices list"},
		{"token without access_token", serveBody(`{"token_type":"Bearer","expires_in":3600}`), nil, "Tailscale OAuth token response has no access_token"},
		{"token_type mac", serveBody(`{"access_token":"access-TOKEN-x","token_type":"mac","expires_in":3600}`), nil, "Tailscale OAuth token response is not a Bearer token"},
		{"access_token with a space", serveBody(`{"access_token":"access-TOKEN x","token_type":"Bearer"}`), nil, "Tailscale OAuth token response has a malformed access_token"},
		{"truncated token response", serveBody(`{"access_token":"access-TOKEN`), nil, "decode Tailscale OAuth token response"},
		{"oversized token response", serveBody(`{"access_token":"` + strings.Repeat("a", 70<<10) + `"}`), nil, "unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAPI(t, tc.token, tc.devices)
			raw := apiKeyConfig(srv.URL)
			if tc.token != nil {
				raw = oauthConfig(srv.URL)
			}
			snap, err := New().Scan(context.Background(), raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want it to contain %q", err, tc.want)
			}
			if len(snap.Hosts) != 0 {
				t.Fatalf("a failed scan returned hosts: %#v", snap.Hosts)
			}
		})
	}
	t.Run("oversized device list is cut at the cap", func(t *testing.T) {
		srv := newAPI(t, nil, oversizedDevices)
		snap, err := New().Scan(context.Background(), apiKeyConfig(srv.URL))
		if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "decode Tailscale device list") {
			t.Fatalf("err=%v, want a decode error wrapping io.ErrUnexpectedEOF", err)
		}
		if len(snap.Hosts) != 0 {
			t.Fatal("a truncated list produced hosts")
		}
	})
	t.Run("empty device list is a valid empty tailnet", func(t *testing.T) {
		srv := newAPI(t, nil, serveBody(`{"devices":[]}`))
		snap, err := New().Scan(context.Background(), apiKeyConfig(srv.URL))
		if err != nil || len(snap.Hosts) != 0 {
			t.Fatalf("snap=%#v err=%v", snap, err)
		}
	})
}

func TestDecodeValidation(t *testing.T) {
	const (
		errNoCredential = "an OAuth client (oauth_client_id + oauth_client_secret) or an API access token (api_key) is required"
		errBoth         = "configure either an OAuth client or an API access token, not both"
		errHalfOAuth    = "oauth_client_id and oauth_client_secret are both required"
		errScheme       = "base_url must be an http(s) URL such as https://api.tailscale.com"
		errBaseParts    = "base_url must not contain credentials, a path, a query, or a fragment"
		errTailnet      = `tailnet must be "-" or a tailnet name or ID such as example.com`
		errCleartext    = "base_url must use https unless its host is loopback (127.0.0.1, ::1 or localhost): http would send credentials in cleartext"
	)
	withKey := func(kv ...string) connectors.Config { return cfg(append([]string{"api_key", testAPIKey}, kv...)...) }
	withOAuth := func(secret string) connectors.Config {
		return cfg("oauth_client_id", testClientID, "oauth_client_secret", secret)
	}
	x := strings.Repeat
	for _, tc := range []struct {
		name   string
		raw    connectors.Config
		want   string
		secret string // must not appear in the error
	}{
		{"no credential", cfg(), errNoCredential, ""},
		{"blank credential", cfg("api_key", "  ", "oauth_client_id", "\t", "oauth_client_secret", " "), errNoCredential, ""},
		{"both kinds", cfg("oauth_client_id", testClientID, "oauth_client_secret", testClientSecret, "api_key", testAPIKey), errBoth, "SECRETvalue"},
		{"both kinds, secret only", cfg("oauth_client_secret", testClientSecret, "api_key", testAPIKey), errBoth, "SECRETvalue"},
		{"client id only", cfg("oauth_client_id", testClientID), errHalfOAuth, ""},
		{"client secret only", cfg("oauth_client_secret", testClientSecret), errHalfOAuth, "clientSECRETvalue"},
		{"ftp base_url", withKey("base_url", "ftp://api.example.com"), errScheme, ""},
		{"schemeless base_url", withKey("base_url", "api.example.com"), errScheme, ""},
		{"base_url without host", withKey("base_url", "https://"), errScheme, ""},
		{"base_url with only a port", withKey("base_url", "http://:8080"), errScheme, ""},
		{"unparseable base_url", withKey("base_url", "https://api.example .com"), errScheme, ""},
		{"base_url with credentials", withKey("base_url", "https://reader:hunter2PASS@api.example.com"), errBaseParts, "hunter2PASS"},
		{"base_url with a path", withKey("base_url", "https://api.example.com/api/v2"), errBaseParts, ""},
		{"base_url with a query", withKey("base_url", "https://api.example.com?key=queryVALUE"), errBaseParts, "queryVALUE"},
		{"base_url with an empty query", withKey("base_url", "https://api.example.com/?"), errBaseParts, ""},
		{"base_url with a fragment", withKey("base_url", "https://api.example.com#fragVALUE"), errBaseParts, "fragVALUE"},
		{"http base_url to a public host", withKey("base_url", "http://api.tailscale.com"), errCleartext, "api.tailscale.com"},
		{"http base_url to a LAN address", withKey("base_url", "http://10.0.0.5:8080"), errCleartext, "10.0.0.5"},
		{"http base_url to a tailnet address", withKey("base_url", "http://100.64.0.1"), errCleartext, "100.64.0.1"},
		{"http base_url to a localhost lookalike", withKey("base_url", "http://localhost.example.com"), errCleartext, "example.com"},
		{"http OAuth base_url", cfg("oauth_client_id", testClientID, "oauth_client_secret", testClientSecret, "base_url", "http://api.example.com"), errCleartext, "clientSECRETvalue"},
		{"tailnet with a slash", withKey("tailnet", "example.com/../keys"), errTailnet, ""},
		{"tailnet with a backslash", withKey("tailnet", `example\com`), errTailnet, ""},
		{"tailnet with a question mark", withKey("tailnet", "example.com?fields=x"), errTailnet, ""},
		{"tailnet with a hash", withKey("tailnet", "example#com"), errTailnet, ""},
		{"tailnet with a space", withKey("tailnet", "my tailnet"), errTailnet, ""},
		{"tailnet with a control char", withKey("tailnet", "example\x7fcom"), errTailnet, ""},
		{"tailnet too long", withKey("tailnet", x("t", 256)), errTailnet, ""},
		{"OAuth client secret as api_key", cfg("api_key", "tskey-client-kX1CNTRL-clientSECRETvalue"), "that is an OAuth client secret; enter it as the OAuth client secret together with its client ID", "clientSECRETvalue"},
		{"auth key as api_key", cfg("api_key", "tskey-auth-kX1CNTRL-authKEYvalue"), "that is an auth key for adding devices, not an API access token", "authKEYvalue"},
		{"API access token as client secret", withOAuth("tskey-api-kX1CNTRL-apiKEYsecretVALUE"), "that is an API access token; use the API access token field instead", "apiKEYsecretVALUE"},
		{"space in api_key", cfg("api_key", "tskey-api-kX1CNTRL apiKEYsecretVALUE"), "api_key contains whitespace", "apiKEYsecretVALUE"},
		{"tab in client secret", withOAuth("tskey-client-kX1CNTRL\tclientSECRETvalue"), "oauth_client_secret contains whitespace", "clientSECRETvalue"},
		{"control char in api_key", cfg("api_key", "tskey-api-kX1CNTRL\x07apiKEYsecretVALUE"), "api_key contains whitespace", "apiKEYsecretVALUE"},
		{"space in client id", cfg("oauth_client_id", "kX1 CNTRL", "oauth_client_secret", testClientSecret), "oauth_client_id contains whitespace", "clientSECRETvalue"},
		{"client id too long", cfg("oauth_client_id", x("k", 257), "oauth_client_secret", testClientSecret), "oauth_client_id is too long", "clientSECRETvalue"},
		{"client secret too long", withOAuth("tskey-client-" + x("S", 500)), "oauth_client_secret is too long", x("S", 20)},
		{"api_key too long", cfg("api_key", "tskey-api-"+x("K", 503)), "api_key is too long", x("K", 20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decode(tc.raw)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("error echoes the value: %q", err)
			}
			if _, err := New().Scan(context.Background(), tc.raw); err == nil || err.Error() != tc.want {
				t.Fatalf("Scan err=%v, want %q", err, tc.want)
			}
			if err := New().Validate(context.Background(), tc.raw); err == nil || err.Error() != tc.want {
				t.Fatalf("Validate err=%v, want %q", err, tc.want)
			}
		})
	}
	t.Run("non-string value", func(t *testing.T) {
		if _, err := decode(connectors.Config{"api_key": json.RawMessage(`123`)}); err == nil {
			t.Fatal("a numeric api_key was accepted")
		}
	})
}

func TestDecodeDefaultsAndNormalizes(t *testing.T) {
	x, err := decode(cfg("api_key", "  "+testAPIKey+"\n", "tailnet", "  "))
	if err != nil {
		t.Fatal(err)
	}
	if x.Tailnet != "-" || x.BaseURL != "https://api.tailscale.com" || x.APIKey != testAPIKey {
		t.Fatalf("defaults: %+v", x)
	}
	x, err = decode(cfg("oauth_client_id", " "+testClientID, "oauth_client_secret", testClientSecret+" ", "tailnet", " example.com ", "base_url", " HTTPS://api.example.com:8443/ "))
	if err != nil {
		t.Fatal(err)
	}
	if x.Tailnet != "example.com" || x.BaseURL != "https://api.example.com:8443" || x.OAuthClientID != testClientID || x.OAuthClientSecret != testClientSecret {
		t.Fatalf("normalized: tailnet=%q base=%q", x.Tailnet, x.BaseURL)
	}
	s := strings.Repeat
	for _, raw := range []connectors.Config{
		cfg("api_key", testAPIKey, "tailnet", s("t", 255)),
		cfg("api_key", "tskey-api-"+s("K", 502)),
		cfg("oauth_client_id", s("k", 256), "oauth_client_secret", "tskey-client-"+s("S", 499)),
		cfg("api_key", testAPIKey, "base_url", "https://[fd7a:115c:a1e0::1]"),
		cfg("api_key", testAPIKey, "base_url", "https://10.0.0.5:8443"),
		// http is for local mocks only.
		cfg("api_key", testAPIKey, "base_url", "http://127.0.0.1:8080"),
		cfg("api_key", testAPIKey, "base_url", "http://127.0.0.2"),
		cfg("api_key", testAPIKey, "base_url", "http://[::1]:8080"),
		cfg("api_key", testAPIKey, "base_url", "http://[::ffff:127.0.0.1]:8080"),
		cfg("api_key", testAPIKey, "base_url", "http://localhost:8080"),
		cfg("api_key", testAPIKey, "base_url", "HTTP://LocalHost"),
	} {
		if _, err := decode(raw); err != nil {
			t.Errorf("limit value refused: %v", err)
		}
	}
}

func TestExampleConfigDecodes(t *testing.T) {
	b, err := os.ReadFile("../../../docs/examples/connectors/tailscale.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw connectors.Config
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	x, err := decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if x.Tailnet != "-" || x.APIKey != "" || x.OAuthClientID == "" || x.BaseURL != "https://api.tailscale.com" {
		t.Fatalf("example: tailnet=%q base=%q oauth=%v", x.Tailnet, x.BaseURL, x.OAuthClientID != "")
	}
}

func TestTailnetPathEscaping(t *testing.T) {
	for _, tc := range []struct{ tailnet, path string }{
		{"", "/api/v2/tailnet/-/devices"},
		{"-", "/api/v2/tailnet/-/devices"},
		{"example.com", "/api/v2/tailnet/example.com/devices"},
		{"T123CNTRL", "/api/v2/tailnet/T123CNTRL/devices"},
		{"odd%name", "/api/v2/tailnet/odd%25name/devices"},
		{"a;b", "/api/v2/tailnet/a%3Bb/devices"},
	} {
		t.Run(tc.tailnet, func(t *testing.T) {
			var got atomic.Value
			srv := newAPI(t, nil, func(w http.ResponseWriter, r *http.Request) {
				got.Store(r.RequestURI)
				serveBody(`{"devices":[]}`)(w, r)
			})
			raw := apiKeyConfig(srv.URL)
			if tc.tailnet != "" {
				raw = apiKeyConfig(srv.URL, "tailnet", tc.tailnet)
			}
			if _, err := New().Scan(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			if got.Load() != tc.path {
				t.Fatalf("request URI %v, want %s", got.Load(), tc.path)
			}
		})
	}
}

func TestScanHonorsContextCancellation(t *testing.T) {
	block := func(started chan<- struct{}) http.HandlerFunc {
		return func(_ http.ResponseWriter, r *http.Request) {
			// net/http notices a client hang-up only once the body is drained.
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		}
	}
	for _, tc := range []struct {
		name  string
		oauth bool
	}{{"device listing", false}, {"token exchange", true}} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			var raw connectors.Config
			if tc.oauth {
				raw = oauthConfig(newAPI(t, block(started), nil).URL)
			} else {
				raw = apiKeyConfig(newAPI(t, nil, block(started)).URL)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				select {
				case <-started:
					cancel()
				case <-ctx.Done():
				}
			}()
			begin := time.Now()
			_, err := New().Scan(ctx, raw)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v, want context.Canceled", err)
			}
			if d := time.Since(begin); d > 5*time.Second {
				t.Fatalf("Scan took %s after cancellation", d)
			}
		})
	}
	t.Run("already canceled", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, raw := range []connectors.Config{apiKeyConfig(srv.URL), oauthConfig(srv.URL)} {
			if _, err := New().Scan(ctx, raw); !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v, want context.Canceled", err)
			}
		}
		if hits.Load() != 0 {
			t.Fatalf("a canceled scan reached the server %d times", hits.Load())
		}
	})
}
