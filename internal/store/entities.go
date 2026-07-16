package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type TagInput struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type CustomFieldInput struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type EntityPatch struct {
	Notes        *string            `json:"notes"`
	Tags         []TagInput         `json:"tags"`
	CustomFields []CustomFieldInput `json:"custom_fields"`
}

type ManualEntityInput struct {
	EntityType   string             `json:"entity_type"`
	Name         string             `json:"name"`
	Kind         string             `json:"kind"`
	HostID       *int64             `json:"host_id"`
	Address      string             `json:"address"`
	OS           string             `json:"os"`
	Arch         string             `json:"arch"`
	Stack        string             `json:"stack"`
	Image        string             `json:"image"`
	Tag          string             `json:"tag"`
	State        string             `json:"state"`
	Domain       string             `json:"domain"`
	PathPrefix   string             `json:"path_prefix"`
	UpstreamHost string             `json:"upstream_host"`
	UpstreamPort int                `json:"upstream_port"`
	TLS          bool               `json:"tls"`
	Subject      string             `json:"subject"`
	Endpoint     string             `json:"endpoint"`
	Authority    string             `json:"authority"`
	Registrar    string             `json:"registrar"`
	ExpiresAt    string             `json:"expires_at"`
	Notes        string             `json:"notes"`
	Tags         []TagInput         `json:"tags"`
	CustomFields []CustomFieldInput `json:"custom_fields"`
}

type EntityDetail struct {
	EntityType   string             `json:"entity_type"`
	Entity       map[string]any     `json:"entity"`
	Notes        string             `json:"notes"`
	Tags         []TagInput         `json:"tags"`
	CustomFields []CustomFieldInput `json:"custom_fields"`
	History      []map[string]any   `json:"history"`
}

type EntityManager struct{ store *Store }

func NewEntityManager(s *Store) *EntityManager { return &EntityManager{store: s} }

var entityTables = map[string]string{
	"host": "hosts", "service": "services", "port": "ports", "route": "routes",
	"cert": "certs", "domain": "domains", "expiry": "manual_expiries",
}

func normalizeEntityType(value string) (string, string, error) {
	typ := strings.ToLower(strings.TrimSpace(value))
	typ = strings.TrimSuffix(typ, "s")
	table, ok := entityTables[typ]
	if !ok {
		return "", "", fmt.Errorf("unsupported entity type %q", value)
	}
	return typ, table, nil
}

