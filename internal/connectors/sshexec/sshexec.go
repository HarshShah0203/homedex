// Package sshexec is the agentless SSH collector: it connects to a host with a
// read-only account and records what it observes — container facts via the
// docker CLI when present, listening-port facts via ss either way. It covers
// hosts where exposing the Docker API is not an option, and bare hosts that
// run no Docker at all.
package sshexec

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	Host          string `json:"host"` // host or host:port, default port 22
	User          string `json:"user"`
	PrivateKey    string `json:"private_key"`     // PEM-encoded key
	Passphrase    string `json:"passphrase"`      // optional key passphrase
	HostKeySHA256 string `json:"host_key_sha256"` // pinned server key fingerprint
	HostName      string `json:"host_name"`       // optional display override
}

// runner abstracts the SSH session so parsers are unit-testable without a server.
type runner interface {
	run(ctx context.Context, cmd string) (string, error)
	close() error
}

type Connector struct {
	dial func(ctx context.Context, cfg Config) (runner, error)
}

func New() *Connector             { return &Connector{dial: dialSSH} }
func (c *Connector) Kind() string { return "ssh" }

const commandTimeout = 10 * time.Second

var errUnpinned = errors.New("host key is not pinned")

func decode(raw connectors.Config) (Config, error) {
	cfg, err := connectors.DecodeConfig[Config](raw)
	if err != nil {
		return cfg, err
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.HostKeySHA256 = normalizeFingerprint(cfg.HostKeySHA256)
	if cfg.Host == "" {
		return cfg, errors.New("an SSH host is required")
	}
	if cfg.User == "" {
		return cfg, errors.New("an SSH user is required")
	}
	if strings.TrimSpace(cfg.PrivateKey) == "" {
		return cfg, errors.New("a private key is required; password auth is deliberately unsupported")
	}
	return cfg, nil
}

func normalizeFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "SHA256:") {
		value = "SHA256:" + value
	}
	return value
}

func hostAddr(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "22")
}

func hostID(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func dialSSH(ctx context.Context, cfg Config) (runner, error) {
	var signer ssh.Signer
	var err error
	if cfg.Passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cfg.PrivateKey), []byte(cfg.Passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
	}
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	observed := ""
	clientCfg := &ssh.ClientConfig{
		User:    cfg.User,
		Auth:    []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout: commandTimeout,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			observed = ssh.FingerprintSHA256(key)
			if cfg.HostKeySHA256 == "" {
				return errUnpinned
			}
			if subtle.ConstantTimeCompare([]byte(observed), []byte(cfg.HostKeySHA256)) != 1 {
				return fmt.Errorf("host key mismatch: the server presented %s", observed)
			}
			return nil
		},
	}
	type dialResult struct {
		client *ssh.Client
		err    error
	}
	results := make(chan dialResult, 1)
	go func() {
		client, dialErr := ssh.Dial("tcp", hostAddr(cfg.Host), clientCfg)
		results <- dialResult{client, dialErr}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-results:
		if result.err != nil {
			if observed != "" && cfg.HostKeySHA256 == "" {
				return nil, fmt.Errorf("host key is not pinned. The server presented %s — paste that value into the host key fingerprint field to pin it", observed)
			}
			return nil, fmt.Errorf("SSH connect to %s: %w", hostAddr(cfg.Host), result.err)
		}
		return &sshRunner{client: result.client}, nil
	}
}

type sshRunner struct{ client *ssh.Client }

func (r *sshRunner) close() error { return r.client.Close() }

func (r *sshRunner) run(ctx context.Context, cmd string) (string, error) {
	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()
	type result struct {
		out []byte
		err error
	}
	results := make(chan result, 1)
	go func() {
		out, runErr := session.Output(cmd)
		results <- result{out, runErr}
	}()
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("%q timed out after %s", cmd, commandTimeout)
	case v := <-results:
		if v.err != nil {
			return "", fmt.Errorf("%q: %w", cmd, v.err)
		}
		return string(v.out), nil
	}
}

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	cfg, err := decode(raw)
	if err != nil {
		return err
	}
	conn, err := c.dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.close()
	if _, err = conn.run(ctx, "uname -s -r -m"); err != nil {
		return fmt.Errorf("connected, but command execution failed: %w", err)
	}
	return nil
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	cfg, err := decode(raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	conn, err := c.dial(ctx, cfg)
	if err != nil {
		return domain.Snapshot{}, err
	}
	defer conn.close()

	id := hostID(cfg.Host)
	hostKey := "ssh:" + id
	host := domain.Host{Key: hostKey, Name: cfg.HostName, Kind: "ssh", Address: id}

	if out, unameErr := conn.run(ctx, "uname -s -r -m"); unameErr == nil {
		fields := strings.Fields(out)
		if len(fields) >= 2 {
			host.OS = fields[0] + " " + fields[1]
		}
		if len(fields) >= 3 {
			host.Arch = fields[2]
		}
	}
	if host.Name == "" {
		if out, hostErr := conn.run(ctx, "hostname"); hostErr == nil && strings.TrimSpace(out) != "" {
			host.Name = strings.TrimSpace(out)
		} else {
			host.Name = id
		}
	}

	snap := domain.Snapshot{Hosts: []domain.Host{host}}

	// Container facts via the docker CLI, when the account can run it.
	dockerPublished := map[string]bool{}
	dockerOut, dockerErr := conn.run(ctx, `docker ps --no-trunc --format '{{.Names}}{{"\t"}}{{.Image}}{{"\t"}}{{.State}}{{"\t"}}{{.Ports}}{{"\t"}}{{.Label "com.docker.compose.project"}}'`)
	if dockerErr == nil {
		services, ports := parseDockerPS(dockerOut, hostKey)
		snap.Services = append(snap.Services, services...)
		snap.Ports = append(snap.Ports, ports...)
		for _, p := range ports {
			if p.Published {
				dockerPublished[strconv.Itoa(p.Number)+"/"+p.Protocol] = true
			}
		}
	}

	// Listening-port facts via ss — the reason this connector exists for
	// bare hosts. Entries already explained by Docker are skipped.
	if ssOut, ssErr := conn.run(ctx, "ss -H -tulnp 2>/dev/null || ss -H -tuln"); ssErr == nil {
		services, ports := parseSS(ssOut, hostKey, dockerPublished)
		snap.Services = append(snap.Services, services...)
		snap.Ports = append(snap.Ports, ports...)
	}

	return snap, nil
}

