package export

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HarshShah0203/homedex/internal/store"
)

const (
	SchemaVersion = "homedex.inventory.v1"
	ContextLimit  = 100 * 1024
	redacted      = "[REDACTED]"
)

type Options struct {
	MaskDomains     bool
	MaskExternalIPs bool
	IncludePrivate  bool
}

type FilterOptions struct {
	Query     string
	State     string
	Status    string
	HostID    int64
	Published *bool
}

type Archive struct {
	SchemaVersion string    `json:"schema_version"`
	Hosts         []Host    `json:"hosts"`
	Services      []Service `json:"services"`
	Ports         []Port    `json:"ports"`
	Routes        []Route   `json:"routes"`
	Expiry        []Expiry  `json:"expiry"`
}

type Metadata struct {
	Notes        string                   `json:"notes,omitempty"`
	Tags         []store.TagInput         `json:"tags,omitempty"`
	CustomFields []store.CustomFieldInput `json:"custom_fields,omitempty"`
}

type Host struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Address   string `json:"address,omitempty"`
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
	State     string `json:"state"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Metadata
}

type Service struct {
	ID            int64             `json:"id"`
	HostID        *int64            `json:"host_id,omitempty"`
	Host          string            `json:"host,omitempty"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Stack         string            `json:"stack,omitempty"`
	Image         string            `json:"image,omitempty"`
	Tag           string            `json:"tag,omitempty"`
	Digest        string            `json:"digest,omitempty"`
	State         string            `json:"state"`
	Health        string            `json:"health,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	FirstSeen     string            `json:"first_seen"`
	LastSeen      string            `json:"last_seen"`
	Metadata
}

type Port struct {
	ID            int64  `json:"id"`
	ServiceID     int64  `json:"service_id"`
	Service       string `json:"service"`
	HostID        *int64 `json:"host_id,omitempty"`
	Host          string `json:"host,omitempty"`
	Number        int    `json:"number"`
	Protocol      string `json:"protocol"`
	Published     bool   `json:"published"`
	HostIP        string `json:"host_ip,omitempty"`
	ContainerPort int    `json:"container_port"`
	Source        string `json:"source,omitempty"`
}

type Route struct {
	ID                int64  `json:"id"`
	Domain            string `json:"domain"`
	PathPrefix        string `json:"path_prefix,omitempty"`
	Proxy             string `json:"proxy,omitempty"`
	UpstreamHost      string `json:"upstream_host,omitempty"`
	UpstreamPort      int    `json:"upstream_port,omitempty"`
	ResolvedServiceID *int64 `json:"resolved_service_id,omitempty"`
	Service           string `json:"service,omitempty"`
	ResolveConfidence string `json:"resolve_confidence"`
	TLS               bool   `json:"tls"`
	Status            string `json:"status"`
	State             string `json:"state"`
	Metadata
}

type Expiry struct {
	EntityType string `json:"entity_type"`
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Authority  string `json:"authority,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	CheckedAt  string `json:"checked_at,omitempty"`
	Source     string `json:"source,omitempty"`
	State      string `json:"state"`
	Metadata
}

type Truncation struct {
	LimitBytes int            `json:"limit_bytes"`
	Bytes      int            `json:"bytes"`
	Omitted    map[string]int `json:"omitted"`
}

type Loader struct{ db *sql.DB }

func NewLoader(db *sql.DB) *Loader { return &Loader{db: db} }