func (m *EntityManager) Patch(ctx context.Context, entityType string, id int64, patch EntityPatch) error {
	typ, table, err := normalizeEntityType(entityType)
	if err != nil {
		return err
	}
	if id <= 0 {
		return errors.New("entity id must be positive")
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = requireEntity(ctx, tx, table, id); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if patch.Notes != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES(?,?,?,?) ON CONFLICT(entity_type,entity_id) DO UPDATE SET notes=excluded.notes,updated_at=excluded.updated_at`, typ, id, *patch.Notes, now); err != nil {
			return err
		}
	}
	if patch.Tags != nil {
		if err = writeEntityTags(ctx, tx, typ, id, patch.Tags, true); err != nil {
			return err
		}
	}
	if patch.CustomFields != nil {
		if err = writeCustomFields(ctx, tx, typ, id, patch.CustomFields, true); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *EntityManager) CreateManual(ctx context.Context, in ManualEntityInput) (EntityDetail, error) {
	typ, _, err := normalizeEntityType(in.EntityType)
	if err != nil {
		return EntityDetail{}, err
	}
	if typ == "port" {
		return EntityDetail{}, errors.New("manual port entries must be attached to a manual service")
	}
	name := strings.TrimSpace(in.Name)
	if typ == "route" && name == "" {
		name = strings.TrimSpace(in.Domain)
	}
	if typ == "cert" && name == "" {
		name = strings.TrimSpace(in.Subject)
	}
	if name == "" {
		return EntityDetail{}, errors.New("name is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	key, err := manualKey(typ)
	if err != nil {
		return EntityDetail{}, err
	}
	var expiry any
	if in.ExpiresAt != "" {
		t, parseErr := time.Parse(time.RFC3339, in.ExpiresAt)
		if parseErr != nil {
			return EntityDetail{}, errors.New("expires_at must be RFC3339")
		}
		expiry = t.UTC().Format(time.RFC3339Nano)
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		return EntityDetail{}, err
	}
	defer tx.Rollback()
	var result sql.Result
	switch typ {
	case "host":
		result, err = tx.ExecContext(ctx, `INSERT INTO hosts(connector_id,natural_key,name,kind,address,os,arch,state,first_seen,last_seen,created_at,updated_at) VALUES(NULL,?,?, 'manual',?,?,?,'active',?,?,?,?)`, key, name, in.Address, in.OS, in.Arch, now, now, now, now)
	case "service":
		state := defaultValue(in.State, "active")
		kind := defaultValue(in.Kind, "manual")
		if in.HostID != nil {
			if err = requireEntity(ctx, tx, "hosts", *in.HostID); err != nil {
				return EntityDetail{}, errors.New("host not found")
			}
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO services(connector_id,host_id,name,kind,stack,image,tag,state,first_seen,last_seen,natural_key,created_at,updated_at) VALUES(NULL,?,?,?,?,?,?,?,?,?,?,?,?)`, in.HostID, name, kind, in.Stack, in.Image, in.Tag, state, now, now, key, now, now)
	case "route":
		domain := defaultValue(strings.TrimSpace(in.Domain), name)
		if in.UpstreamPort < 0 || in.UpstreamPort > 65535 {
			return EntityDetail{}, errors.New("upstream_port must be between 1 and 65535")
		}
		var port any
		if in.UpstreamPort > 0 {
			port = in.UpstreamPort
		}
		status := "unknown"
		if in.UpstreamHost != "" {
			status = "broken"
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO routes(connector_id,domain,path_prefix,upstream_host,upstream_port,resolve_confidence,tls,status,natural_key,state,first_seen,last_seen,created_at,updated_at) VALUES(NULL,?,?,?,?, 'none',?,?,?,'active',?,?,?,?)`, domain, in.PathPrefix, in.UpstreamHost, port, in.TLS, status, key, now, now, now, now)
	case "cert":
		subject := defaultValue(strings.TrimSpace(in.Subject), name)
		endpoint := defaultValue(strings.TrimSpace(in.Endpoint), key)
		result, err = tx.ExecContext(ctx, `INSERT INTO certs(connector_id,natural_key,subject,sans,issuer,not_after,chain_valid,source,endpoint,state,first_seen,last_seen,created_at,updated_at) VALUES(NULL,?,?,'[]',?,?,0,'manual',?,'active',?,?,?,?)`, key, subject, in.Authority, expiry, endpoint, now, now, now, now)
	case "domain":
		result, err = tx.ExecContext(ctx, `INSERT INTO domains(connector_id,natural_key,name,registrar,expires_at,nameservers,source,state,first_seen,last_seen,created_at,updated_at) VALUES(NULL,?,?,?,?, '[]','manual','active',?,?,?,?)`, key, name, in.Registrar, expiry, now, now, now, now)
	case "expiry":
		kind := defaultValue(in.Kind, "renewal")
		if kind != "renewal" && kind != "warranty" && kind != "subscription" && kind != "other" {
			return EntityDetail{}, errors.New("expiry kind must be renewal, warranty, subscription, or other")
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO manual_expiries(natural_key,name,kind,authority,expires_at,source,state,created_at,updated_at) VALUES(?,?,?,?,?,'manual','active',?,?)`, key, name, kind, in.Authority, expiry, now, now)
	default:
		return EntityDetail{}, errors.New("unsupported manual entity type")
	}
	if err != nil {
		return EntityDetail{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return EntityDetail{}, err
	}
	if err = insertInitialMetadata(ctx, tx, typ, id, in.Notes, in.Tags, in.CustomFields, now); err != nil {
		return EntityDetail{}, err
	}
	if err = tx.Commit(); err != nil {
		return EntityDetail{}, err
	}
	return m.Detail(ctx, typ, id)
}

