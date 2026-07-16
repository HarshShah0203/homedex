package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

type Connector struct{ Client *http.Client }

func New() *Connector           { return &Connector{http.DefaultClient} }
func (*Connector) Kind() string { return "caddy" }
func endpoint(raw connectors.Config) (string, error) {
	x, e := connectors.DecodeConfig[struct {
		URL string `json:"url"`
	}](raw)
	if e != nil {
		return "", e
	}
	if x.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	return strings.TrimRight(x.URL, "/") + "/config/", nil
}
func (c *Connector) load(ctx context.Context, raw connectors.Config) (any, error) {
	u, e := endpoint(raw)
	if e != nil {
		return nil, e
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	res, e := c.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Caddy API returned %s", res.Status)
	}
	var v any
	e = json.NewDecoder(res.Body).Decode(&v)
	return v, e
}
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	_, e := c.load(ctx, raw)
	return e
}
func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	v, e := c.load(ctx, raw)
	if e != nil {
		return domain.Snapshot{}, e
	}
	var routes []domain.Route
	walk(v, nil, "/", &routes)
	return domain.Snapshot{Routes: routes}, nil
}
func walk(v any, hosts []string, path string, out *[]domain.Route) {
	switch x := v.(type) {
	case []any:
		for _, z := range x {
			walk(z, hosts, path, out)
		}
	case map[string]any:
		if m, ok := x["match"].([]any); ok {
			for _, mm := range m {
				if z, ok := mm.(map[string]any); ok {
					if hs, ok := z["host"].([]any); ok {
						hosts = nil
						for _, h := range hs {
							hosts = append(hosts, fmt.Sprint(h))
						}
					}
					if ps, ok := z["path"].([]any); ok && len(ps) > 0 {
						path = strings.TrimSuffix(fmt.Sprint(ps[0]), "*")
					}
				}
			}
		}
		if x["handler"] == "reverse_proxy" {
			if us, ok := x["upstreams"].([]any); ok {
				for _, u := range us {
					m, _ := u.(map[string]any)
					dial := fmt.Sprint(m["dial"])
					host, port := splitDial(dial)
					for _, h := range hosts {
						*out = append(*out, domain.Route{Key: "caddy:" + h + ":" + path + ":" + dial, Domain: h, PathPrefix: path, UpstreamHost: host, UpstreamPort: port, Status: "unknown", TLS: true})
					}
				}
			}
		}
		for k, z := range x {
			if k != "match" && k != "upstreams" {
				walk(z, hosts, path, out)
			}
		}
	}
}
func splitDial(s string) (string, int) {
	u, e := url.Parse("tcp://" + s)
	if e != nil {
		return s, 0
	}
	var p int
	_, _ = fmt.Sscanf(u.Port(), "%d", &p)
	return u.Hostname(), p
}
