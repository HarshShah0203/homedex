package tlsprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

type Connector struct {
	Dial func(context.Context, string, string) (*tls.Conn, error)
}

func New() *Connector           { return &Connector{Dial: dial} }
func (*Connector) Kind() string { return "tlsprobe" }

type config struct {
	Targets        []string `json:"targets"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

func parse(raw connectors.Config) (config, error) {
	x, e := connectors.DecodeConfig[config](raw)
	if len(x.Targets) == 0 {
		return x, fmt.Errorf("at least one target is required")
	}
	if x.TimeoutSeconds == 0 {
		x.TimeoutSeconds = 5
	}
	return x, e
}
func dial(ctx context.Context, address, serverName string) (*tls.Conn, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{ServerName: serverName, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	c, e := d.DialContext(ctx, "tcp", address)
	if e != nil {
		return nil, e
	}
	return c.(*tls.Conn), nil
}
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	x, e := parse(raw)
	if e != nil {
		return e
	}
	_, e = c.probe(ctx, x.Targets[0], time.Duration(x.TimeoutSeconds)*time.Second)
	return e
}
func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	x, e := parse(raw)
	if e != nil {
		return domain.Snapshot{}, e
	}
	targets := unique(x.Targets)
	out := make([]domain.Cert, len(targets))
	errs := make([]error, len(targets))
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			out[i], errs[i] = c.probe(ctx, t, time.Duration(x.TimeoutSeconds)*time.Second)
		}(i, t)
	}
	wg.Wait()
	var snap domain.Snapshot
	for i, z := range out {
		if errs[i] == nil {
			snap.Certs = append(snap.Certs, z)
		}
	}
	if len(snap.Certs) == 0 {
		return snap, fmt.Errorf("all TLS probes failed: %v", errs)
	}
	for i, e := range errs {
		if e != nil {
			host, _, _ := net.SplitHostPort(targets[i])
			snap.Certs = append(snap.Certs, domain.Cert{Key: "tls:" + targets[i], Subject: host, Source: "probe", Endpoint: targets[i]})
		}
	}
	return snap, nil
}
func (c *Connector) probe(ctx context.Context, target string, timeout time.Duration) (domain.Cert, error) {
	target, e := canonicalTarget(target)
	if e != nil {
		return domain.Cert{}, e
	}
	host, _, _ := net.SplitHostPort(target)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, e := c.Dial(pctx, target, host)
	if e != nil {
		return domain.Cert{}, e
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return domain.Cert{}, fmt.Errorf("no peer certificate")
	}
	leaf := state.PeerCertificates[0]
	opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
	for _, z := range state.PeerCertificates[1:] {
		opts.Intermediates.AddCert(z)
	}
	_, verifyErr := leaf.Verify(opts)
	return domain.Cert{Key: "tls:" + target, Subject: leaf.Subject.CommonName, SANs: leaf.DNSNames, Issuer: leaf.Issuer.CommonName, NotAfter: leaf.NotAfter, ChainValid: verifyErr == nil, Source: "probe", Endpoint: target}, nil
}
func unique(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		if target, e := canonicalTarget(s); e == nil {
			m[target] = true
		}
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func canonicalTarget(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.Contains(s, "://") {
		u, e := url.Parse(s)
		if e != nil {
			return "", e
		}
		s = u.Host
	}
	if host, port, e := net.SplitHostPort(s); e == nil {
		return net.JoinHostPort(host, port), nil
	}
	if s == "" {
		return "", fmt.Errorf("empty TLS target")
	}
	if ip := net.ParseIP(s); ip != nil {
		return net.JoinHostPort(s, "443"), nil
	}
	if strings.Contains(s, ":") {
		return "", fmt.Errorf("invalid TLS target %q", s)
	}
	return net.JoinHostPort(s, "443"), nil
}