func (m *EntityManager) DeleteManual(ctx context.Context, entityType string, id int64) error {
	typ, table, err := normalizeEntityType(entityType)
	if err != nil {
		return err
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if typ != "expiry" {
		var connectorID sql.NullInt64
		var key string
		if err = tx.QueryRowContext(ctx, `SELECT connector_id,natural_key FROM `+table+` WHERE id=?`, id).Scan(&connectorID, &key); err != nil {
			return err
		}
		if connectorID.Valid || !strings.HasPrefix(key, "manual:") {
			return errors.New("discovered entities cannot be deleted; mark them gone by removing the source")
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	_, _ = tx.ExecContext(ctx, `DELETE FROM entity_notes WHERE entity_type=? AND entity_id=?`, typ, id)
	_, _ = tx.ExecContext(ctx, `DELETE FROM entity_tags WHERE entity_type=? AND entity_id=?`, typ, id)
	_, _ = tx.ExecContext(ctx, `DELETE FROM custom_fields WHERE entity_type=? AND entity_id=?`, typ, id)
	return tx.Commit()
}

func (m *EntityManager) Detail(ctx context.Context, entityType string, id int64) (EntityDetail, error) {
	typ, _, err := normalizeEntityType(entityType)
	if err != nil {
		return EntityDetail{}, err
	}
	columns, query := detailQuery(typ)
	if query == "" {
		return EntityDetail{}, errors.New("unsupported entity type")
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err = m.store.db.QueryRowContext(ctx, query, id).Scan(pointers...); err != nil {
		return EntityDetail{}, err
	}
	entity := make(map[string]any, len(columns))
	for i, column := range columns {
		if raw, ok := values[i].([]byte); ok {
			entity[column] = string(raw)
		} else {
			entity[column] = values[i]
		}
	}
	detail := EntityDetail{EntityType: typ, Entity: entity, Tags: []TagInput{}, CustomFields: []CustomFieldInput{}, History: []map[string]any{}}
	if base, ok := entity["notes"].(string); ok {
		detail.Notes = base
		delete(entity, "notes")
	}
	var notes string
	if err = m.store.db.QueryRowContext(ctx, `SELECT notes FROM entity_notes WHERE entity_type=? AND entity_id=?`, typ, id).Scan(&notes); err == nil {
		detail.Notes = notes
	} else if err != sql.ErrNoRows {
		return EntityDetail{}, err
	}
	rows, err := m.store.db.QueryContext(ctx, `SELECT t.name,t.color FROM tags t JOIN entity_tags et ON et.tag_id=t.id WHERE et.entity_type=? AND et.entity_id=? ORDER BY LOWER(t.name),t.id`, typ, id)
	if err != nil {
		return EntityDetail{}, err
	}
	for rows.Next() {
		var tag TagInput
		if err = rows.Scan(&tag.Name, &tag.Color); err != nil {
			rows.Close()
			return EntityDetail{}, err
		}
		detail.Tags = append(detail.Tags, tag)
	}
	rows.Close()
	rows, err = m.store.db.QueryContext(ctx, `SELECT key,kind,value FROM custom_fields WHERE entity_type=? AND entity_id=? ORDER BY LOWER(key),id`, typ, id)
	if err != nil {
		return EntityDetail{}, err
	}
	for rows.Next() {
		var field CustomFieldInput
		if err = rows.Scan(&field.Key, &field.Kind, &field.Value); err != nil {
			rows.Close()
			return EntityDetail{}, err
		}
		detail.CustomFields = append(detail.CustomFields, field)
	}
	rows.Close()
	rows, err = m.store.db.QueryContext(ctx, `SELECT id,scan_run_id,change_kind,summary,diff,seen,note,created_at FROM changes WHERE entity_type=? AND entity_id=? ORDER BY id DESC LIMIT 100`, typ, id)
	if err != nil {
		return EntityDetail{}, err
	}
	for rows.Next() {
		var change struct {
			id, run                            int64
			kind, summary, diff, note, created string
			seen                               bool
		}
		if err = rows.Scan(&change.id, &change.run, &change.kind, &change.summary, &change.diff, &change.seen, &change.note, &change.created); err != nil {
			rows.Close()
			return EntityDetail{}, err
		}
		detail.History = append(detail.History, map[string]any{"id": change.id, "scan_run_id": change.run, "change_kind": change.kind, "summary": change.summary, "diff": json.RawMessage(change.diff), "seen": change.seen, "note": change.note, "created_at": change.created})
	}
	rows.Close()
	return detail, nil
}

func detailQuery(typ string) ([]string, string) {
	switch typ {
	case "host":
		return []string{"id", "name", "kind", "address", "os", "arch", "notes", "state", "first_seen", "last_seen", "natural_key", "service_count", "port_count"}, `SELECT h.id,h.name,h.kind,h.address,h.os,h.arch,h.notes,h.state,h.first_seen,h.last_seen,h.natural_key,(SELECT COUNT(*) FROM services s WHERE s.host_id=h.id),(SELECT COUNT(*) FROM ports p WHERE p.host_id=h.id) FROM hosts h WHERE h.id=?`
	case "service":
		return []string{"id", "host_id", "host_name", "name", "kind", "stack", "image", "tag", "digest", "state", "health", "restart_policy", "raw_labels", "notes", "first_seen", "last_seen", "natural_key"}, `SELECT s.id,s.host_id,COALESCE(h.name,''),s.name,s.kind,s.stack,s.image,s.tag,s.digest,s.state,s.health,s.restart_policy,s.raw_labels,s.notes,s.first_seen,s.last_seen,s.natural_key FROM services s LEFT JOIN hosts h ON h.id=s.host_id WHERE s.id=?`
	case "port":
		return []string{"id", "service_id", "service_name", "host_id", "host_name", "number", "protocol", "published", "host_ip", "container_port", "source"}, `SELECT p.id,p.service_id,s.name,p.host_id,COALESCE(h.name,''),p.number,p.protocol,p.published,p.host_ip,p.container_port,p.source FROM ports p JOIN services s ON s.id=p.service_id LEFT JOIN hosts h ON h.id=p.host_id WHERE p.id=?`
	case "route":
		return []string{"id", "domain", "path_prefix", "upstream_host", "upstream_port", "resolved_service_id", "service_name", "proxy_kind", "resolve_confidence", "tls", "status", "state", "first_seen", "last_seen", "natural_key"}, `SELECT r.id,r.domain,r.path_prefix,r.upstream_host,r.upstream_port,r.resolved_service_id,COALESCE(s.name,''),COALESCE(p.kind,''),r.resolve_confidence,r.tls,r.status,r.state,r.first_seen,r.last_seen,r.natural_key FROM routes r LEFT JOIN services s ON s.id=r.resolved_service_id LEFT JOIN proxies p ON p.id=r.proxy_id WHERE r.id=?`
	case "cert":
		return []string{"id", "subject", "sans", "issuer", "not_after", "chain_valid", "source", "endpoint", "state", "first_seen", "last_seen", "natural_key"}, `SELECT id,subject,sans,issuer,not_after,chain_valid,source,endpoint,state,first_seen,last_seen,natural_key FROM certs WHERE id=?`
	case "domain":
		return []string{"id", "name", "registrar", "expires_at", "nameservers", "source", "last_checked", "state", "first_seen", "last_seen", "natural_key"}, `SELECT id,name,registrar,expires_at,nameservers,source,last_checked,state,first_seen,last_seen,natural_key FROM domains WHERE id=?`
	case "expiry":
		return []string{"id", "name", "kind", "authority", "expires_at", "source", "state", "created_at", "updated_at", "natural_key"}, `SELECT id,name,kind,authority,expires_at,source,state,created_at,updated_at,natural_key FROM manual_expiries WHERE id=?`
	default:
		return nil, ""
	}
}

func requireEntity(ctx context.Context, tx *sql.Tx, table string, id int64) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func insertInitialMetadata(ctx context.Context, tx *sql.Tx, typ string, id int64, notes string, tags []TagInput, fields []CustomFieldInput, now string) error {
	if notes != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES(?,?,?,?)`, typ, id, notes, now); err != nil {
			return err
		}
	}
	if err := writeEntityTags(ctx, tx, typ, id, tags, false); err != nil {
		return err
	}
	return writeCustomFields(ctx, tx, typ, id, fields, false)
}

func writeEntityTags(ctx context.Context, tx *sql.Tx, typ string, id int64, inputs []TagInput, replace bool) error {
	tags := normalizeTags(inputs)
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM entity_tags WHERE entity_type=? AND entity_id=?`, typ, id); err != nil {
			return err
		}
	}
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tags(name,color) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET color=CASE WHEN excluded.color='' THEN tags.color ELSE excluded.color END`, tag.Name, tag.Color); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entity_tags(tag_id,entity_type,entity_id) SELECT id,?,? FROM tags WHERE name=?`, typ, id, tag.Name); err != nil {
			return err
		}
	}
	return nil
}

