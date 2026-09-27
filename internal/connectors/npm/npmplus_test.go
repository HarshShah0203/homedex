package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	plusEmail    = "reader@example.com"
	plusPassword = "npmplus-password"
)

// plus mimics NPMplus (backend/routes/tokens.js and
// backend/lib/express/jwt-decode.js): HTTPS only, POST /api/tokens answers
// {"expires":...} and sets the session as a signed HttpOnly cookie, and reads
// look only at that cookie, never at an Authorization header. By default it
// answers as the develop branch does after the 2026-07-24 release; release
// answers as that release does, which the published image still runs.
type plus struct {
	*httptest.Server
	release bool
	totp    bool
	refuse  atomic.Bool // every read is refused, as for a revoked session
	logins  atomic.Int32
	reads   atomic.Int32
	mu      sync.Mutex
	valid   string // the session cookie value reads accept
}

// versions are the NPMplus behaviours every scenario runs against.
var versions = map[string]bool{"develop": false, "release 2026-07-24": true}

func newPlus(t *testing.T, release bool) *plus {
	t.Helper()
	p := &plus{release: release}
	p.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	// A client that does not trust the certificate is part of a test.
	p.Config.ErrorLog = log.New(io.Discard, "", 0)
	p.StartTLS()
	t.Cleanup(p.Close)
	return p
}

// restart forgets every session, as NPMplus does when it restarts without a
// fixed COOKIE_SECRET.
func (p *plus) restart() {
	p.mu.Lock()
	p.valid = ""
	p.mu.Unlock()
}

func (p *plus) cookie(name, value string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}

func (p *plus) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/tokens":
		var body struct{ Identity, Secret string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Identity != plusEmail || body.Secret != plusPassword {
			// The release throws an AuthError, which it answers with 400.
			status := map[bool]int{false: http.StatusForbidden, true: http.StatusBadRequest}[p.release]
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":"Invalid email or password"}}`, status)
			return
		}
		n := p.logins.Add(1)
		if p.totp && p.release {
			_, _ = io.WriteString(w, `{"requires_2fa":true,"challenge_token":"eyJjaGFsbGVuZ2UifQ"}`)
			return
		}
		if p.totp {
			expires := time.Now().Add(3 * time.Minute)
			http.SetCookie(w, p.cookie(plusChallengeCookie, "s%3Achallenge.c2ln", expires))
			_, _ = fmt.Fprintf(w, `{"requiresTotp":true,"expires":%q}`, expires.UTC().Format(time.RFC3339))
			return
		}
		expires := time.Now().Add(time.Hour)
		value := fmt.Sprintf("s%%3Ajwt-%d.c2ln%%2Bbase64", n)
		p.mu.Lock()
		p.valid = value
		p.mu.Unlock()
		http.SetCookie(w, p.cookie(plusSessionCookie, value, expires))
		_, _ = fmt.Fprintf(w, `{"expires":%q}`, expires.UTC().Format(time.RFC3339))
	case r.URL.Path == "/api/tokens/totp" || r.URL.Path == "/api/tokens/2fa":
		t.Errorf("Homedex tried to answer the TOTP challenge")
		w.WriteHeader(http.StatusForbidden)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/nginx/"):
		p.reads.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("a read sent Authorization %q to NPMplus", got)
		}
		p.mu.Lock()
		valid := p.valid
		p.mu.Unlock()
		ck, err := r.Cookie(plusSessionCookie)
		if err != nil || valid == "" || ck.Value != valid || p.refuse.Load() {
			if p.release {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":{"code":403,"message":"Permission Denied"}}`)
				return
			}
			http.SetCookie(w, p.cookie(plusSessionCookie, "", time.Unix(0, 0)))
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid or expired token"}}`)
			return
		}
		fixture := map[string]string{"/api/nginx/proxy-hosts": "testdata/npmplus-proxy-hosts.json", "/api/nginx/certificates": "testdata/npmplus-certificates.json"}[r.URL.Path]
		b, err := os.ReadFile(fixture)
		if err != nil {
			t.Errorf("read %s: %v", r.URL.Path, err)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	default:
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// connector returns a Connector that trusts the fake's certificate.
func (p *plus) connector() *Connector {
	c := New()
	c.Client = p.Client()
	c.Client.Timeout = 5 * time.Second
	return c
}

func (p *plus) config(password string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"url": json.RawMessage(`"` + p.URL + `/"`), "email": json.RawMessage(`"` + plusEmail + `"`), "password": json.RawMessage(`"` + password + `"`)}
}