func splitImage(image string) (string, string) {
	slash := strings.LastIndex(image, "/")
	if colon := strings.LastIndex(image, ":"); colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, ""
}

func parseDockerPS(out, hostKey string) ([]domain.Service, []domain.Port) {
	var services []domain.Service
	var ports []domain.Port
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		name := strings.TrimSpace(fields[0])
		image, tag := splitImage(strings.TrimSpace(fields[1]))
		state := strings.TrimSpace(fields[2])
		serviceKey := hostKey + ":" + name
		stack := ""
		if len(fields) >= 5 {
			stack = strings.TrimSpace(fields[4])
		}
		services = append(services, domain.Service{
			Key: serviceKey, HostKey: hostKey, Name: name, Kind: "container",
			Stack: stack, Image: image, Tag: tag, State: state,
		})
		if len(fields) >= 4 {
			ports = append(ports, parseDockerPorts(strings.TrimSpace(fields[3]), serviceKey, hostKey)...)
		}
	}
	return services, ports
}

var portMapRE = regexp.MustCompile(`^(?:(.*):)?(\d+)->(\d+)/(tcp|udp)$`)
var portExposeRE = regexp.MustCompile(`^(\d+)(?:-\d+)?/(tcp|udp)$`)

func parseDockerPorts(spec, serviceKey, hostKey string) []domain.Port {
	var ports []domain.Port
	seen := map[string]bool{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if match := portMapRE.FindStringSubmatch(entry); match != nil {
			hostIP := match[1]
			number, _ := strconv.Atoi(match[2])
			containerPort, _ := strconv.Atoi(match[3])
			key := match[2] + "/" + match[4]
			if seen[key] {
				continue // the same mapping repeats for IPv4 and IPv6 binds
			}
			seen[key] = true
			ports = append(ports, domain.Port{
				ServiceKey: serviceKey, HostKey: hostKey, Number: number, ContainerPort: containerPort,
				Protocol: match[4], Published: true, HostIP: hostIP, Source: "ssh",
			})
			continue
		}
		if match := portExposeRE.FindStringSubmatch(entry); match != nil {
			number, _ := strconv.Atoi(match[1])
			key := match[1] + "/" + match[2] + "/internal"
			if seen[key] {
				continue
			}
			seen[key] = true
			ports = append(ports, domain.Port{
				ServiceKey: serviceKey, HostKey: hostKey, Number: number, ContainerPort: number,
				Protocol: match[2], Published: false, Source: "ssh",
			})
		}
	}
	return ports
}

var ssProcessRE = regexp.MustCompile(`\(\("([^"]+)"`)

func parseSS(out, hostKey string, dockerPublished map[string]bool) ([]domain.Service, []domain.Port) {
	var ports []domain.Port
	processes := map[string]bool{}
	var order []string
	type listener struct {
		process  string
		number   int
		protocol string
		hostIP   string
		public   bool
	}
	var listeners []listener
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		protocol := fields[0]
		if protocol != "tcp" && protocol != "udp" {
			continue
		}
		local := fields[4]
		colon := strings.LastIndex(local, ":")
		if colon < 0 {
			continue
		}
		number, err := strconv.Atoi(local[colon+1:])
		if err != nil {
			continue
		}
		addr := strings.Trim(local[:colon], "[]")
		process := "listener"
		if match := ssProcessRE.FindStringSubmatch(line); match != nil {
			process = match[1]
		}
		if process == "docker-proxy" || dockerPublished[strconv.Itoa(number)+"/"+protocol] {
			continue // already explained by the Docker facts
		}
		key := strconv.Itoa(number) + "/" + protocol + "/" + addr
		if seen[key] {
			continue
		}
		seen[key] = true
		public := addr != "127.0.0.1" && addr != "::1"
		hostIP := addr
		if addr == "*" || addr == "::" {
			hostIP = "0.0.0.0"
		}
		listeners = append(listeners, listener{process, number, protocol, hostIP, public})
	}
	for _, l := range listeners {
		if !processes[l.process] {
			processes[l.process] = true
			order = append(order, l.process)
		}
		serviceKey := hostKey + ":proc:" + l.process
		ports = append(ports, domain.Port{
			ServiceKey: serviceKey, HostKey: hostKey, Number: l.number, ContainerPort: l.number,
			Protocol: l.protocol, Published: l.public, HostIP: l.hostIP, Source: "ssh",
		})
	}
	var services []domain.Service
	for _, name := range order {
		services = append(services, domain.Service{
			Key: hostKey + ":proc:" + name, HostKey: hostKey, Name: name, Kind: "process", State: "running",
		})
	}
	return services, ports
}
