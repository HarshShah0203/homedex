package adguardhome

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

type Connector struct{ Client *http.Client }

func New() *Connector           { return &Connector{connectors.Client(connectors.DefaultTimeout)} }
func (*Connector) Kind() string { return "adguardhome" }

type config struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func endpoint(raw connectors.Config) (config, error) {
	x, e := connectors.DecodeConfig[config](raw)
	if e != nil {
		return config{}, e
	}
	if x.URL == "" {
		return config{}, fmt.Errorf("url is required")
	}
	x.URL = strings.TrimRight(x.URL, "/") + "/control/"
	return x, nil
}

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	conf, e := endpoint(raw)
	if e != nil {
		return e
	}
	var v any
	return connectors.GetJSON(ctx, c.Client, conf.URL+"status", &v, connectors.WithLabel("AdGuard Home"), connectors.WithBasicAuth(conf.Username, conf.Password))
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	conf, e := endpoint(raw)
	if e != nil {
		return domain.Snapshot{}, e
	}

	var rewrites []struct {
		Domain string `json:"domain"`
		Answer string `json:"answer"`
	}
	if e := connectors.GetJSON(ctx, c.Client, conf.URL+"rewrite/list", &rewrites, connectors.WithLabel("AdGuard Home"), connectors.WithBasicAuth(conf.Username, conf.Password)); e != nil {
		return domain.Snapshot{}, e
	}

	var settings any
	_ = connectors.GetJSON(ctx, c.Client, conf.URL+"rewrite/settings", &settings, connectors.WithLabel("AdGuard Home"), connectors.WithBasicAuth(conf.Username, conf.Password))

	var dhcp struct {
		StaticLeases []struct {
			Hostname string `json:"hostname"`
			IP       string `json:"ip"`
		} `json:"static_leases"`
	}
	_ = connectors.GetJSON(ctx, c.Client, conf.URL+"dhcp/status", &dhcp, connectors.WithLabel("AdGuard Home"), connectors.WithBasicAuth(conf.Username, conf.Password))

	var hosts []domain.Host
	for _, r := range rewrites {
		if r.Answer != "" && r.Domain != "" {
			hosts = append(hosts, domain.Host{
				Key:       "adguardhome:rewrite:" + r.Domain,
				Kind:      "dns",
				Name:      r.Domain,
				Addresses: []string{r.Answer},
			})
		}
	}
	for _, l := range dhcp.StaticLeases {
		if l.IP != "" && l.Hostname != "" {
			hosts = append(hosts, domain.Host{
				Key:       "adguardhome:dhcp:" + l.Hostname,
				Kind:      "dns",
				Name:      l.Hostname,
				Addresses: []string{l.IP},
			})
		}
	}

	return domain.Snapshot{Hosts: hosts}, nil
}