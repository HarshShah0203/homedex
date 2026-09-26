package pihole

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
func (*Connector) Kind() string { return "pihole" }

type config struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

func (c *Connector) auth(ctx context.Context, cfg config) (string, error) {
	if cfg.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if cfg.Password == "" {
		return "", fmt.Errorf("password is required")
	}

	baseURL := strings.TrimRight(cfg.URL, "/")
	reqBody := map[string]string{"password": cfg.Password}
	var resBody struct {
		Session struct {
			Sid string `json:"sid"`
		} `json:"session"`
	}

	err := connectors.PostJSON(ctx, c.Client, baseURL+"/api/auth", reqBody, &resBody, connectors.WithLabel("Pi-hole"))
	if err != nil {
		return "", err
	}
	if resBody.Session.Sid == "" {
		return "", fmt.Errorf("no session sid returned")
	}
	return resBody.Session.Sid, nil
}

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	cfg, err := connectors.DecodeConfig[config](raw)
	if err != nil {
		return err
	}
	_, err = c.auth(ctx, cfg)
	return err
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	cfg, err := connectors.DecodeConfig[config](raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	sid, err := c.auth(ctx, cfg)
	if err != nil {
		return domain.Snapshot{}, err
	}

	baseURL := strings.TrimRight(cfg.URL, "/")
	
	var hostsRes struct {
		Config struct {
			DNS struct {
				Hosts []struct {
					IP   string `json:"ip"`
					Name string `json:"name"`
				} `json:"hosts"`
			} `json:"dns"`
		} `json:"config"`
	}
	err = connectors.GetJSON(ctx, c.Client, baseURL+"/api/config/dns/hosts", &hostsRes, connectors.WithLabel("Pi-hole"), connectors.WithHeader("sid", sid))
	if err != nil {
		return domain.Snapshot{}, err
	}

	var cnamesRes struct {
		Config struct {
			DNS struct {
				CnameRecords []struct {
					Domain string `json:"domain"`
					Target string `json:"target"`
				} `json:"cnameRecords"`
			} `json:"dns"`
		} `json:"config"`
	}
	err = connectors.GetJSON(ctx, c.Client, baseURL+"/api/config/dns/cnameRecords", &cnamesRes, connectors.WithLabel("Pi-hole"), connectors.WithHeader("sid", sid))
	if err != nil {
		return domain.Snapshot{}, err
	}

	var snap domain.Snapshot
	
	for _, h := range hostsRes.Config.DNS.Hosts {
		snap.Hosts = append(snap.Hosts, domain.Host{
			Key:       "pihole:" + h.Name,
			Name:      h.Name,
			Addresses: []string{h.IP},
			Kind:      "dns",
		})
	}

	for _, cname := range cnamesRes.Config.DNS.CnameRecords {
		snap.Hosts = append(snap.Hosts, domain.Host{
			Key:       "pihole:" + cname.Domain,
			Name:      cname.Domain,
			Addresses: []string{cname.Target},
			Kind:      "dns",
		})
	}

	return snap, nil
}