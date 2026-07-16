package tlsprobe

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestProbeCapturesInvalidSelfSignedCertificate(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	until := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	der, _ := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "homedex.test"}, Issuer: pkix.Name{CommonName: "Fixture CA"}, DNSNames: []string{"homedex.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "homedex.test"}, DNSNames: []string{"homedex.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: until, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pair, _ := tls.X509KeyPair(certPEM, keyPEM)
	ln, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if tc, ok := c.(*tls.Conn); ok {
					_ = tc.Handshake()
				}
			}(c)
		}
	}()
	snap, e := New().Scan(context.Background(), connectors.Config{"targets": json.RawMessage(`[` + strconvQuote(ln.Addr().String()) + `]`), "timeout_seconds": json.RawMessage(`2`)})
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Certs) != 1 || snap.Certs[0].Subject != "homedex.test" || snap.Certs[0].ChainValid || !snap.Certs[0].NotAfter.Equal(until) {
		t.Fatalf("cert=%#v", snap.Certs)
	}
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func TestCanonicalTargetAcceptsURLsAndIPv6(t *testing.T) {
	for input, want := range map[string]string{"https://Example.COM/path": "example.com:443", "example.com:8443": "example.com:8443", "::1": "[::1]:443"} {
		got, e := canonicalTarget(input)
		if e != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", input, got, e, want)
		}
	}
}
