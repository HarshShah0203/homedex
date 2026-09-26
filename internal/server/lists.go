package server

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/HarshShah0203/homedex/internal/imageref"
)

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	limit, offset := listPage(r)
	lookups, err := s.imageLookups(r)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT s.id,s.host_id,COALESCE(h.name,''),s.name,s.kind,s.stack,s.image,s.tag,s.digest,s.state,s.health,s.restart_policy,s.first_seen,s.last_seen,s.natural_key,
		COALESCE((SELECT GROUP_CONCAT(mapping, ', ') FROM (SELECT CASE WHEN p.published=1 THEN printf('%d → %d/%s',p.number,p.container_port,p.protocol) ELSE printf('%d/%s',p.container_port,p.protocol) END mapping FROM ports p WHERE p.service_id=s.id ORDER BY p.number,p.container_port,p.protocol)),''),
		COALESCE((SELECT r.domain FROM routes r WHERE r.resolved_service_id=s.id AND r.state!='gone' ORDER BY LOWER(r.domain),r.id LIMIT 1),''),
		s.repo_digests,COALESCE(co.kind,'')
		FROM services s LEFT JOIN hosts h ON h.id=s.host_id LEFT JOIN connectors co ON co.id=s.connector_id ORDER BY LOWER(COALESCE(h.name,'')),LOWER(s.name),s.id LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var hostID sql.NullInt64
		var host, name, kind, stack, image, tag, digest, state, health, restart, first, last, natural, ports, route, repoDigests, source string
		if err = rows.Scan(&id, &hostID, &host, &name, &kind, &stack, &image, &tag, &digest, &state, &health, &restart, &first, &last, &natural, &ports, &route, &repoDigests, &source); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		item := map[string]any{"id": id, "host_id": nullInt(hostID), "host": host, "name": name, "kind": kind, "stack": stack, "image": image, "tag": tag, "digest": digest, "state": state, "health": health, "restart_policy": restart, "first_seen": first, "last_seen": last, "natural_key": natural, "ports": ports, "route": route, "update_status": nil, "update_checked_at": nil}
		addUpdateStatus(item, source, kind, state, image, tag, repoDigests, lookups)
		items = append(items, item)
	}
	writeList(w, r, s, "services", items, limit, offset)
}

// storedLookup is one image_updates row as the services list needs it.
type storedLookup struct {
	lookup    imageref.Lookup
	checkedAt string
}

// imageLookups loads the current registry lookups, keyed by image reference.
// There is one row per distinct reference the containers run, so the set
// stays small. Retired rows (references no container ran at the last check,
// or ones the source now skips) are kept only so the change feed does not
// repeat itself, and never reach the API.
func (s *Server) imageLookups(r *http.Request) (map[string]storedLookup, error) {
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT image_ref,lookup,remote_digest,reason,checked_at FROM image_updates WHERE retired_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedLookup{}
	for rows.Next() {
		var ref string
		var x storedLookup
		if err = rows.Scan(&ref, &x.lookup.Outcome, &x.lookup.RemoteDigest, &x.lookup.Reason, &x.checkedAt); err != nil {
			return nil, err
		}
		out[ref] = x
	}
	return out, rows.Err()
}

// addUpdateStatus reports whether a newer image is published for the tag a
// container runs. Containers whose reference no registry source has checked
// keep update_status null, which the UI shows as nothing at all, and so do
// containers that are gone (they run nothing that could be updated) and
// containers another kind of source found, such as an SSH host: they carry
// no registry digests, so they are never checked (imageref.SourceKind).
func addUpdateStatus(item map[string]any, source, kind, state, image, tag, repoDigestsJSON string, lookups map[string]storedLookup) {
	if source != imageref.SourceKind || kind != "container" || state == "gone" || image == "" {
		return
	}
	stored, ok := lookups[imageref.Join(image, tag)]
	if !ok {
		return
	}
	var repoDigests []string
	_ = json.Unmarshal([]byte(repoDigestsJSON), &repoDigests)
	result := imageref.Compare(image, tag, repoDigests, stored.lookup)
	item["update_status"] = result.Status
	item["update_checked_at"] = stored.checkedAt
	item["update_reason"] = result.Reason
	item["running_digest"] = result.Running
	if stored.lookup.Outcome == imageref.LookupResolved {
		item["latest_digest"] = stored.lookup.RemoteDigest
	} else {
		item["latest_digest"] = ""
	}
}

