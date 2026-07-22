package sshexec

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/HarshShah0203/homedex/internal/connectors"
)

type fakeRunner struct{ outputs map[string]string }

func (f *fakeRunner) close() error { return nil }
func (f *fakeRunner) run(_ context.Context, cmd string) (string, error) {
	for prefix, out := range f.outputs {
		if strings.HasPrefix(cmd, prefix) {
			if out == "ERR" {
				return "", errors.New("command not found")
			}
			return out, nil
		}
	}
	return "", errors.New("unexpected command: " + cmd)
}

// A syntactically valid PEM key so decode() passes; the fake runner means it is
// never used for a real handshake.
const testKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW\nQyNTUxOQAAACDaz2KTNv8kK6UEwUwWvGXjWCVYbrJ2985DEsjEsE35RwAAAJgxnbfWMZ23\n1gAAAAtzc2gtZWQyNTUxOQAAACDaz2KTNv8kK6UEwUwWvGXjWCVYbrJ2985DEsjEsE35Rw\nAAAEB4NZFa5wtlyzuvBOPzE1CIRhkl0h5vTFYXpiJThJHhy9rPYpM2/yQrpQTBTBa8ZeNY\nJVhusnb3zkMSyMSwTflHAAAAEnRlc3RAaG9tZWRleC5sb2NhbAECAw==\n-----END OPENSSH PRIVATE KEY-----\n"

func config(t *testing.T) connectors.Config {
	t.Helper()
	raw := map[string]any{"host": "nas.lab:22", "user": "inventory", "private_key": testKey, "host_key_sha256": "SHA256:abc"}
	encoded, _ := json.Marshal(raw)
	var cfg connectors.Config
	if err := json.Unmarshal(encoded, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestScanCollectsContainersProcessesAndPorts(t *testing.T) {
	fake := &fakeRunner{outputs: map[string]string{
		"uname":    "Linux 6.8.0-45-generic x86_64\n",
		"hostname": "nas\n",
		"docker ps": "jellyfin\tlscr.io/linuxserver/jellyfin:10.10.7\trunning\t0.0.0.0:8096->8096/tcp, :::8096->8096/tcp, 1900/udp\tmedia\n" +
			"postgres\tpostgres:16\trunning\t5432/tcp\t\n",
		"ss -H": `tcp   LISTEN 0      128        0.0.0.0:22        0.0.0.0:*    users:(("sshd",pid=812,fd=3))
tcp   LISTEN 0      4096       0.0.0.0:8096      0.0.0.0:*    users:(("docker-proxy",pid=990,fd=4))
tcp   LISTEN 0      4096     127.0.0.1:5433      0.0.0.0:*    users:(("pgbouncer",pid=1200,fd=6))
udp   UNCONN 0      0          0.0.0.0:5353      0.0.0.0:*    users:(("avahi-daemon",pid=700,fd=12))
`,
	}}
	c := &Connector{dial: func(context.Context, Config) (runner, error) { return fake, nil }}
	snap, err := c.Scan(context.Background(), config(t))
	if err != nil {
		t.Fatal(err)
	}

	if len(snap.Hosts) != 1 || snap.Hosts[0].Key != "ssh:nas.lab" || snap.Hosts[0].Name != "nas" ||
		snap.Hosts[0].OS != "Linux 6.8.0-45-generic" || snap.Hosts[0].Arch != "x86_64" || snap.Hosts[0].Kind != "ssh" {
		t.Fatalf("host: %#v", snap.Hosts)
	}

	byKey := map[string]string{}
	for _, s := range snap.Services {
		byKey[s.Key] = s.Kind
	}
	for key, kind := range map[string]string{
		"ssh:nas.lab:jellyfin":          "container",
		"ssh:nas.lab:postgres":          "container",
		"ssh:nas.lab:proc:sshd":         "process",
		"ssh:nas.lab:proc:pgbouncer":    "process",
		"ssh:nas.lab:proc:avahi-daemon": "process",
	} {
		if byKey[key] != kind {
			t.Fatalf("service %s: want kind %q, got %q (all: %v)", key, kind, byKey[key], byKey)
		}
	}
	// docker-proxy's 8096 listener must not appear as a separate process.
	if _, ok := byKey["ssh:nas.lab:proc:docker-proxy"]; ok {
		t.Fatal("docker-proxy leaked into services")
	}
	for _, s := range snap.Services {
		if s.Key == "ssh:nas.lab:jellyfin" {
			if s.Image != "lscr.io/linuxserver/jellyfin" || s.Tag != "10.10.7" || s.Stack != "media" {
				t.Fatalf("jellyfin parse: %#v", s)
			}
		}
	}

	type portFact struct {
		published bool
		container int
	}
	got := map[string]portFact{}
	for _, p := range snap.Ports {
		got[p.ServiceKey+":"+strconv.Itoa(p.Number)+"/"+p.Protocol] = portFact{p.Published, p.ContainerPort}
	}
	want := map[string]portFact{
		"ssh:nas.lab:jellyfin:8096/tcp":          {true, 8096},  // IPv4+IPv6 deduped to one
		"ssh:nas.lab:jellyfin:1900/udp":          {false, 1900}, // internal-only exposure
		"ssh:nas.lab:postgres:5432/tcp":          {false, 5432},
		"ssh:nas.lab:proc:sshd:22/tcp":           {true, 22},
		"ssh:nas.lab:proc:pgbouncer:5433/tcp":    {false, 5433}, // loopback bind = not published
		"ssh:nas.lab:proc:avahi-daemon:5353/udp": {true, 5353},
	}
	if len(got) != len(want) {
		t.Fatalf("ports: want %d, got %d: %v", len(want), len(got), got)
	}
	for key, fact := range want {
		if got[key] != fact {
			t.Fatalf("port %s: want %+v got %+v", key, fact, got[key])
		}
	}
}

func TestScanOnBareHostWithoutDocker(t *testing.T) {
	fake := &fakeRunner{outputs: map[string]string{
		"uname":     "Linux 6.1.0 aarch64\n",
		"hostname":  "pi\n",
		"docker ps": "ERR",
		"ss -H":     `tcp   LISTEN 0 128 *:80 *:* users:(("nginx",pid=3,fd=6))` + "\n",
	}}
	c := &Connector{dial: func(context.Context, Config) (runner, error) { return fake, nil }}
	snap, err := c.Scan(context.Background(), config(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Services) != 1 || snap.Services[0].Key != "ssh:nas.lab:proc:nginx" || snap.Services[0].Kind != "process" {
		t.Fatalf("services: %#v", snap.Services)
	}
	if len(snap.Ports) != 1 || snap.Ports[0].Number != 80 || !snap.Ports[0].Published || snap.Ports[0].HostIP != "0.0.0.0" {
		t.Fatalf("ports: %#v", snap.Ports)
	}
}

func TestDecodeRejectsIncompleteConfig(t *testing.T) {
	for name, raw := range map[string]map[string]any{
		"missing host": {"user": "u", "private_key": testKey},
		"missing user": {"host": "h", "private_key": testKey},
		"missing key":  {"host": "h", "user": "u"},
	} {
		encoded, _ := json.Marshal(raw)
		var cfg connectors.Config
		_ = json.Unmarshal(encoded, &cfg)
		if _, err := decode(cfg); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestFingerprintNormalization(t *testing.T) {
	if normalizeFingerprint(" abc ") != "SHA256:abc" || normalizeFingerprint("SHA256:abc") != "SHA256:abc" || normalizeFingerprint("") != "" {
		t.Fatal("fingerprint normalization broken")
	}
}