func TestNPMplusCookieSessionReadsProxyHostsAndCertificates(t *testing.T) {
	for version, release := range versions {
		p := newPlus(t, release)
		c := p.connector()
		if err := c.Validate(context.Background(), p.config(plusPassword)); err != nil {
			t.Fatalf("%s: Validate: %v", version, err)
		}
		snap, err := c.Scan(context.Background(), p.config(plusPassword))
		if err != nil {
			t.Fatalf("%s: Scan: %v", version, err)
		}
		if n := p.logins.Load(); n != 1 {
			t.Fatalf("%s: logins = %d, want the session from Validate reused by Scan", version, n)
		}
		if n := p.reads.Load(); n != 2 {
			t.Fatalf("%s: reads = %d, want proxy hosts and certificates once each", version, n)
		}
		type route struct {
			key, path, host string
			port            int
			tls             bool
		}
		var got []route
		for _, r := range snap.Routes {
			got = append(got, route{r.Key, r.PathPrefix, r.UpstreamHost, r.UpstreamPort, r.TLS})
		}
		// The disabled host and the disabled location are left out, and the
		// host that serves files from a directory has no upstream.
		want := []route{
			{"npm:3:docs.example.com", "/", "", 0, false},
			{"npm:7:paperless.example.com", "/", "paperless", 8000, true},
			{"npm:7:paperless.example.com:/socket", "/socket", "paperless-ws", 8001, true},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: routes\n got %v\nwant %v", version, got, want)
		}
		if len(snap.Certs) != 1 || snap.Certs[0].NaturalKey() != "tls:paperless.example.com:443" || !snap.Certs[0].NotAfter.Equal(time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC)) {
			t.Fatalf("%s: certs: %#v", version, snap.Certs)
		}
	}
}

// The release answers a session it no longer accepts with 403, later NPMplus
// with 401; either way Homedex logs in once more and retries once.
func TestNPMplusLogsInAgainOnceWhenTheCookieIsRefused(t *testing.T) {
	for version, release := range versions {
		p := newPlus(t, release)
		c := p.connector()
		if err := c.Validate(context.Background(), p.config(plusPassword)); err != nil {
			t.Fatalf("%s: Validate: %v", version, err)
		}
		p.restart()
		snap, err := c.Scan(context.Background(), p.config(plusPassword))
		if err != nil || len(snap.Routes) != 3 || len(snap.Certs) != 1 {
			t.Fatalf("%s: Scan after a restart = %d routes, %d certs, %v", version, len(snap.Routes), len(snap.Certs), err)
		}
		// One login for Validate and one after the refused read; the
		// certificate read uses the fresh session instead of logging in again.
		if n := p.logins.Load(); n != 2 {
			t.Fatalf("%s: logins = %d, want 2", version, n)
		}

		// A session refused even right after logging in fails the scan after
		// one more login instead of looping, and the error names neither the
		// cookie nor the password.
		p.refuse.Store(true)
		_, err = c.Scan(context.Background(), p.config(plusPassword))
		want := map[bool]string{
			false: "NPM API returned 401 Unauthorized",
			true:  "NPM API returned 403 Forbidden: the account needs view access to Proxy Hosts and Certificates",
		}[release]
		if err == nil || err.Error() != want {
			t.Fatalf("%s: Scan with a refused session = %v, want %q", version, err, want)
		}
		if n := p.logins.Load(); n != 3 {
			t.Fatalf("%s: logins = %d, want exactly one more", version, n)
		}
		for _, secret := range []string{"jwt-", "s%3A", plusPassword} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: error %q contains %q", version, err, secret)
			}
		}
	}
}

func TestTwoFactorAccountsFailValidateWithAClearMessage(t *testing.T) {
	for version, release := range versions {
		p := newPlus(t, release)
		p.totp = true
		c := p.connector()
		if err := c.Validate(context.Background(), p.config(plusPassword)); !errors.Is(err, errSecondFactor) {
			t.Fatalf("%s: Validate = %v, want %v", version, err, errSecondFactor)
		}
		if _, err := c.Scan(context.Background(), p.config(plusPassword)); !errors.Is(err, errSecondFactor) {
			t.Fatalf("%s: Scan = %v, want %v", version, err, errSecondFactor)
		}
		if p.reads.Load() != 0 {
			t.Fatalf("%s: a challenge was used as a session", version)
		}
	}

	// NPM itself answers a second factor in the body, like the release.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tokens" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"requires_2fa":true,"challenge_token":"challenge"}`)
	}))
	defer srv.Close()
	if err := New().Validate(context.Background(), npmConfig(srv.URL)); !errors.Is(err, errSecondFactor) {
		t.Fatalf("NPM Validate = %v, want %v", err, errSecondFactor)
	}
}