func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) {
	limit, offset := listPage(r)
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT h.id,h.name,h.kind,h.address,h.os,h.arch,h.state,h.first_seen,h.last_seen,h.natural_key,(SELECT COUNT(*) FROM services s WHERE s.host_id=h.id AND s.state!='gone'),(SELECT COUNT(*) FROM ports p WHERE p.host_id=h.id),h.aliases,h.reported_last_seen,h.power_state,h.parent_host_id,COALESCE(parent.name,'') FROM hosts h LEFT JOIN hosts parent ON parent.id=h.parent_host_id ORDER BY LOWER(h.name),h.id LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, kind, address, os, arch, state, first, last, natural, aliasesJSON, power, parentName string
		var services, ports int
		var reported sql.NullString
		var parentID sql.NullInt64
		if err = rows.Scan(&id, &name, &kind, &address, &os, &arch, &state, &first, &last, &natural, &services, &ports, &aliasesJSON, &reported, &power, &parentID, &parentName); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		aliases := []string{}
		if json.Unmarshal([]byte(aliasesJSON), &aliases) != nil || aliases == nil {
			aliases = []string{}
		}
		var parent any
		if parentID.Valid {
			parent = parentID.Int64
		}
		items = append(items, map[string]any{"id": id, "name": name, "kind": kind, "address": address, "os": os, "arch": arch, "state": state, "first_seen": first, "last_seen": last, "natural_key": natural, "services": services, "ports": ports, "aliases": aliases, "reported_last_seen": nullString(reported), "power_state": power, "parent_id": parent, "parent": parentName})
	}
	writeList(w, r, s, "hosts", items, limit, offset)
}

func (s *Server) listPorts(w http.ResponseWriter, r *http.Request) {
	limit, offset := listPage(r)
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT p.id,p.service_id,s.name,p.host_id,COALESCE(h.name,''),p.number,p.protocol,p.published,p.host_ip,p.container_port,p.source FROM ports p JOIN services s ON s.id=p.service_id LEFT JOIN hosts h ON h.id=p.host_id ORDER BY LOWER(COALESCE(h.name,'')),p.number,p.protocol,LOWER(s.name),p.id LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, serviceID int64
		var hostID sql.NullInt64
		var service, host, protocol, hostIP, source string
		var number, containerPort int
		var published bool
		if err = rows.Scan(&id, &serviceID, &service, &hostID, &host, &number, &protocol, &published, &hostIP, &containerPort, &source); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		items = append(items, map[string]any{"id": id, "service_id": serviceID, "service": service, "host_id": nullInt(hostID), "host": host, "number": number, "protocol": protocol, "published": published, "host_ip": hostIP, "container_port": containerPort, "source": source})
	}
	writeList(w, r, s, "ports", items, limit, offset)
}

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	limit, offset := listPage(r)
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT r.id,r.proxy_id,COALESCE(p.kind,''),r.domain,r.path_prefix,r.upstream_host,r.upstream_port,r.resolved_service_id,COALESCE(s.name,''),r.resolve_confidence,r.tls,r.status,r.state,c.not_after FROM routes r LEFT JOIN proxies p ON p.id=r.proxy_id LEFT JOIN services s ON s.id=r.resolved_service_id LEFT JOIN certs c ON c.id=r.cert_id ORDER BY LOWER(r.domain),r.path_prefix,r.id LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var proxyID, port, serviceID sql.NullInt64
		var proxy, domain, path, upstream, service, confidence, status, state string
		var tls bool
		var certExpiry sql.NullString
		if err = rows.Scan(&id, &proxyID, &proxy, &domain, &path, &upstream, &port, &serviceID, &service, &confidence, &tls, &status, &state, &certExpiry); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		items = append(items, map[string]any{"id": id, "proxy_id": nullInt(proxyID), "proxy": proxy, "domain": domain, "path_prefix": path, "upstream_host": upstream, "upstream_port": nullInt(port), "resolved_service_id": nullInt(serviceID), "service": service, "resolve_confidence": confidence, "tls": tls, "status": status, "state": state, "cert_expires_at": nullString(certExpiry)})
	}
	writeList(w, r, s, "routes", items, limit, offset)
}

func (s *Server) listChanges(w http.ResponseWriter, r *http.Request) {
	limit, offset := listPage(r)
	rows, err := s.store.DB().QueryContext(r.Context(), `SELECT c.id,c.scan_run_id,c.entity_type,c.entity_id,c.change_kind,c.summary,c.diff,c.seen,c.note,c.created_at,COALESCE(co.name,''),sr.started_at,sr.finished_at,sr.status FROM changes c JOIN scan_runs sr ON sr.id=c.scan_run_id LEFT JOIN connectors co ON co.id=sr.connector_id ORDER BY c.id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, runID, entityID int64
		var typ, kind, summary, diff, note, created, connector, started, status string
		var seen bool
		var finished sql.NullString
		if err = rows.Scan(&id, &runID, &typ, &entityID, &kind, &summary, &diff, &seen, &note, &created, &connector, &started, &finished, &status); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		item := map[string]any{"id": id, "scan_run_id": runID, "entity_type": typ, "entity_id": entityID, "change_kind": kind, "summary": summary, "diff": json.RawMessage(diff), "seen": seen, "created_at": created, "connector": connector, "scan_started_at": started, "scan_finished_at": nullString(finished), "scan_status": status}
		if !isShareRequest(r) {
			item["note"] = note
		}
		items = append(items, item)
	}
	writeList(w, r, s, "changes", items, limit, offset)
}

func listPage(r *http.Request) (int, int) {
	return queryInt(r, "limit", 100, 1, 500), queryInt(r, "offset", 0, 0, 1_000_000)
}
func writeList(w http.ResponseWriter, r *http.Request, s *Server, table string, items []map[string]any, limit, offset int) {
	var total int
	if err := s.store.DB().QueryRowContext(r.Context(), `SELECT COUNT(*) FROM `+table).Scan(&total); err != nil {
		http.Error(w, "database error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
func nullInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
func nullString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
