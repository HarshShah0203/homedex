package traefik

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

type Connector struct{ Client *http.Client }

func New() *Connector           { return &Connector{Client: http.DefaultClient} }
func (*Connector) Kind() string { return "traefik" }

type config struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Header      string `json:"header"`
	HeaderValue string `json:"header_value"`
}

func cfg(raw connectors.Config) (config, error) {
	c, err := connectors.DecodeConfig[config](raw)
	if err != nil {
		return config{}, err
	}
	if _, err := url.ParseRequestURI(c.URL); err != nil {
		return config{}, fmt.Errorf("invalid URL: %w", err)
	}
	return c, nil
}
func (c *Connector) get(ctx context.Context, x config, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(x.URL, "/")+path, nil)
	if x.Username != "" {
		req.SetBasicAuth(x.Username, x.Password)
	}
	if x.Header != "" {
		req.Header.Set(x.Header, x.HeaderValue)
	}
	res, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("Traefik API returned %s", res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	x, e := cfg(raw)
	if e != nil {
		return e
	}
	var v any
	return c.get(ctx, x, "/api/version", &v)
}
func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	x, e := cfg(raw)
	if e != nil {
		return domain.Snapshot{}, e
	}
	var metadata any
	if e = c.get(ctx, x, "/api/version", &metadata); e != nil {
		return domain.Snapshot{}, e
	}
	if e = c.get(ctx, x, "/api/entrypoints", &metadata); e != nil {
		return domain.Snapshot{}, e
	}
	var routers []struct {
		Name, Service, Rule string
		TLS                 any
	}
	var services []struct {
		Name         string
		LoadBalancer *struct{ Servers []struct{ URL string } }
	}
	if e = c.get(ctx, x, "/api/http/routers", &routers); e != nil {
		return domain.Snapshot{}, e
	}
	if e = c.get(ctx, x, "/api/http/services", &services); e != nil {
		return domain.Snapshot{}, e
	}
	ups := map[string][]string{}
	for _, s := range services {
		if s.LoadBalancer != nil {
			for _, u := range s.LoadBalancer.Servers {
				ups[s.Name] = append(ups[s.Name], u.URL)
			}
		}
	}
	var snap domain.Snapshot
	for _, r := range routers {
		hosts := ruleValues(r.Rule, "Host")
		paths := ruleValues(r.Rule, "PathPrefix")
		if len(paths) == 0 {
			paths = []string{"/"}
		}
		for _, h := range hosts {
			for _, p := range paths {
				for _, up := range upstreamsFor(r.Service, r.Name, ups) {
					u, err := url.Parse(up)
					if err != nil {
						continue
					}
					port := portOf(u)
					snap.Routes = append(snap.Routes, domain.Route{Key: "traefik:" + r.Name + ":" + h + ":" + p + ":" + up, Domain: h, PathPrefix: p, UpstreamHost: u.Hostname(), UpstreamPort: port, TLS: r.TLS != nil, Status: "unknown"})
				}
			}
		}
	}
	return snap, nil
}

// upstreamsFor resolves a router's service reference against the service map.
// Traefik's API names services with a provider suffix ("whoami@docker") while
// routers reference them unsuffixed ("whoami"), so an exact match is tried
// first, then the reference qualified with the router's own provider, then a
// unique suffix-insensitive match.
func upstreamsFor(ref, routerName string, ups map[string][]string) []string {
	if v, ok := ups[ref]; ok {
		return v
	}
	if !strings.Contains(ref, "@") {
		if i := strings.Index(routerName, "@"); i >= 0 {
			if v, ok := ups[ref+routerName[i:]]; ok {
				return v
			}
		}
		var match []string
		count := 0
		for name, v := range ups {
			if strings.HasPrefix(name, ref+"@") {
				match = v
				count++
			}
		}
		if count == 1 {
			return match
		}
	}
	return nil
}

var argRE = regexp.MustCompile("`([^`]+)`|'([^']+)'|\"([^\"]+)\"")

func ruleValues(rule, fn string) []string {
	var out []string
	needle := fn + "("
	for {
		i := strings.Index(rule, needle)
		if i < 0 {
			return out
		}
		rest := rule[i+len(needle):]
		j := strings.Index(rest, ")")
		if j < 0 {
			return out
		}
		for _, m := range argRE.FindAllStringSubmatch(rest[:j], -1) {
			for k := 1; k < len(m); k++ {
				if m[k] != "" {
					out = append(out, m[k])
					break
				}
			}
		}
		rule = rest[j+1:]
	}
}
func portOf(u *url.URL) int {
	if u.Port() != "" {
		var p int
		_, _ = fmt.Sscanf(u.Port(), "%d", &p)
		return p
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}
