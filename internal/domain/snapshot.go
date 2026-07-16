package domain

import (
	"strconv"
	"time"
)

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
	Health        string
	RestartPolicy string
	RawLabels     map[string]string
	Networks      []ServiceNetwork
}

// ServiceNetwork is addressing metadata used to resolve proxy upstreams.
// Environment variables are deliberately not represented anywhere in a snapshot.
type ServiceNetwork struct {
	Name    string   `json:"name"`
	IP      string   `json:"ip"`
	Aliases []string `json:"aliases"`
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
	return p.ServiceKey + ":" + p.HostIP + ":" + p.Protocol + ":" + strconv.Itoa(p.Number) + ":" + strconv.Itoa(p.ContainerPort)
}

type Route struct {
	Key                        string
	ProxyID                    *int64
	ProxyHostConnectorID       int64
	ProxyHostKey               string
	ProxyNetworks              []string
	Domain                     string
	PathPrefix                 string
	UpstreamHost               string
	UpstreamPort               int
	ResolvedServiceKey         string
	ResolvedServiceConnectorID int64
	ResolveConfidence          string
	TLS                        bool
	Status                     string
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
