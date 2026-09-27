package npm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HarshShah0203/homedex/internal/connectors"
)

// fingerprint is the SHA-256 fingerprint of the certificate srv presents, as
// a failed Test connection shows it.
func fingerprint(srv *httptest.Server) string {
	sum := sha256.Sum256(srv.Certificate().Raw)
	return connectors.FormatFingerprint(sum[:])
}

func (p *plus) pinned(password, pin string) map[string]json.RawMessage {
	c := p.config(password)
	c["fingerprint"] = json.RawMessage(`"` + pin + `"`)
	return c
}

// wrongPin is a well-formed fingerprint no test server presents.
var wrongPin = strings.Repeat("AB:", 31) + "AB"

// NPMplus's admin port serves a self-signed certificate by default. Without a
// pin the login is refused before the password is sent, and the error shows
// the fingerprint to compare and paste.
func TestUnpinnedSelfSignedCertificateShowsItsFingerprint(t *testing.T) {
	p := newPlus(t, false)
	fp := fingerprint(p.Server)
	err := New().Validate(context.Background(), p.config(plusPassword))
	if err == nil {
		t.Fatal("an untrusted certificate passed Validate")
	}
	msg := err.Error()
	for _, want := range []string{"the NPM certificate is not trusted (x509: ", "The server presented the SHA-256 fingerprint " + fp + ".", "paste it into the certificate fingerprint field", "DEFAULT_CERT_ID"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, plusPassword) {
		t.Errorf("error %q quotes the password", msg)
	}
	if p.logins.Load() != 0 {
		t.Fatal("the password reached a server whose certificate was not trusted")
	}
	// What the error shows is what the fingerprint field accepts.
	shown := regexp.MustCompile(`fingerprint ([0-9A-F:]{95})\.`).FindStringSubmatch(msg)
	if shown == nil {
		t.Fatalf("no fingerprint to paste in %q", msg)
	}
	if err = New().Validate(context.Background(), p.pinned(plusPassword, shown[1])); err != nil {
		t.Fatalf("pinning the fingerprint the error showed: %v", err)
	}
}

// A pinned source logs in and reads over the self-signed certificate, with a
// client that trusts nothing else, and in every form the fingerprint is pasted.
func TestPinnedNPMplusLogsInAndReads(t *testing.T) {
	for version, release := range versions {
		p := newPlus(t, release)
		fp := fingerprint(p.Server)
		for name, pin := range map[string]string{
			"as shown":        fp,
			"lower no colons": strings.ToLower(strings.ReplaceAll(fp, ":", "")),
			"openssl output":  "sha256 Fingerprint=" + fp,
		} {
			p.logins.Store(0)
			p.reads.Store(0)
			c := New()
			if err := c.Validate(context.Background(), p.pinned(plusPassword, pin)); err != nil {
				t.Fatalf("%s %s: Validate: %v", version, name, err)
			}
			snap, err := c.Scan(context.Background(), p.pinned(plusPassword, pin))
			if err != nil || len(snap.Routes) != 3 || len(snap.Certs) != 1 {
				t.Fatalf("%s %s: Scan = %d routes, %d certs, %v", version, name, len(snap.Routes), len(snap.Certs), err)
			}
			if l, r := p.logins.Load(), p.reads.Load(); l != 1 || r != 2 {
				t.Fatalf("%s %s: %d logins and %d reads, want the session from Validate reused for both reads", version, name, l, r)
			}
		}
	}
}

// A certificate other than the pinned one fails Validate and Scan before any
// request is sent, even when the connector's own client would trust it.
func TestWrongPinFailsWithThePresentedFingerprint(t *testing.T) {
	p := newPlus(t, false)
	fp := fingerprint(p.Server)
	for name, c := range map[string]*Connector{"system roots": New(), "client trusting the server": p.connector()} {
		err := c.Validate(context.Background(), p.pinned(plusPassword, wrongPin))
		if err == nil || !strings.HasPrefix(err.Error(), "the NPM certificate does not match the pinned fingerprint: the server presented "+fp+". ") {
			t.Fatalf("%s: Validate = %v", name, err)
		}
		if _, err = c.Scan(context.Background(), p.pinned(plusPassword, wrongPin)); err == nil || !strings.Contains(err.Error(), "does not match the pinned fingerprint") {
			t.Fatalf("%s: Scan = %v", name, err)
		}
	}
	if l, r := p.logins.Load(), p.reads.Load(); l != 0 || r != 0 {
		t.Fatalf("%d logins and %d reads reached a server whose certificate is not the pinned one", l, r)
	}
}

