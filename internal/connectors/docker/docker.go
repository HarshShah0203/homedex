package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
)

type Config struct {
	Endpoint    string `json:"endpoint"`
	HostName    string `json:"host_name"`
	HostAddress string `json:"host_address"`
	TLSVerify   bool   `json:"tls_verify"`
	CACert      string `json:"ca_cert"`
	ClientCert  string `json:"client_cert"`
	ClientKey   string `json:"client_key"`
}

type API interface {
	ContainerList(context.Context, container.ListOptions) ([]types.Container, error)
	ContainerInspect(context.Context, string) (types.ContainerJSON, error)
	Info(context.Context) (system.Info, error)
	ServerVersion(context.Context) (types.Version, error)
	Close() error
}

type Connector struct{ newClient func(Config) (API, error) }

func New() *Connector             { return &Connector{newClient: newClient} }
func (c *Connector) Kind() string { return "docker" }

func decode(raw connectors.Config) (Config, error) {
	cfg, err := connectors.DecodeConfig[Config](raw)
	if err != nil {
		return cfg, err
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "unix:///var/run/docker.sock"
	}
	if !strings.HasPrefix(cfg.Endpoint, "unix://") && !strings.HasPrefix(cfg.Endpoint, "tcp://") && !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") && !strings.HasPrefix(cfg.Endpoint, "ssh://") {
		return cfg, errors.New("docker endpoint must use unix, tcp, http, https, or ssh")
	}
	return cfg, nil
}

func newClient(cfg Config) (API, error) {
	if strings.HasPrefix(cfg.Endpoint, "ssh://") {
		helper, err := connhelper.GetConnectionHelper(cfg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("Docker SSH helper: %w", err)
		}
		transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return helper.Dialer(ctx, network, address)
		}}
		return client.NewClientWithOpts(client.WithHost(helper.Host), client.WithHTTPClient(&http.Client{Transport: transport}), client.WithAPIVersionNegotiation())
	}
	opts := []client.Opt{client.WithHost(cfg.Endpoint), client.WithAPIVersionNegotiation()}
	if cfg.TLSVerify || cfg.CACert != "" || cfg.ClientCert != "" {
		if cfg.TLSVerify && cfg.CACert == "" {
			return nil, errors.New("CA certificate is required when Docker TLS verification is enabled")
		}
		if (cfg.ClientCert == "") != (cfg.ClientKey == "") {
			return nil, errors.New("Docker client certificate and key must be provided together")
		}
		opts = append(opts, client.WithTLSClientConfig(cfg.CACert, cfg.ClientCert, cfg.ClientKey))
	}
	return client.NewClientWithOpts(opts...)
}

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	cfg, err := decode(raw)
	if err != nil {
		return err
	}
	cli, err := c.newClient(cfg)
	if err != nil {
		return err
	}
	defer cli.Close()
	_, err = cli.ServerVersion(ctx)
	return err
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	cfg, err := decode(raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	cli, err := c.newClient(cfg)
	if err != nil {
		return domain.Snapshot{}, err
	}
	defer cli.Close()
	info, err := cli.Info(ctx)
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("docker info: %w", err)
	}
	if _, err = cli.ServerVersion(ctx); err != nil {
		return domain.Snapshot{}, fmt.Errorf("docker version: %w", err)
	}
	items, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("list containers: %w", err)
	}
	hostName := cfg.HostName
	if hostName == "" {
		hostName = info.Name
	}
	if hostName == "" {
		hostName = "docker"
	}
	hostKey := "docker:" + hostName
	address := cfg.HostAddress
	if address == "" {
		if u, e := url.Parse(cfg.Endpoint); e == nil {
			address = u.Hostname()
		}
	}
	snap := domain.Snapshot{Hosts: []domain.Host{{Key: hostKey, Name: hostName, Kind: "docker", Address: address, OS: info.OperatingSystem, Arch: info.Architecture}}}
	type result struct {
		inspect types.ContainerJSON
		err     error
	}
	results := make([]result, len(items))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			results[i].inspect, results[i].err = cli.ContainerInspect(ctx, items[i].ID)
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.err != nil {
			return domain.Snapshot{}, fmt.Errorf("inspect container %s: %w", items[i].ID, r.err)
		}
		svc, ports := mapContainer(hostKey, items[i], r.inspect)
		snap.Services = append(snap.Services, svc)
		snap.Ports = append(snap.Ports, ports...)
	}
	sort.Slice(snap.Services, func(i, j int) bool { return snap.Services[i].Key < snap.Services[j].Key })
	return snap, nil
}