func normalizeTags(inputs []TagInput) []TagInput {
	tags := make([]TagInput, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, tag := range inputs {
		tag.Name = strings.TrimSpace(tag.Name)
		if tag.Name == "" {
			continue
		}
		if _, ok := seen[tag.Name]; ok {
			continue
		}
		seen[tag.Name] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

func writeCustomFields(ctx context.Context, tx *sql.Tx, typ string, id int64, inputs []CustomFieldInput, replace bool) error {
	fields, err := normalizeCustomFields(inputs)
	if err != nil {
		return err
	}
	if replace {
		if _, err = tx.ExecContext(ctx, `DELETE FROM custom_fields WHERE entity_type=? AND entity_id=?`, typ, id); err != nil {
			return err
		}
	}
	for _, field := range fields {
		if _, err = tx.ExecContext(ctx, `INSERT INTO custom_fields(entity_type,entity_id,key,kind,value) VALUES(?,?,?,?,?)`, typ, id, field.Key, field.Kind, field.Value); err != nil {
			return err
		}
	}
	return nil
}

func normalizeCustomFields(inputs []CustomFieldInput) ([]CustomFieldInput, error) {
	fields := make([]CustomFieldInput, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, field := range inputs {
		field.Key = strings.TrimSpace(field.Key)
		if field.Key == "" {
			return nil, errors.New("custom field key is required")
		}
		if _, ok := seen[field.Key]; ok {
			return nil, fmt.Errorf("duplicate custom field %q", field.Key)
		}
		if !validFieldKind(field.Kind) {
			return nil, fmt.Errorf("unsupported custom field kind %q", field.Kind)
		}
		seen[field.Key] = struct{}{}
		fields = append(fields, field)
	}
	return fields, nil
}

func validFieldKind(kind string) bool {
	return kind == "text" || kind == "date" || kind == "url" || kind == "number"
}

func manualKey(typ string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "manual:" + typ + ":" + base64.RawURLEncoding.EncodeToString(b), nil
}

func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
