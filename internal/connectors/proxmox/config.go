package proxmox

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/HarshShah0203/homedex/internal/connectors"
)

const (
	defaultPort = "8006"
	maxCABytes  = 64 << 10
)

type config struct {
	URL         string `json:"url"`
	TokenID     string `json:"token_id"`
	TokenSecret string `json:"token_secret"`
	Fingerprint string `json:"fingerprint"`
	CAPEM       string `json:"ca_pem"`
}

// settings is a validated config.
type settings struct {
	base  string // scheme://host:port
	host  string // host name or IP the certificate must name
	https bool
	auth  string
	pin   []byte         // SHA-256 of the leaf certificate, when pinned
	roots *x509.CertPool // the configured CA, or nil for the system roots
}

// PVE's own patterns: user@realm!tokenname. A user name may itself contain
// '@' (an email address in an LDAP, AD or OpenID realm); the realm starts
// after the last one, since a realm cannot contain '@'. A '!' in the user
// name is refused, as it would make the token name ambiguous.
var tokenIDRE = regexp.MustCompile(`^[^\s:/!]+@[A-Za-z][A-Za-z0-9._-]*![A-Za-z][A-Za-z0-9._-]*$`)

// decode validates without ever quoting a credential back: the error text
// reaches the UI and the scan log.
func decode(raw connectors.Config) (settings, error) {
	x, err := connectors.DecodeConfig[config](raw)
	if err != nil {
		return settings{}, err
	}
	var s settings
	if s.base, s.host, s.https, err = normalizeURL(x.URL); err != nil {
		return settings{}, err
	}
	tokenID, secret := strings.TrimSpace(x.TokenID), strings.TrimSpace(x.TokenSecret)
	switch {
	case tokenID == "":
		return settings{}, errors.New("an API token ID such as homedex@pve!inventory is required")
	case strings.HasPrefix(tokenID, "PVEAPIToken") || strings.Contains(tokenID, "="):
		return settings{}, errors.New("enter only the token ID (user@realm!name) as the token ID, and the secret in the token secret field")
	case !strings.Contains(tokenID, "!"):
		return settings{}, errors.New("that is a user, not an API token: create a token under Datacenter > Permissions > API Tokens and enter its ID, such as homedex@pve!inventory")
	case len(tokenID) > 256 || !tokenIDRE.MatchString(tokenID):
		return settings{}, errors.New("the token ID must look like user@realm!tokenname, such as homedex@pve!inventory")
	case secret == "":
		return settings{}, errors.New("the API token secret is required; Proxmox shows it once, when the token is created")
	case len(secret) > 512 || strings.ContainsFunc(secret, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return settings{}, errors.New("the API token secret must be the single value Proxmox showed, without spaces")
	}
	s.auth = "PVEAPIToken=" + tokenID + "=" + secret

	fingerprint, ca := strings.TrimSpace(x.Fingerprint), strings.TrimSpace(x.CAPEM)
	switch {
	case fingerprint != "" && ca != "":
		return settings{}, errors.New("set either a certificate fingerprint or a CA, not both")
	case (fingerprint != "" || ca != "") && !s.https:
		return settings{}, errors.New("a certificate fingerprint or CA applies only to an https URL")
	case fingerprint != "":
		if s.pin, err = parseFingerprint(fingerprint); err != nil {
			return settings{}, err
		}
	case ca != "":
		if s.roots, err = parseCA(ca); err != nil {
			return settings{}, err
		}
	}
	return s, nil
}

// normalizeURL accepts what an operator is likely to paste, including the web
// UI's address with its #v1:... fragment, and reduces it to scheme://host:port.
func normalizeURL(raw string) (base, host string, https bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false, errors.New("the Proxmox URL is required, such as https://pve.lab.example:8006")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, perr := url.Parse(raw)
	if perr != nil || len(raw) > 2048 || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return "", "", false, errors.New("the Proxmox URL must be an https URL such as https://pve.lab.example:8006")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return "", "", false, errors.New("the Proxmox URL must not contain credentials or a query")
	}
	switch strings.TrimSuffix(u.Path, "/") {
	case "", "/api2/json":
	default:
		return "", "", false, errors.New("the Proxmox URL must not contain a path; use the node's address, such as https://pve.lab.example:8006")
	}
	host = u.Hostname()
	// http exists for local mocks and SSH tunnels; anywhere else it would put
	// the token on the wire in cleartext.
	if u.Scheme == "http" && !loopbackHost(host) {
		return "", "", false, errors.New("the Proxmox URL must use https unless its host is loopback (127.0.0.1, ::1 or localhost): http would send the token in cleartext")
	}
	// "LOCALHOST" and "localhost." are the same name; spelled any other way
	// than "localhost" it would miss the proxy bypass and the hosts file.
	if _, ipErr := netip.ParseAddr(host); ipErr != nil && loopbackHost(host) {
		host = "localhost"
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port), host, u.Scheme == "https", nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Unmap().IsLoopback()
}

