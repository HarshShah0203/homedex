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
	// ImageUpdates are registry lookups of the image references containers
	// run. Only the registry connector returns them.
	ImageUpdates []ImageUpdate
}

// Host kinds a connector may report besides docker, ssh, manual, vm, lxc and
// proxmox-node. The hosts table CHECK lists every accepted kind.
const (
	// HostKindTailscale marks a tailnet device. It is a second view of a
	// machine another connector may already report, so route resolution links
	// the two instead of treating the device as a machine of its own.
	HostKindTailscale = "tailscale"
	// HostKindDNS marks a DNS view of one address, as a local resolver
	// (Pi-hole, AdGuard Home) answers it: Address is the IP and Aliases are the
	// names that resolve to it. Like a tailnet device it is a view, not a
	// machine; route resolution links it to the one machine reported at that
	// exact address, never by name.
	HostKindDNS = "dns"
	// HostKindUnraid is an Unraid server.
	HostKindUnraid = "unraid"
	// HostKindTrueNAS is a TrueNAS system.
	HostKindTrueNAS = "truenas"
	// HostKindK8sNode is a Kubernetes node.
	HostKindK8sNode = "k8s-node"
)

type Host struct {
	Key     string
	Name    string
	Kind    string
	Address string
	OS      string
	Arch    string
	Notes   string
	// Aliases are other identifiers of this host that a route upstream may use
	// (a second IP, DNS names); route resolution and search match them.
	Aliases []string
	// ReportedLastSeen is the source's own last-contact time. It is stored but
	// excluded from change diffs, because it moves on every poll.
	ReportedLastSeen *time.Time
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
	// RepoDigests are the registry digests Docker recorded for the image the
	// container runs ("nginx@sha256:..."): for a multi-arch image, the index
	// digest its tag resolved to when pulled. Empty for a locally built image.
	// They are stored for update checks but excluded from change diffs.
	RepoDigests []string
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

// ImageUpdate is one anonymous registry lookup of an image reference, keyed
// by the reference exactly as containers name it ("traefik/whoami:v1.10.0").
type ImageUpdate struct {
	Ref string
	// Lookup is "resolved" (RemoteDigest is what the tag points at now),
	// "pinned" (the reference names a digest, nothing to look up) or "unknown".
	Lookup       string
	RemoteDigest string
	// Reason explains an unknown lookup in words safe to show the operator.
	Reason string
	// Transient marks an unknown lookup that may succeed later (rate limit,
	// registry outage, timeout); the engine keeps the previous answer.
	Transient bool
	CheckedAt time.Time
}

func (u ImageUpdate) NaturalKey() string { return u.Ref }
