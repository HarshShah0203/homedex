package rdap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"golang.org/x/net/publicsuffix"
)

type Connector struct {
	Client         *http.Client
	BootstrapURL   string
	mu             sync.Mutex
	bootstrapCache map[string]string
	bootstrapUntil time.Time
	domainCache    map[string]cachedDomain
}

type cachedDomain struct {
	value domain.Domain
	until time.Time
}

func New() *Connector {
	return &Connector{Client: connectors.Client(connectors.DefaultTimeout), BootstrapURL: "https://data.iana.org/rdap/dns.json", domainCache: map[string]cachedDomain{}}
}
func (*Connector) Kind() string { return "rdap" }

type config struct {
	Domains []string `json:"domains"`
}

func parse(raw connectors.Config) (config, error) {
	x, e := connectors.DecodeConfig[config](raw)
	if len(x.Domains) == 0 {
		return x, fmt.Errorf("at least one domain is required")
	}
	return x, e
}
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	x, e := parse(raw)
	if e != nil {
		return e
	}
	_, e = registrable(x.Domains[0])
	return e
}
func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	x, e := parse(raw)
	if e != nil {
		return domain.Snapshot{}, e
	}
	boot, e := c.bootstrap(ctx)
	if e != nil {
		return domain.Snapshot{}, e
	}
	seen := map[string]bool{}
	var s domain.Snapshot
	for _, input := range x.Domains {
		d, e := registrable(input)
		if e != nil || seen[d] {
			continue
		}
		seen[d] = true
		if z, ok := c.cached(d); ok {
			s.Domains = append(s.Domains, z)
			continue
		}
		suffix, _ := publicsuffix.PublicSuffix(d)
		base := boot[strings.TrimPrefix(suffix, ".")]
		if base == "" {
			s.Domains = append(s.Domains, unknown(d))
			continue
		}
		if e = throttle(ctx); e != nil {
			return s, e
		}
		var v struct {
			Events []struct {
				Action string    `json:"eventAction"`
				Date   time.Time `json:"eventDate"`
			} `json:"events"`
			Nameservers []struct {
				LDHName string `json:"ldhName"`
			} `json:"nameservers"`
			Entities []struct {
				Roles []string `json:"roles"`
				VCard []any    `json:"vcardArray"`
			} `json:"entities"`
		}
		// RDAP servers are discovered from IANA bootstrap data, not chosen by
		// the administrator, so the shared size-capped GetJSON guards against a
		// hostile server streaming unbounded JSON. Any failure (network,
		// non-2xx, or decode) degrades to an "unknown" record exactly as
		// before; a cancelled context still aborts the whole scan.
		reqURL := strings.TrimRight(base, "/") + "/domain/" + url.PathEscape(d)
		if err := connectors.GetJSON(ctx, c.Client, reqURL, &v); err != nil {
			if ctx.Err() != nil {
				return s, ctx.Err()
			}
			s.Domains = append(s.Domains, unknown(d))
			continue
		}
		now := time.Now().UTC()
		z := domain.Domain{Key: "rdap:" + d, Name: d, Source: "rdap", LastChecked: &now}
		for _, ev := range v.Events {
			if ev.Action == "expiration" {
				t := ev.Date
				z.ExpiresAt = &t
			}
		}
		for _, n := range v.Nameservers {
			z.Nameservers = append(z.Nameservers, n.LDHName)
		}
		z.Registrar = registrar(v.Entities)
		s.Domains = append(s.Domains, z)
		c.mu.Lock()
		if c.domainCache == nil {
			c.domainCache = map[string]cachedDomain{}
		}
		c.domainCache[d] = cachedDomain{z, time.Now().Add(24 * time.Hour)}
		c.mu.Unlock()
	}
	return s, nil
}
func unknown(name string) domain.Domain {
	now := time.Now().UTC()
	return domain.Domain{Key: "rdap:" + name, Name: name, Source: "rdap", LastChecked: &now}
}
func registrar(entities []struct {
	Roles []string `json:"roles"`
	VCard []any    `json:"vcardArray"`
}) string {
	for _, e := range entities {
		isRegistrar := false
		for _, r := range e.Roles {
			if r == "registrar" {
				isRegistrar = true
			}
		}
		if !isRegistrar || len(e.VCard) < 2 {
			continue
		}
		props, ok := e.VCard[1].([]any)
		if !ok {
			continue
		}
		for _, p := range props {
			fields, ok := p.([]any)
			if ok && len(fields) >= 4 && fmt.Sprint(fields[0]) == "fn" {
				return fmt.Sprint(fields[3])
			}
		}
	}
	return ""
}
func registrable(s string) (string, error) {
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	if net.ParseIP(s) != nil || !strings.Contains(s, ".") {
		return "", fmt.Errorf("not registrable")
	}
	d, e := publicsuffix.EffectiveTLDPlusOne(s)
	if e != nil || strings.HasSuffix(d, ".local") || strings.HasSuffix(d, ".lan") || strings.HasSuffix(d, ".internal") {
		return "", fmt.Errorf("not registrable")
	}
	return d, nil
}
func (c *Connector) bootstrap(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	if time.Now().Before(c.bootstrapUntil) && c.bootstrapCache != nil {
		out := c.bootstrapCache
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()
	req, _ := http.NewRequestWithContext(ctx, "GET", c.BootstrapURL, nil)
	res, e := c.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	var b struct {
		Services [][][]string `json:"services"`
	}
	// Size-cap the bootstrap read as well; behavior is otherwise unchanged.
	if e = json.NewDecoder(io.LimitReader(res.Body, connectors.MaxResponseBytes)).Decode(&b); e != nil {
		return nil, e
	}
	out := map[string]string{}
	for _, svc := range b.Services {
		if len(svc) < 2 || len(svc[1]) == 0 {
			continue
		}
		for _, tld := range svc[0] {
			out[strings.ToLower(tld)] = svc[1][0]
		}
	}
	c.mu.Lock()
	c.bootstrapCache = out
	c.bootstrapUntil = time.Now().Add(7 * 24 * time.Hour)
	c.mu.Unlock()
	return out, nil
}
func (c *Connector) cached(name string) (domain.Domain, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	z, ok := c.domainCache[name]
	return z.value, ok && time.Now().Before(z.until)
}

var rateMu sync.Mutex
var lastRequest time.Time

func throttle(ctx context.Context) error {
	rateMu.Lock()
	defer rateMu.Unlock()
	wait := time.Until(lastRequest.Add(time.Second))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	lastRequest = time.Now()
	return nil
}
