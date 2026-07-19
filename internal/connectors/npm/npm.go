package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Connector struct {
	Client *http.Client
	mu     sync.Mutex
	tokens map[string]string
}

func New() *Connector {
	return &Connector{Client: connectors.Client(connectors.DefaultTimeout), tokens: map[string]string{}}
}
func (*Connector) Kind() string { return "npm" }

type config struct {
	URL      string `json:"url"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func decode(raw connectors.Config) (config, error) {
	x, e := connectors.DecodeConfig[config](raw)
	if x.URL == "" || x.Email == "" || x.Password == "" {
		e = fmt.Errorf("url, email, and password are required")
	}
	return x, e
}

// certExpiryLayouts are tried in order against a certificate's expires_on
// value. NPM's API returns a space-separated timestamp ("2025-08-01 00:00:00"),
// not RFC3339, so a single RFC3339 parse silently failed and every NPM-managed
// certificate's not_after was stored as the zero time.
var certExpiryLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// parseExpiry returns the first layout that parses s, or the zero time if none
// match (callers leave not_after unset rather than crashing).
func parseExpiry(s string) time.Time {
	for _, layout := range certExpiryLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
func (c *Connector) token(ctx context.Context, x config) (string, error) {
	key := strings.TrimRight(x.URL, "/") + "|" + x.Email
	c.mu.Lock()
	cached := c.tokens[key]
	c.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	b, _ := json.Marshal(map[string]string{"identity": x.Email, "secret": x.Password})
	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(x.URL, "/")+"/api/tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, e := c.Client.Do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("NPM token API returned %s", res.Status)
	}
	var v struct {
		Token string `json:"token"`
	}
	e = json.NewDecoder(res.Body).Decode(&v)
	if e == nil {
		c.mu.Lock()
		if c.tokens == nil {
			c.tokens = map[string]string{}
		}
		c.tokens[key] = v.Token
		c.mu.Unlock()
	}
	return v.Token, e
}
func (c *Connector) get(ctx context.Context, x config, tok, path string, out any) error {
	return c.getAttempt(ctx, x, tok, path, out, true)
}
func (c *Connector) getAttempt(ctx context.Context, x config, tok, path string, out any, retry bool) error {
	e := connectors.GetJSON(ctx, c.Client, strings.TrimRight(x.URL, "/")+path, out,
		connectors.WithLabel("NPM"), connectors.WithBearerToken(tok))
	if retry {
		var se *connectors.StatusError
		if errors.As(e, &se) && se.StatusCode == http.StatusUnauthorized {
			c.mu.Lock()
			delete(c.tokens, strings.TrimRight(x.URL, "/")+"|"+x.Email)
			c.mu.Unlock()
			fresh, err := c.token(ctx, x)
			if err != nil {
				return err
			}
			return c.getAttempt(ctx, x, fresh, path, out, false)
		}
	}
	return e
}
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	x, e := decode(raw)
	if e != nil {
		return e
	}
	_, e = c.token(ctx, x)
	return e
}
func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	x, e := decode(raw)
	if e != nil {
		return domain.Snapshot{}, e
	}
	tok, e := c.token(ctx, x)
	if e != nil {
		return domain.Snapshot{}, e
	}
	var hs []struct {
		ID            int      `json:"id"`
		DomainNames   []string `json:"domain_names"`
		ForwardHost   string   `json:"forward_host"`
		ForwardPort   int      `json:"forward_port"`
		ForwardScheme string   `json:"forward_scheme"`
		CertificateID int      `json:"certificate_id"`
		Enabled       int      `json:"enabled"`
		Locations     []struct {
			Path        string `json:"path"`
			ForwardHost string `json:"forward_host"`
			ForwardPort int    `json:"forward_port"`
		} `json:"locations"`
	}
	if e = c.get(ctx, x, tok, "/api/nginx/proxy-hosts", &hs); e != nil {
		return domain.Snapshot{}, e
	}
	var cs []struct {
		ID          int
		DomainNames []string `json:"domain_names"`
		ExpiresOn   string   `json:"expires_on"`
		Provider    string
	}
	_ = c.get(ctx, x, tok, "/api/nginx/certificates", &cs)
	var s domain.Snapshot
	for _, h := range hs {
		if h.Enabled == 0 {
			continue
		}
		for _, d := range h.DomainNames {
			status := "unknown"
			s.Routes = append(s.Routes, domain.Route{Key: fmt.Sprintf("npm:%d:%s", h.ID, d), Domain: d, PathPrefix: "/", UpstreamHost: h.ForwardHost, UpstreamPort: h.ForwardPort, TLS: h.CertificateID > 0, Status: status})
			for _, l := range h.Locations {
				s.Routes = append(s.Routes, domain.Route{Key: fmt.Sprintf("npm:%d:%s:%s", h.ID, d, l.Path), Domain: d, PathPrefix: l.Path, UpstreamHost: l.ForwardHost, UpstreamPort: l.ForwardPort, TLS: h.CertificateID > 0, Status: status})
			}
		}
	}
	for _, z := range cs {
		t := parseExpiry(z.ExpiresOn)
		for _, d := range z.DomainNames {
			endpoint := d + ":443"
			s.Certs = append(s.Certs, domain.Cert{Key: "tls:" + endpoint, Subject: d, SANs: z.DomainNames, NotAfter: t, Source: "proxy", Endpoint: endpoint})
		}
	}
	return s, nil
}