// parseFingerprint accepts Proxmox's AA:BB:... form in any case, with or
// without colons, and with the "SHA256 Fingerprint=" prefix openssl prints.
func parseFingerprint(s string) ([]byte, error) {
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

func parseCA(pemText string) (*x509.CertPool, error) {
	if len(pemText) > maxCABytes {
		return nil, errors.New("the CA certificate is too long; paste only /etc/pve/pve-root-ca.pem")
	}
	if strings.Contains(pemText, "PRIVATE KEY") {
		return nil, errors.New("that is a private key; paste the CA certificate (/etc/pve/pve-root-ca.pem) instead, and treat the key as exposed")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pemText)) {
		return nil, errors.New("the CA must be a PEM certificate, such as the contents of /etc/pve/pve-root-ca.pem")
	}
	return pool, nil
}

func formatFingerprint(sum []byte) string {
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// untrustedError reports a certificate Homedex will not accept, with the
// fingerprint the server presented so the operator can compare and pin it.
type untrustedError struct {
	fingerprint string
	pinned      bool
	cause       error
}

func (e *untrustedError) Error() string {
	if e.pinned {
		return fmt.Sprintf("the Proxmox certificate does not match the pinned fingerprint: the server presented %s. A renewed node certificate changes its fingerprint; pin the cluster CA (/etc/pve/pve-root-ca.pem) to survive renewals", e.fingerprint)
	}
	return fmt.Sprintf("the Proxmox certificate is not trusted (%v). The server presented the SHA-256 fingerprint %s. If it matches the one under the node's System > Certificates, paste it into the fingerprint field to pin it, or paste the cluster CA from /etc/pve/pve-root-ca.pem", e.cause, e.fingerprint)
}

// tlsConfig verifies the server itself, in VerifyConnection, which Go runs on
// every connection including resumed ones: against the pin, or against the
// configured CA or the system roots for the configured host name. That is
// why InsecureSkipVerify is set; nothing is ever accepted unverified.
func tlsConfig(s settings) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // #nosec G402 -- verified in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the Proxmox server presented no certificate")
			}
			leaf := cs.PeerCertificates[0]
			sum := sha256.Sum256(leaf.Raw)
			if s.pin != nil {
				if subtle.ConstantTimeCompare(sum[:], s.pin) == 1 {
					return nil
				}
				return &untrustedError{fingerprint: formatFingerprint(sum[:]), pinned: true}
			}
			opts := x509.VerifyOptions{Roots: s.roots, DNSName: s.host, Intermediates: x509.NewCertPool()}
			for _, cert := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(cert)
			}
			if _, err := leaf.Verify(opts); err != nil {
				return &untrustedError{fingerprint: formatFingerprint(sum[:]), cause: err}
			}
			return nil
		},
	}
}