func mapContainer(hostKey string, summary types.Container, in types.ContainerJSON) (domain.Service, []domain.Port) {
	labels := summary.Labels
	imageRef := summary.Image
	if in.Config != nil {
		if in.Config.Labels != nil {
			labels = in.Config.Labels
		}
		imageRef = in.Config.Image
	}
	name := strings.TrimPrefix(in.Name, "/")
	if v := labels["com.docker.compose.service"]; v != "" {
		name = v
	}
	if name == "" && len(summary.Names) > 0 {
		name = strings.TrimPrefix(summary.Names[0], "/")
	}
	image, tag := splitImage(imageRef)
	digest := summary.ImageID
	state := "unknown"
	health := ""
	if in.State != nil {
		state = in.State.Status
		if in.State.Health != nil {
			health = in.State.Health.Status
		}
	}
	restart := ""
	if in.HostConfig != nil {
		restart = string(in.HostConfig.RestartPolicy.Name)
	}
	svc := domain.Service{Key: "container:" + in.ID, HostKey: hostKey, Name: name, Kind: "container", Stack: labels["com.docker.compose.project"], Image: image, Tag: tag, Digest: digest, State: state, Health: health, RestartPolicy: restart, RawLabels: labels}
	if in.NetworkSettings != nil {
		for network, ep := range in.NetworkSettings.Networks {
			if ep != nil {
				aliases := append(append([]string(nil), ep.Aliases...), ep.DNSNames...)
				svc.Networks = append(svc.Networks, domain.ServiceNetwork{Name: network, IP: ep.IPAddress, Aliases: dedupe(aliases)})
			}
		}
	}
	var ports []domain.Port
	seenPorts := map[string]bool{}
	addPort := func(p domain.Port) {
		if !seenPorts[p.NaturalKey()] {
			ports = append(ports, p)
			seenPorts[p.NaturalKey()] = true
		}
	}
	if in.NetworkSettings != nil {
		for p, bindings := range in.NetworkSettings.Ports {
			if len(bindings) == 0 {
				addPort(domain.Port{ServiceKey: svc.Key, HostKey: hostKey, Number: p.Int(), ContainerPort: p.Int(), Protocol: p.Proto(), Source: "docker"})
				continue
			}
			for _, b := range bindings {
				n := p.Int()
				_, _ = fmt.Sscanf(b.HostPort, "%d", &n)
				addPort(domain.Port{ServiceKey: svc.Key, HostKey: hostKey, Number: n, ContainerPort: p.Int(), Protocol: p.Proto(), Published: true, HostIP: b.HostIP, Source: "docker"})
			}
		}
	}
	if in.HostConfig != nil {
		for p, bindings := range in.HostConfig.PortBindings {
			for _, b := range bindings {
				n := p.Int()
				_, _ = fmt.Sscanf(b.HostPort, "%d", &n)
				addPort(domain.Port{ServiceKey: svc.Key, HostKey: hostKey, Number: n, ContainerPort: p.Int(), Protocol: p.Proto(), Published: true, HostIP: b.HostIP, Source: "docker"})
			}
		}
	}
	if in.Config != nil {
		for p := range in.Config.ExposedPorts {
			addPort(domain.Port{ServiceKey: svc.Key, HostKey: hostKey, Number: p.Int(), ContainerPort: p.Int(), Protocol: p.Proto(), Source: "docker"})
		}
	}
	sort.Slice(svc.Networks, func(i, j int) bool { return svc.Networks[i].Name < svc.Networks[j].Name })
	sort.Slice(ports, func(i, j int) bool { return ports[i].NaturalKey() < ports[j].NaturalKey() })
	return svc, ports
}
func dedupe(in []string) []string {
	m := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !m[v] {
			m[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func splitImage(v string) (string, string) {
	at := strings.LastIndex(v, "@")
	if at >= 0 {
		return v[:at], v[at+1:]
	}
	slash := strings.LastIndex(v, "/")
	colon := strings.LastIndex(v, ":")
	if colon > slash {
		return v[:colon], v[colon+1:]
	}
	return v, ""
}
