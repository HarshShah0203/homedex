package connectors

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// selfSigned serves {"valid":true} over TLS with httptest's own certificate,
// which no system root trusts.
func selfSigned(t *testing.T) (*httptest.Server, []byte, string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"valid":true}`)
	}))
	// Refused handshakes are part of these tests.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	return srv, sum[:], FormatFingerprint(sum[:])
}

func TestParseFingerprintAcceptsWhatOperatorsPaste(t *testing.T) {
	_, pin, fp := selfSigned(t)
	for name, in := range map[string]string{
		"upper with colons":  fp,
		"lower no colons":    strings.ToLower(strings.ReplaceAll(fp, ":", "")),
		"openssl 1.1":        "SHA256 Fingerprint=" + fp,
		"openssl 3":          "sha256 Fingerprint=" + fp,
		"prefixed":           "SHA256:" + fp,
		"spaces and newline": "  " + strings.ReplaceAll(fp, ":", " ") + "\n",
	} {
		got, err := ParseFingerprint(in)
		if err != nil || string(got) != string(pin) {
			t.Errorf("%s: %x, %v", name, got, err)
		}
	}
	for name, in := range map[string]string{
		"empty":   "",
		"short":   "AB:CD",
		"sha1":    strings.Repeat("AB:", 19) + "AB",
		"not hex": strings.Repeat("ZZ", 32),
		"secret":  "npm-password-" + strings.Repeat("0", 51),
	} {
		_, err := ParseFingerprint(in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if in != "" && strings.Contains(err.Error(), in) {
			t.Errorf("%s: error quotes the input: %v", name, err)
		}
	}
}

func TestCertTrustPinTrustsExactlyThePinnedCertificate(t *testing.T) {
	srv, pin, fp := selfSigned(t)
	var got answer
	pinned, done := CertTrust{Label: "Test", Pin: pin}.Client(Client(5 * time.Second))
	defer done()
	if err := GetJSON(context.Background(), pinned, srv.URL, &got); err != nil || !got.Valid {
		t.Fatalf("pinned GET = %+v, %v", got, err)
	}

	// A wrong pin fails the handshake, even with a base client that trusts the
	// server through its roots: the pin replaces every other trust.
	wrong := make([]byte, len(pin))
	copy(wrong, pin)
	wrong[0] ^= 0xff
	for name, base := range map[string]*http.Client{"system roots": Client(5 * time.Second), "trusting roots": srv.Client()} {
		c, done := CertTrust{Label: "Test", Pin: wrong}.Client(base)
		err := GetJSON(context.Background(), c, srv.URL, &got)
		done()
		untrusted, ok := AsUntrustedCert(err, "Other")
		if !ok || !untrusted.Pinned || untrusted.Fingerprint != fp || untrusted.Label != "Test" {
			t.Fatalf("%s: wrong pin = %v", name, err)
		}
		if want := "the Test certificate does not match the pinned fingerprint: the server presented " + fp; untrusted.Error() != want {
			t.Fatalf("%s: message %q, want %q", name, untrusted.Error(), want)
		}
	}
}

func TestCertTrustWithoutPinVerifiesRootsAndHost(t *testing.T) {
	srv, _, fp := selfSigned(t)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	var got answer
	for name, tc := range map[string]struct {
		trust CertTrust
		ok    bool
	}{
		"configured CA":          {CertTrust{Label: "Test", Host: "127.0.0.1", Roots: roots}, true},
		"system roots":           {CertTrust{Label: "Test", Host: "127.0.0.1"}, false},
		"name it does not carry": {CertTrust{Label: "Test", Host: "pve.lab.example", Roots: roots}, false},
		"no host name":           {CertTrust{Label: "Test", Roots: roots}, false},
	} {
		c, done := tc.trust.Client(Client(5 * time.Second))
		err := GetJSON(context.Background(), c, srv.URL, &got)
		done()
		if tc.ok != (err == nil) {
			t.Errorf("%s: err = %v, want ok=%v", name, err, tc.ok)
		}
		if untrusted, ok := AsUntrustedCert(err, "Test"); ok && (untrusted.Pinned || untrusted.Fingerprint != fp || untrusted.Cause == nil) {
			t.Errorf("%s: %+v", name, untrusted)
		}
	}
}

// Go's own verification, as a client without a CertTrust does it, yields the
// same error with the presented certificate's fingerprint.
func TestAsUntrustedCertReadsGoVerificationErrors(t *testing.T) {
	srv, _, fp := selfSigned(t)
	var got answer
	err := GetJSON(context.Background(), Client(5*time.Second), srv.URL, &got)
	untrusted, ok := AsUntrustedCert(err, "Test")
	if !ok || untrusted.Pinned || untrusted.Fingerprint != fp {
		t.Fatalf("AsUntrustedCert(%v) = %+v, %v", err, untrusted, ok)
	}
	if msg := untrusted.Error(); !strings.HasPrefix(msg, "the Test certificate is not trusted (x509: ") || !strings.HasSuffix(msg, "). The server presented the SHA-256 fingerprint "+fp) {
		t.Fatalf("message %q", msg)
	}
	if _, ok = AsUntrustedCert(errors.New("dial tcp: connection refused"), "Test"); ok {
		t.Fatal("a network error read as a refused certificate")
	}
	if _, ok = AsUntrustedCert(nil, "Test"); ok {
		t.Fatal("nil read as a refused certificate")
	}
}

func TestCertTrustClientKeepsTheBaseClientsPolicy(t *testing.T) {
	_, pin, _ := selfSigned(t)
	policy := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base := &http.Client{Timeout: 7 * time.Second, CheckRedirect: policy}
	c, done := CertTrust{Label: "Test", Pin: pin}.Client(base)
	defer done()
	if c == base || c.Timeout != base.Timeout || c.CheckRedirect == nil || base.Transport != nil {
		t.Fatalf("copy %+v of base %+v", c, base)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.VerifyConnection == nil || transport.Proxy == nil {
		t.Fatalf("transport %#v", c.Transport)
	}
}