// The pin covers reads too: a session cached earlier is never sent to a
// server whose certificate is not the pinned one.
func TestPinAppliesToReadsWithACachedSession(t *testing.T) {
	p := newPlus(t, false)
	c := New()
	if err := c.Validate(context.Background(), p.pinned(plusPassword, fingerprint(p.Server))); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	good, err := decode(p.pinned(plusPassword, fingerprint(p.Server)))
	if err != nil {
		t.Fatal(err)
	}
	bad, err := decode(p.pinned(plusPassword, wrongPin))
	if err != nil {
		t.Fatal(err)
	}
	// Hand the valid session to the wrongly pinned config, as if the
	// certificate had changed since the login.
	c.mu.Lock()
	c.sessions[bad.key()] = c.sessions[good.key()]
	c.mu.Unlock()
	if _, err = c.Scan(context.Background(), p.pinned(plusPassword, wrongPin)); err == nil || !strings.Contains(err.Error(), "does not match the pinned fingerprint") {
		t.Fatalf("Scan with a cached session = %v", err)
	}
	if l, r := p.logins.Load(), p.reads.Load(); l != 1 || r != 0 {
		t.Fatalf("%d logins and %d reads, want only the first login", l, r)
	}
}

// A pinned client keeps the request helpers' redirect rules: neither the
// password nor the session follows a redirect to another host, even one
// presenting the pinned certificate.
func TestPinnedClientRefusesRedirectsToAnotherHost(t *testing.T) {
	var landed atomic.Int32
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed.Add(1)
		_, _ = io.WriteString(w, `{"token":"t"}`)
	}))
	defer elsewhere.Close()
	// Both servers listen on 127.0.0.1 with httptest's one certificate;
	// naming the target localhost makes the redirect leave the configured host.
	other := strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)
	for path, want := range map[string]string{
		"/api/tokens":            "NPM token API returned 308 Permanent Redirect; Homedex does not follow this redirect with credentials, check the URL",
		"/api/nginx/proxy-hosts": "NPM API returned 308 Permanent Redirect; Homedex does not follow this redirect with credentials, check the URL",
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == path {
				http.Redirect(w, r, other+r.URL.Path, http.StatusPermanentRedirect)
				return
			}
			_, _ = io.WriteString(w, `{"token":"t"}`)
		}))
		pinned := npmConfig(srv.URL)
		pinned["fingerprint"] = json.RawMessage(`"` + fingerprint(srv) + `"`)
		if _, err := New().Scan(context.Background(), pinned); err == nil || err.Error() != want {
			t.Errorf("%s: Scan = %v, want %q", path, err, want)
		}
		srv.Close()
	}
	if n := landed.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests carrying credentials", n)
	}
}

// A pinned client keeps the request helpers' size limit.
func TestPinnedReadsKeepTheSizeLimit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tokens" {
			_, _ = io.WriteString(w, `{"token":"t"}`)
			return
		}
		row := `{"id":1,"domain_names":["a.example.com"],"forward_scheme":"http","forward_host":"a","forward_port":80,"enabled":true},`
		_, _ = io.WriteString(w, "[")
		for written := 0; written <= connectors.MaxResponseBytes; written += len(row) {
			if _, err := io.WriteString(w, row); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `{"id":2}]`)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	defer srv.Close()
	pinned := npmConfig(srv.URL)
	pinned["fingerprint"] = json.RawMessage(`"` + fingerprint(srv) + `"`)
	if _, err := New().Scan(context.Background(), pinned); err == nil {
		t.Fatal("a proxy host list over the size limit was accepted")
	}
}

func TestFingerprintConfig(t *testing.T) {
	with := func(url, pin string) connectors.Config {
		c := npmConfig(url)
		c["fingerprint"] = json.RawMessage(fmt.Sprintf("%q", pin))
		return c
	}
	for name, tc := range map[string]struct {
		cfg  connectors.Config
		want string
	}{
		"http URL":  {with("http://npm:81", wrongPin), "a certificate fingerprint applies only to an https:// URL"},
		"short":     {with("https://npmplus:81", "AB:CD"), "the fingerprint must be the certificate's SHA-256 fingerprint"},
		"password":  {with("https://npmplus:81", "npm-password"), "the fingerprint must be the certificate's SHA-256 fingerprint"},
		"no secret": {connectors.Config{"url": json.RawMessage(`"https://npmplus:81"`), "fingerprint": json.RawMessage(`"` + wrongPin + `"`)}, "url, email, and password are required"},
	} {
		_, err := decode(tc.cfg)
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "npm-password") {
			t.Errorf("%s: error quotes the password: %v", name, err)
		}
	}
	for _, blank := range []string{"", "  \n"} {
		x, err := decode(with("http://npm:81", blank))
		if err != nil || x.pin != nil {
			t.Errorf("blank fingerprint %q: pin %x, %v", blank, x.pin, err)
		}
	}
	x, err := decode(with("HTTPS://npmplus:81/", wrongPin))
	if err != nil || len(x.pin) != sha256.Size {
		t.Fatalf("pin on an https URL: %x, %v", x.pin, err)
	}
}
