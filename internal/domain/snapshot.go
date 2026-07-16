package domain

import "time"

// Snapshot is the complete observed state returned by one connector scan.
// Connectors do not persist data; the engine owns reconciliation.
type Snapshot struct {
	Hosts    []Host
	Services []Service
	Ports    []Port
	Routes   []Route
	Certs    []Cert
	Domains  []Domain
}

type Entity interface {
	NaturalKey() string
}

type Host struct {
	Key     string
	Name    string
	Kind    string
	Address string
	OS      string
	Arch    string
	Notes   string
}

func (h Host) NaturalKey() string { return h.Key }

type Service struct {
	Key           string
	HostKey       string
	Name          string
	Kind          string
	Stack         string
	Image         string
	Tag           string
	Digest        string
	State         string
	RestartPolicy string
	RawLabels     map[string]string
}

func (s Service) NaturalKey() string { return s.Key }

type Port struct {
	ServiceKey    string
	HostKey       string
	Number        int
	Protocol      string
	Published     bool
	HostIP        string
	ContainerPort int
	Source        string
}

func (p Port) NaturalKey() string {
	return p.ServiceKey + ":" + p.HostIP + ":" + p.Protocol + ":" + itoa(p.Number) + ":" + itoa(p.ContainerPort)
}

type Route struct {
	Key                string
	ProxyID            *int64
	Domain             string
	PathPrefix         string
	UpstreamHost       string
	UpstreamPort       int
	ResolvedServiceKey string
	ResolveConfidence  string
	TLS                bool
	Status             string
}

func (r Route) NaturalKey() string { return r.Key }

type Cert struct {
	Key        string
	Subject    string
	SANs       []string
	Issuer     string
	NotAfter   time.Time
	ChainValid bool
	Source     string
	Endpoint   string
}

func (c Cert) NaturalKey() string { return c.Key }

type Domain struct {
	Key         string
	Name        string
	Registrar   string
	ExpiresAt   *time.Time
	Nameservers []string
	Source      string
	LastChecked *time.Time
}

func (d Domain) NaturalKey() string { return d.Key }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := make([]byte, 0, 12)
	for n > 0 {
		b = append(b, byte('0'+n%10))
		n /= 10
	}
	if neg {
		b = append(b, '-')
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