func TestLoginRefusalsExplainThemselves(t *testing.T) {
	const hint = ": check the email and password; NPMplus also refuses every password login while OIDC_DISABLE_PASSWORD is true"
	for version, release := range versions {
		p := newPlus(t, release)
		err := p.connector().Validate(context.Background(), p.config("wrong"))
		want := map[bool]string{false: "NPM token API returned 403 Forbidden", true: "NPM token API returned 400 Bad Request"}[release] + hint
		if err == nil || err.Error() != want {
			t.Fatalf("%s: wrong password = %v, want %q", version, err, want)
		}
	}

	for name, tc := range map[string]struct {
		write func(http.ResponseWriter)
		want  string
	}{
		"rate limited": {func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"Too many requests, please try again later."}}`)
		}, "NPM token API returned 429 Too Many Requests: NPMplus refuses logins from this address for a few minutes after repeated failures; check the email and password, then test again later"},
		"no session": {func(w http.ResponseWriter) {
			http.SetCookie(w, &http.Cookie{Name: "unrelated", Value: "x"})
			_, _ = io.WriteString(w, `{"expires":"2026-09-27T18:00:00.000Z"}`)
		}, "NPM token API answered without a token or an NPMplus session cookie"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tc.write(w) }))
		if err := New().Validate(context.Background(), npmConfig(srv.URL)); err == nil || err.Error() != tc.want {
			t.Errorf("%s: Validate = %v, want %q", name, err, tc.want)
		}
		srv.Close()
	}

	// NPMplus's admin port serves a self-signed certificate by default.
	p := newPlus(t, true)
	err := New().Validate(context.Background(), p.config(plusPassword))
	if err == nil || !strings.Contains(err.Error(), "certificate") || !strings.Contains(err.Error(), "DEFAULT_CERT_ID") {
		t.Fatalf("untrusted certificate = %v, want the DEFAULT_CERT_ID hint", err)
	}
	if p.logins.Load() != 0 {
		t.Fatal("the password reached a server whose certificate was not trusted")
	}
}

// Test connection must check the password it is given, not answer from a
// session an earlier, correct password left behind.
func TestValidateAlwaysLogsIn(t *testing.T) {
	p := newPlus(t, true)
	c := p.connector()
	if err := c.Validate(context.Background(), p.config(plusPassword)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := c.Validate(context.Background(), p.config("changed")); err == nil {
		t.Fatal("a wrong password passed Validate from the cache")
	}
}

// A 403 on a read with an NPM bearer token is a missing permission, not an
// expired token (NPM answers that with 401), so it does not log in again.
func TestNPMBearerForbiddenReadIsNotRetried(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tokens" {
			logins.Add(1)
			_, _ = io.WriteString(w, `{"token":"t"}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := New().Scan(context.Background(), npmConfig(srv.URL))
	if want := "NPM API returned 403 Forbidden: the account needs view access to Proxy Hosts and Certificates"; err == nil || err.Error() != want {
		t.Fatalf("Scan = %v, want %q", err, want)
	}
	if n := logins.Load(); n != 1 {
		t.Fatalf("logins = %d, want 1", n)
	}
}

// NPM 2.12 and later send enabled as a boolean, like NPMplus; older releases
// send 0 or 1.
func TestScanReadsBooleanEnabledFromNPM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tokens":
			_, _ = io.WriteString(w, `{"token":"t","expires":"2026-09-28T00:00:00.000Z"}`)
		case "/api/nginx/proxy-hosts":
			if r.Header.Get("Authorization") != "Bearer t" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `[{"id":1,"domain_names":["a.example.com"],"forward_scheme":"http","forward_host":"a","forward_port":80,"certificate_id":0,"enabled":true,"locations":[]},
				{"id":2,"domain_names":["b.example.com"],"forward_scheme":"http","forward_host":"b","forward_port":80,"certificate_id":0,"enabled":false,"locations":[]}]`)
		case "/api/nginx/certificates":
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	defer srv.Close()
	snap, err := New().Scan(context.Background(), npmConfig(srv.URL))
	if err != nil || len(snap.Routes) != 1 || snap.Routes[0].Domain != "a.example.com" {
		t.Fatalf("Scan = %#v, %v", snap.Routes, err)
	}
}

func TestFlagDecoding(t *testing.T) {
	for in, want := range map[string]bool{`true`: true, `false`: false, `1`: true, `0`: false, `null`: false} {
		var f flag
		if err := json.Unmarshal([]byte(in), &f); err != nil || bool(f) != want {
			t.Errorf("%s: %v, %v, want %v", in, f, err, want)
		}
	}
	for _, in := range []string{`"true"`, `2`, `{}`} {
		var f flag
		if err := json.Unmarshal([]byte(in), &f); err == nil {
			t.Errorf("%s accepted as %v", in, f)
		}
	}
}
