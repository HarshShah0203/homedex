package connectors

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// ParseFingerprint reads a certificate's SHA-256 fingerprint as an operator
// pastes it: the AA:BB:... form a failed Test connection shows, in any case,
// with or without colons, and with the "SHA256 Fingerprint=" prefix openssl
// prints. The error never quotes the input.
func ParseFingerprint(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "="); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "SHA256:"), "sha256:")
	s = strings.Map(func(r rune) rune {
		if r == ':' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	pin, err := hex.DecodeString(s)
	if err != nil || len(pin) != sha256.Size {
		return nil, errors.New("the fingerprint must be the certificate's SHA-256 fingerprint, 32 hex bytes such as 3A:9F:...; Test connection shows the one the server presents")
	}
	return pin, nil
}

// FormatFingerprint writes a SHA-256 sum as AA:BB:..., the form
// ParseFingerprint reads back.
func FormatFingerprint(sum []byte) string {
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func leafFingerprint(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.Raw)
	return FormatFingerprint(sum[:])
}

// UntrustedCertError is a server certificate Homedex refused, with the
// SHA-256 fingerprint of the one the server presented so the operator can
// compare it and pin it. A connector adds its own advice by wrapping it.
type UntrustedCertError struct {
	Label       string // the connector's name, such as "Proxmox"
	Fingerprint string // of the presented leaf, as FormatFingerprint writes it
	Pinned      bool   // a pin was set, and this certificate is not the pinned one
	Cause       error  // why verification failed, when no pin was set
}

func (e *UntrustedCertError) Error() string {
	if e.Pinned {
		return fmt.Sprintf("the %s certificate does not match the pinned fingerprint: the server presented %s", e.Label, e.Fingerprint)
	}
	return fmt.Sprintf("the %s certificate is not trusted (%v). The server presented the SHA-256 fingerprint %s", e.Label, e.Cause, e.Fingerprint)
}

// AsUntrustedCert finds a refused server certificate in err: the
// *UntrustedCertError a CertTrust fails with, or the
// *tls.CertificateVerificationError of Go's own verification, which it
// rewords the same way under label, with the presented certificate's
// fingerprint.
func AsUntrustedCert(err error, label string) (*UntrustedCertError, bool) {
	var untrusted *UntrustedCertError
	if errors.As(err, &untrusted) {
		return untrusted, true
	}
	var verify *tls.CertificateVerificationError
	if errors.As(err, &verify) && len(verify.UnverifiedCertificates) > 0 {
		return &UntrustedCertError{Label: label, Fingerprint: leafFingerprint(verify.UnverifiedCertificates[0]), Cause: verify.Err}, true
	}
	return nil, false
}

// CertTrust is which server certificate a connector accepts: with Pin set,
// only the leaf certificate whose SHA-256 is Pin, whoever issued it and
// whatever names it carries; without one, a chain to Roots, or to the system
// roots when Roots is nil, for the host name Host.
type CertTrust struct {
	Label string // the connector's name, for errors
	Host  string // the name or IP the certificate must carry; required when not pinned
	Pin   []byte
	Roots *x509.CertPool
}

// TLSConfig verifies the server itself, in VerifyConnection, which Go runs on
// every connection including resumed ones: against the pin, or against the
// roots for the host name. That is why InsecureSkipVerify is set; nothing is
// ever accepted unverified. A refused certificate fails the handshake with an
// *UntrustedCertError, before any request is sent.
func (t CertTrust) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // #nosec G402 -- verified in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("the %s server presented no certificate", t.Label)
			}
			leaf := cs.PeerCertificates[0]
			sum := sha256.Sum256(leaf.Raw)
			if t.Pin != nil {
				if subtle.ConstantTimeCompare(sum[:], t.Pin) == 1 {
					return nil
				}
				return &UntrustedCertError{Label: t.Label, Fingerprint: FormatFingerprint(sum[:]), Pinned: true}
			}
			// An empty DNSName would skip the name check altogether.
			if t.Host == "" {
				return fmt.Errorf("the %s certificate cannot be verified without a host name", t.Label)
			}
			opts := x509.VerifyOptions{Roots: t.Roots, DNSName: t.Host, Intermediates: x509.NewCertPool()}
			for _, cert := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(cert)
			}
			if _, err := leaf.Verify(opts); err != nil {
				return &UntrustedCertError{Label: t.Label, Fingerprint: FormatFingerprint(sum[:]), Cause: err}
			}
			return nil
		},
	}
}

// Client returns a copy of client whose HTTPS connections accept only the
// certificate t allows, and a function that closes its idle connections once
// the caller is done with it. The copy keeps client's timeout and redirect
// policy, so GetJSON, PostJSON and PostForm apply their redirect rules and
// size limit to it as to any client. Its transport is a clone of client's own
// *http.Transport, or of Go's default one when client has another kind, with
// only the TLS configuration replaced.
func (t CertTrust) Client(client *http.Client) (*http.Client, func()) {
	base, ok := client.Transport.(*http.Transport)
	if !ok || base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	transport := base.Clone()
	transport.TLSClientConfig = t.TLSConfig()
	c := *client
	c.Transport = transport
	return &c, transport.CloseIdleConnections
}