func (l *Loader) Load(ctx context.Context, options Options) (Archive, error) {
	archive := Archive{SchemaVersion: SchemaVersion, Hosts: []Host{}, Services: []Service{}, Ports: []Port{}, Routes: []Route{}, Expiry: []Expiry{}}
	notes, tags, fields, err := l.metadata(ctx)
	if err != nil {
		return archive, err
	}
	rows, err := l.db.QueryContext(ctx, `SELECT id,name,kind,address,os,arch,notes,state,first_seen,last_seen FROM hosts ORDER BY LOWER(name),id`)
	if err != nil {
		return archive, err
	}
	for rows.Next() {
		var item Host
		var baseNotes string
		if err = rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Address, &item.OS, &item.Arch, &baseNotes, &item.State, &item.FirstSeen, &item.LastSeen); err != nil {
			rows.Close()
			return archive, err
		}
		item.Metadata = metadataFor("host", item.ID, baseNotes, notes, tags, fields)
		archive.Hosts = append(archive.Hosts, item)
	}
	if err = rows.Close(); err != nil {
		return archive, err
	}
	rows, err = l.db.QueryContext(ctx, `SELECT s.id,s.host_id,COALESCE(h.name,''),s.name,s.kind,s.stack,s.image,s.tag,s.digest,s.state,s.health,s.restart_policy,s.raw_labels,s.notes,s.first_seen,s.last_seen FROM services s LEFT JOIN hosts h ON h.id=s.host_id ORDER BY LOWER(COALESCE(h.name,'')),LOWER(s.name),s.id`)
	if err != nil {
		return archive, err
	}
	for rows.Next() {
		var item Service
		var hostID sql.NullInt64
		var labels, baseNotes string
		if err = rows.Scan(&item.ID, &hostID, &item.Host, &item.Name, &item.Kind, &item.Stack, &item.Image, &item.Tag, &item.Digest, &item.State, &item.Health, &item.RestartPolicy, &labels, &baseNotes, &item.FirstSeen, &item.LastSeen); err != nil {
			rows.Close()
			return archive, err
		}
		if hostID.Valid {
			item.HostID = &hostID.Int64
		}
		_ = json.Unmarshal([]byte(labels), &item.Labels)
		if item.Labels == nil {
			item.Labels = map[string]string{}
		}
		item.Metadata = metadataFor("service", item.ID, baseNotes, notes, tags, fields)
		archive.Services = append(archive.Services, item)
	}
	if err = rows.Close(); err != nil {
		return archive, err
	}
	rows, err = l.db.QueryContext(ctx, `SELECT p.id,p.service_id,s.name,p.host_id,COALESCE(h.name,''),p.number,p.protocol,p.published,p.host_ip,p.container_port,p.source FROM ports p JOIN services s ON s.id=p.service_id LEFT JOIN hosts h ON h.id=p.host_id ORDER BY LOWER(COALESCE(h.name,'')),p.number,p.protocol,LOWER(s.name),p.id`)
	if err != nil {
		return archive, err
	}
	for rows.Next() {
		var item Port
		var hostID sql.NullInt64
		if err = rows.Scan(&item.ID, &item.ServiceID, &item.Service, &hostID, &item.Host, &item.Number, &item.Protocol, &item.Published, &item.HostIP, &item.ContainerPort, &item.Source); err != nil {
			rows.Close()
			return archive, err
		}
		if hostID.Valid {
			item.HostID = &hostID.Int64
		}
		archive.Ports = append(archive.Ports, item)
	}
	if err = rows.Close(); err != nil {
		return archive, err
	}
	rows, err = l.db.QueryContext(ctx, `SELECT r.id,r.domain,r.path_prefix,COALESCE(p.kind,''),r.upstream_host,COALESCE(r.upstream_port,0),r.resolved_service_id,COALESCE(s.name,''),r.resolve_confidence,r.tls,r.status,r.state FROM routes r LEFT JOIN proxies p ON p.id=r.proxy_id LEFT JOIN services s ON s.id=r.resolved_service_id ORDER BY LOWER(r.domain),r.path_prefix,r.id`)
	if err != nil {
		return archive, err
	}
	for rows.Next() {
		var item Route
		var serviceID sql.NullInt64
		if err = rows.Scan(&item.ID, &item.Domain, &item.PathPrefix, &item.Proxy, &item.UpstreamHost, &item.UpstreamPort, &serviceID, &item.Service, &item.ResolveConfidence, &item.TLS, &item.Status, &item.State); err != nil {
			rows.Close()
			return archive, err
		}
		if serviceID.Valid {
			item.ResolvedServiceID = &serviceID.Int64
		}
		item.Metadata = metadataFor("route", item.ID, "", notes, tags, fields)
		archive.Routes = append(archive.Routes, item)
	}
	if err = rows.Close(); err != nil {
		return archive, err
	}
	if err = l.loadExpiry(ctx, &archive, notes, tags, fields); err != nil {
		return archive, err
	}
	return Sanitize(archive, options), nil
}

func (l *Loader) loadExpiry(ctx context.Context, archive *Archive, notes map[string]string, tags map[string][]store.TagInput, fields map[string][]store.CustomFieldInput) error {
	rows, err := l.db.QueryContext(ctx, `SELECT id,subject,issuer,COALESCE(not_after,''),COALESCE(last_seen,''),source,state FROM certs ORDER BY COALESCE(not_after,'9999'),LOWER(subject),id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item Expiry
		item.EntityType, item.Kind = "cert", "certificate"
		if err = rows.Scan(&item.ID, &item.Name, &item.Authority, &item.ExpiresAt, &item.CheckedAt, &item.Source, &item.State); err != nil {
			rows.Close()
			return err
		}
		item.Metadata = metadataFor("cert", item.ID, "", notes, tags, fields)
		archive.Expiry = append(archive.Expiry, item)
	}
	rows.Close()
	rows, err = l.db.QueryContext(ctx, `SELECT id,name,registrar,COALESCE(expires_at,''),COALESCE(last_checked,''),source,state FROM domains ORDER BY COALESCE(expires_at,'9999'),LOWER(name),id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item Expiry
		item.EntityType, item.Kind = "domain", "domain"
		if err = rows.Scan(&item.ID, &item.Name, &item.Authority, &item.ExpiresAt, &item.CheckedAt, &item.Source, &item.State); err != nil {
			rows.Close()
			return err
		}
		item.Metadata = metadataFor("domain", item.ID, "", notes, tags, fields)
		archive.Expiry = append(archive.Expiry, item)
	}
	rows.Close()
	rows, err = l.db.QueryContext(ctx, `SELECT id,name,kind,authority,COALESCE(expires_at,''),source,state FROM manual_expiries ORDER BY COALESCE(expires_at,'9999'),LOWER(name),id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item Expiry
		item.EntityType = "expiry"
		if err = rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Authority, &item.ExpiresAt, &item.Source, &item.State); err != nil {
			rows.Close()
			return err
		}
		item.Metadata = metadataFor("expiry", item.ID, "", notes, tags, fields)
		archive.Expiry = append(archive.Expiry, item)
	}
	rows.Close()
	sort.SliceStable(archive.Expiry, func(i, j int) bool {
		a, b := archive.Expiry[i], archive.Expiry[j]
		if a.ExpiresAt != b.ExpiresAt {
			if a.ExpiresAt == "" {
				return false
			}
			if b.ExpiresAt == "" {
				return true
			}
			return a.ExpiresAt < b.ExpiresAt
		}
		if strings.ToLower(a.Name) != strings.ToLower(b.Name) {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if a.EntityType != b.EntityType {
			return a.EntityType < b.EntityType
		}
		return a.ID < b.ID
	})
	return nil
}

func (l *Loader) metadata(ctx context.Context) (map[string]string, map[string][]store.TagInput, map[string][]store.CustomFieldInput, error) {
	notes := map[string]string{}
	tags := map[string][]store.TagInput{}
	fields := map[string][]store.CustomFieldInput{}
	rows, err := l.db.QueryContext(ctx, `SELECT entity_type,entity_id,notes FROM entity_notes ORDER BY entity_type,entity_id`)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var typ, note string
		var id int64
		if err = rows.Scan(&typ, &id, &note); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		notes[metadataKey(typ, id)] = note
	}
	rows.Close()
	rows, err = l.db.QueryContext(ctx, `SELECT et.entity_type,et.entity_id,t.name,t.color FROM entity_tags et JOIN tags t ON t.id=et.tag_id ORDER BY et.entity_type,et.entity_id,LOWER(t.name),t.id`)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var typ string
		var id int64
		var tag store.TagInput
		if err = rows.Scan(&typ, &id, &tag.Name, &tag.Color); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		key := metadataKey(typ, id)
		tags[key] = append(tags[key], tag)
	}
	rows.Close()
	rows, err = l.db.QueryContext(ctx, `SELECT entity_type,entity_id,key,kind,value FROM custom_fields ORDER BY entity_type,entity_id,LOWER(key),id`)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var typ string
		var id int64
		var field store.CustomFieldInput
		if err = rows.Scan(&typ, &id, &field.Key, &field.Kind, &field.Value); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		key := metadataKey(typ, id)
		fields[key] = append(fields[key], field)
	}
	return notes, tags, fields, rows.Close()
}

func metadataFor(typ string, id int64, base string, notes map[string]string, tags map[string][]store.TagInput, fields map[string][]store.CustomFieldInput) Metadata {
	key := metadataKey(typ, id)
	if note, ok := notes[key]; ok {
		base = note
	}
	return Metadata{Notes: base, Tags: tags[key], CustomFields: fields[key]}
}

func metadataKey(typ string, id int64) string { return typ + ":" + strconv.FormatInt(id, 10) }

var (
	secretLike = regexp.MustCompile(`(?i)(key|token|secret|passw|auth|api)`)
	ipv4Like   = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6Like   = regexp.MustCompile(`(?i)\b[0-9a-f]*:[0-9a-f:]+\b`)
)

func Sanitize(input Archive, options Options) Archive {
	data := clone(input)
	for i := range data.Services {
		if !options.IncludePrivate {
			data.Services[i].Labels = nil
		} else {
			for key, value := range data.Services[i].Labels {
				if secretLike.MatchString(key) || secretLike.MatchString(value) {
					data.Services[i].Labels[key] = redacted
				}
			}
		}
	}
	if !options.IncludePrivate {
		for i := range data.Hosts {
			data.Hosts[i].Metadata = Metadata{}
		}
		for i := range data.Services {
			data.Services[i].Metadata = Metadata{}
		}
		for i := range data.Routes {
			data.Routes[i].Metadata = Metadata{}
		}
		for i := range data.Expiry {
			data.Expiry[i].Metadata = Metadata{}
		}
	}
	domains := []string{}
	if options.MaskDomains {
		seen := map[string]bool{}
		for _, route := range data.Routes {
			if route.Domain != "" {
				seen[route.Domain] = true
			}
		}
		for _, item := range data.Expiry {
			if item.EntityType == "domain" && item.Name != "" {
				seen[item.Name] = true
			}
		}
		for domain := range seen {
			domains = append(domains, domain)
		}
		sort.Slice(domains, func(i, j int) bool {
			if len(domains[i]) != len(domains[j]) {
				return len(domains[i]) > len(domains[j])
			}
			return domains[i] < domains[j]
		})
	}
	mutateStrings(&data, func(value string) string {
		if options.MaskDomains {
			for i, domain := range domains {
				value = strings.ReplaceAll(value, domain, fmt.Sprintf("domain-%03d.invalid", i+1))
			}
		}
		if options.MaskExternalIPs {
			value = maskExternalIPs(value)
		}
		return value
	})
	return data
}

func clone(input Archive) Archive {
	b, _ := json.Marshal(input)
	var output Archive
	_ = json.Unmarshal(b, &output)
	return output
}

func mutateStrings(data *Archive, fn func(string) string) {
	for i := range data.Hosts {
		h := &data.Hosts[i]
		h.Name, h.Address, h.OS, h.Arch, h.Notes = fn(h.Name), fn(h.Address), fn(h.OS), fn(h.Arch), fn(h.Notes)
		mutateFields(h.CustomFields, fn)
	}
	for i := range data.Services {
		s := &data.Services[i]
		s.Host, s.Name, s.Stack, s.Image, s.Tag, s.Digest, s.Notes = fn(s.Host), fn(s.Name), fn(s.Stack), fn(s.Image), fn(s.Tag), fn(s.Digest), fn(s.Notes)
		mutateFields(s.CustomFields, fn)
		for key, value := range s.Labels {
			s.Labels[key] = fn(value)
		}
	}
	for i := range data.Ports {
		p := &data.Ports[i]
		p.Service, p.Host, p.HostIP = fn(p.Service), fn(p.Host), fn(p.HostIP)
	}
	for i := range data.Routes {
		r := &data.Routes[i]
		r.Domain, r.PathPrefix, r.Proxy, r.UpstreamHost, r.Service, r.Notes = fn(r.Domain), fn(r.PathPrefix), fn(r.Proxy), fn(r.UpstreamHost), fn(r.Service), fn(r.Notes)
		mutateFields(r.CustomFields, fn)
	}
	for i := range data.Expiry {
		e := &data.Expiry[i]
		e.Name, e.Authority, e.Notes = fn(e.Name), fn(e.Authority), fn(e.Notes)
		mutateFields(e.CustomFields, fn)
	}
}

func mutateFields(fields []store.CustomFieldInput, fn func(string) string) {
	for i := range fields {
		fields[i].Value = fn(fields[i].Value)
	}
}

func maskExternalIPs(value string) string {
	mask := func(candidate string) string {
		ip := net.ParseIP(candidate)
		if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return candidate
		}
		return "[EXTERNAL-IP]"
	}
	value = ipv4Like.ReplaceAllStringFunc(value, mask)
	return ipv6Like.ReplaceAllStringFunc(value, mask)
}

func JSON(data Archive) ([]byte, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Filter applies the same simple view filters accepted by inventory tables so
// a CSV download can represent the user's current filtered view.
func Filter(data Archive, filter FilterOptions) Archive {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	matches := func(values ...string) bool {
		if query == "" {
			return true
		}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), query) {
				return true
			}
		}
		return false
	}
	services := map[int64]Service{}
	for _, service := range data.Services {
		services[service.ID] = service
	}
	hosts := data.Hosts[:0]
	for _, item := range data.Hosts {
		if filter.HostID > 0 && item.ID != filter.HostID {
			continue
		}
		if filter.State != "" && item.State != filter.State {
			continue
		}
		if matches(item.Name, item.Address, item.OS, item.Arch, item.Notes, tagNames(item.Tags)) {
			hosts = append(hosts, item)
		}
	}
	data.Hosts = hosts
	filteredServices := data.Services[:0]
	for _, item := range data.Services {
		if filter.HostID > 0 && (item.HostID == nil || *item.HostID != filter.HostID) {
			continue
		}
		if filter.State != "" && item.State != filter.State {
			continue
		}
		if matches(item.Host, item.Name, item.Stack, item.Image, item.Tag, item.Notes, tagNames(item.Tags)) {
			filteredServices = append(filteredServices, item)
		}
	}
	data.Services = filteredServices
	ports := data.Ports[:0]
	for _, item := range data.Ports {
		if filter.HostID > 0 && (item.HostID == nil || *item.HostID != filter.HostID) {
			continue
		}
		if filter.Published != nil && item.Published != *filter.Published {
			continue
		}
		if matches(item.Host, item.Service, strconv.Itoa(item.Number), strconv.Itoa(item.ContainerPort), item.Protocol, item.HostIP) {
			ports = append(ports, item)
		}
	}
	data.Ports = ports
	routes := data.Routes[:0]
	for _, item := range data.Routes {
		if filter.HostID > 0 {
			service, ok := services[pointerValue(item.ResolvedServiceID)]
			if !ok || service.HostID == nil || *service.HostID != filter.HostID {
				continue
			}
		}
		if filter.State != "" && item.State != filter.State {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if matches(item.Domain, item.PathPrefix, item.Proxy, item.UpstreamHost, item.Service, item.Notes, tagNames(item.Tags)) {
			routes = append(routes, item)
		}
	}
	data.Routes = routes
	expiry := data.Expiry[:0]
	for _, item := range data.Expiry {
		if filter.State != "" && item.State != filter.State {
			continue
		}
		if matches(item.Name, item.Kind, item.Authority, item.Source, item.Notes, tagNames(item.Tags)) {
			expiry = append(expiry, item)
		}
	}
	data.Expiry = expiry
	return data
}

func Markdown(data Archive) []byte {
	var b strings.Builder
	b.WriteString("# Homedex inventory\n\n")
	b.WriteString("Schema: `" + data.SchemaVersion + "`\n\n")
	for _, host := range data.Hosts {
		fmt.Fprintf(&b, "## Host: %s\n\n", md(host.Name))
		fmt.Fprintf(&b, "- Kind: %s\n- Address: %s\n- State: %s\n", md(host.Kind), md(empty(host.Address)), md(host.State))
		if host.Notes != "" {
			fmt.Fprintf(&b, "- Notes: %s\n", md(host.Notes))
		}
		b.WriteString("\n| Service | Stack | Image | State |\n|---|---|---|---|\n")
		count := 0
		for _, service := range data.Services {
			if service.HostID == nil || *service.HostID != host.ID {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", md(service.Name), md(empty(service.Stack)), md(imageRef(service)), md(service.State))
			count++
		}
		if count == 0 {
			b.WriteString("| _No services_ |  |  |  |\n")
		}
		b.WriteByte('\n')
	}
	if len(data.Hosts) == 0 {
		b.WriteString("## Hosts\n\n_No hosts._\n\n")
	}
	b.WriteString("## Routes\n\n| Domain | Path | Proxy | Upstream | Service | TLS | Status |\n|---|---|---|---|---|---|---|\n")
	for _, route := range data.Routes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %t | %s |\n", md(route.Domain), md(empty(route.PathPrefix)), md(empty(route.Proxy)), md(endpoint(route.UpstreamHost, route.UpstreamPort)), md(empty(route.Service)), route.TLS, md(route.Status))
	}
	if len(data.Routes) == 0 {
		b.WriteString("| _No routes_ |  |  |  |  |  |  |\n")
	}
	b.WriteString("\n## Expiry\n\n| Name | Type | Authority | Expires | State |\n|---|---|---|---|---|\n")
	for _, item := range data.Expiry {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", md(item.Name), md(item.Kind), md(empty(item.Authority)), md(empty(item.ExpiresAt)), md(item.State))
	}
	if len(data.Expiry) == 0 {
		b.WriteString("| _No expiry records_ |  |  |  |  |\n")
	}
	return []byte(b.String())
}

func CSV(data Archive, view string) ([]byte, error) {
	view = strings.ToLower(strings.TrimSpace(view))
	if view == "" {
		view = "services"
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	switch view {
	case "hosts":
		_ = w.Write([]string{"id", "name", "kind", "address", "os", "arch", "state", "first_seen", "last_seen", "notes", "tags"})
		for _, x := range data.Hosts {
			_ = w.Write([]string{i64(x.ID), x.Name, x.Kind, x.Address, x.OS, x.Arch, x.State, x.FirstSeen, x.LastSeen, x.Notes, tagNames(x.Tags)})
		}
	case "services":
		_ = w.Write([]string{"id", "host", "name", "kind", "stack", "image", "tag", "state", "health", "restart_policy", "first_seen", "last_seen", "notes", "tags", "labels"})
		for _, x := range data.Services {
			labels, _ := json.Marshal(x.Labels)
			_ = w.Write([]string{i64(x.ID), x.Host, x.Name, x.Kind, x.Stack, x.Image, x.Tag, x.State, x.Health, x.RestartPolicy, x.FirstSeen, x.LastSeen, x.Notes, tagNames(x.Tags), string(labels)})
		}
	case "ports":
		_ = w.Write([]string{"id", "host", "service", "number", "protocol", "published", "host_ip", "container_port", "source"})
		for _, x := range data.Ports {
			_ = w.Write([]string{i64(x.ID), x.Host, x.Service, strconv.Itoa(x.Number), x.Protocol, strconv.FormatBool(x.Published), x.HostIP, strconv.Itoa(x.ContainerPort), x.Source})
		}
	case "routes":
		_ = w.Write([]string{"id", "domain", "path_prefix", "proxy", "upstream_host", "upstream_port", "service", "confidence", "tls", "status", "state", "notes", "tags"})
		for _, x := range data.Routes {
			_ = w.Write([]string{i64(x.ID), x.Domain, x.PathPrefix, x.Proxy, x.UpstreamHost, strconv.Itoa(x.UpstreamPort), x.Service, x.ResolveConfidence, strconv.FormatBool(x.TLS), x.Status, x.State, x.Notes, tagNames(x.Tags)})
		}
	case "expiry":
		_ = w.Write([]string{"entity_type", "id", "name", "kind", "authority", "expires_at", "checked_at", "source", "state", "notes", "tags"})
		for _, x := range data.Expiry {
			_ = w.Write([]string{x.EntityType, i64(x.ID), x.Name, x.Kind, x.Authority, x.ExpiresAt, x.CheckedAt, x.Source, x.State, x.Notes, tagNames(x.Tags)})
		}
	default:
		return nil, errors.New("CSV view must be hosts, services, ports, routes, or expiry")
	}
	w.Flush()
	return b.Bytes(), w.Error()
}

func Context(data Archive) ([]byte, Truncation) { return ContextWithLimit(data, ContextLimit) }

func ContextWithLimit(data Archive, limit int) ([]byte, Truncation) {
	if limit <= 0 {
		limit = ContextLimit
	}
	type section struct {
		name, header string
		rows         []string
	}
	sections := []section{
		{name: "hosts", header: "## Hosts\n\n| Name | Kind | Address | OS/arch | Notes |\n|---|---|---|---|---|\n"},
		{name: "services", header: "## Services\n\n| Host | Service | Stack | Image | State | Ports | Tags | Labels | Notes |\n|---|---|---|---|---|---|---|---|---|\n"},
		{name: "ports", header: "## Ports\n\n| Host | Service | Published | Container | Protocol | Scope |\n|---|---|---:|---:|---|---|\n"},
		{name: "routes", header: "## Routes\n\n| Domain | Path | Proxy | Upstream | Service | TLS | Status |\n|---|---|---|---|---|---|---|\n"},
		{name: "expiry", header: "## Expiry\n\n| Name | Type | Authority | Expires | State |\n|---|---|---|---|---|\n"},
	}
	portsByService := map[int64][]string{}
	for _, p := range data.Ports {
		portsByService[p.ServiceID] = append(portsByService[p.ServiceID], fmt.Sprintf("%d→%d/%s", p.Number, p.ContainerPort, p.Protocol))
	}
	for _, x := range data.Hosts {
		sections[0].rows = append(sections[0].rows, fmt.Sprintf("| %s | %s | %s | %s | %s |\n", md(x.Name), md(x.Kind), md(empty(x.Address)), md(strings.Trim(strings.TrimSpace(x.OS+" "+x.Arch), " ")), md(x.Notes)))
	}
	for _, x := range data.Services {
		labels, _ := json.Marshal(x.Labels)
		sections[1].rows = append(sections[1].rows, fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", md(empty(x.Host)), md(x.Name), md(empty(x.Stack)), md(imageRef(x)), md(x.State), md(strings.Join(portsByService[x.ID], ", ")), md(tagNames(x.Tags)), md(string(labels)), md(x.Notes)))
	}
	for _, x := range data.Ports {
		scope := "internal"
		if x.Published {
			scope = "published"
		}
		sections[2].rows = append(sections[2].rows, fmt.Sprintf("| %s | %s | %d | %d | %s | %s |\n", md(x.Host), md(x.Service), x.Number, x.ContainerPort, md(x.Protocol), scope))
	}
	for _, x := range data.Routes {
		sections[3].rows = append(sections[3].rows, fmt.Sprintf("| %s | %s | %s | %s | %s | %t | %s |\n", md(x.Domain), md(empty(x.PathPrefix)), md(empty(x.Proxy)), md(endpoint(x.UpstreamHost, x.UpstreamPort)), md(empty(x.Service)), x.TLS, md(x.Status)))
	}
	for _, x := range data.Expiry {
		sections[4].rows = append(sections[4].rows, fmt.Sprintf("| %s | %s | %s | %s | %s |\n", md(x.Name), md(x.Kind), md(empty(x.Authority)), md(empty(x.ExpiresAt)), md(x.State)))
	}
	preamble := "# Homedex lab context\n\nSchema: `" + data.SchemaVersion + "`\n\n"
	reportReserve := 512
	available := limit - len(preamble) - reportReserve
	if available < len(sections)*64 {
		available = limit - len(preamble)
		reportReserve = 0
	}
	perSection := available / len(sections)
	omitted := map[string]int{}
	var b strings.Builder
	b.WriteString(preamble)
	for _, section := range sections {
		used := len(section.header)
		b.WriteString(section.header)
		included := 0
		for _, row := range section.rows {
			if used+len(row) > perSection {
				break
			}
			b.WriteString(row)
			used += len(row)
			included++
		}
		omitted[section.name] = len(section.rows) - included
		b.WriteByte('\n')
	}
	b.WriteString("## Truncation report\n\n")
	for _, section := range sections {
		fmt.Fprintf(&b, "- %s: %d omitted\n", section.name, omitted[section.name])
	}
	result := []byte(b.String())
	if len(result) > limit {
		// Tiny test limits may not fit all section/report headers. Preserve the
		// hard privacy/product budget even in that degenerate case.
		result = result[:limit]
	}
	return result, Truncation{LimitBytes: limit, Bytes: len(result), Omitted: omitted}
}

func md(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}
func empty(value string) string {
	if value == "" {
		return "—"
	}
	return value
}
func i64(value int64) string { return strconv.FormatInt(value, 10) }
func imageRef(x Service) string {
	if x.Tag == "" {
		return empty(x.Image)
	}
	return x.Image + ":" + x.Tag
}
func endpoint(host string, port int) string {
	if host == "" {
		return "—"
	}
	if port == 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
func tagNames(tags []store.TagInput) string {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return strings.Join(names, ",")
}
func pointerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// DaysRemaining is shared by the expiry API and notification evaluator. It
// rounds future partial days up so an item with 24h01m left is reported as two
// calendar-warning days rather than one.
func DaysRemaining(now time.Time, expiresAt string) (*int, error) {
	if expiresAt == "" {
		return nil, nil
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		expiry, err = time.Parse(time.RFC3339, expiresAt)
	}
	if err != nil {
		return nil, err
	}
	hours := expiry.Sub(now).Hours()
	days := int(math.Ceil(hours / 24))
	if hours < 0 {
		days = int(math.Floor(hours / 24))
	}
	return &days, nil
}
