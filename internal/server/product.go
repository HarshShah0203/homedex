package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	exportpkg "github.com/HarshShah0203/homedex/internal/export"
	"github.com/HarshShah0203/homedex/internal/notify"
	"github.com/HarshShah0203/homedex/internal/store"
	"github.com/go-chi/chi/v5"
)

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	queries := map[string]string{
		"hosts":            `SELECT COUNT(*) FROM hosts WHERE state!='gone'`,
		"services":         `SELECT COUNT(*) FROM services WHERE state!='gone'`,
		"services_running": `SELECT COUNT(*) FROM services WHERE state='running'`,
		"ports":            `SELECT COUNT(*) FROM ports`,
		"ports_published":  `SELECT COUNT(*) FROM ports WHERE published=1`,
		"routes":           `SELECT COUNT(*) FROM routes WHERE state!='gone'`,
		"routes_broken":    `SELECT COUNT(*) FROM routes WHERE state!='gone' AND status='broken'`,
		"certs":            `SELECT COUNT(*) FROM certs WHERE state!='gone'`,
		"domains":          `SELECT COUNT(*) FROM domains WHERE state!='gone'`,
		"changes_unseen":   `SELECT COUNT(*) FROM changes WHERE seen=0`,
		"connectors_error": `SELECT COUNT(*) FROM connectors WHERE last_status='error'`,
	}
	counts := map[string]int{}
	for key, query := range queries {
		var count int
		if err := s.store.DB().QueryRowContext(r.Context(), query).Scan(&count); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		counts[key] = count
	}
	expiring := 0
	archive, err := exportpkg.NewLoader(s.store.DB()).Load(r.Context(), exportpkg.Options{IncludePrivate: false})
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	for _, item := range archive.Expiry {
		days, _ := exportpkg.DaysRemaining(time.Now(), item.ExpiresAt)
		if days != nil && *days <= 30 {
			expiring++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"counts":   counts,
		"services": map[string]int{"total": counts["services"], "running": counts["services_running"]},
		"hosts":    map[string]int{"total": counts["hosts"]},
		"ports":    map[string]int{"total": counts["ports"], "published": counts["ports_published"], "internal": counts["ports"] - counts["ports_published"]},
		"routes":   map[string]int{"total": counts["routes"], "broken": counts["routes_broken"], "resolved": counts["routes"] - counts["routes_broken"]},
		"expiry":   map[string]int{"total": len(archive.Expiry), "due_within_30_days": expiring},
		"changes":  map[string]int{"unseen": counts["changes_unseen"]},
	})
}

func (s *Server) expiry(w http.ResponseWriter, r *http.Request) {
	archive, err := exportpkg.NewLoader(s.store.DB()).Load(r.Context(), exportpkg.Options{IncludePrivate: false})
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(archive.Expiry))
	now := time.Now()
	for _, item := range archive.Expiry {
		days, parseErr := exportpkg.DaysRemaining(now, item.ExpiresAt)
		status := "unknown"
		if parseErr == nil && days != nil {
			switch {
			case *days < 0:
				status = "expired"
			case *days <= 14:
				status = "action_needed"
			case *days <= 30:
				status = "expiring"
			default:
				status = "upcoming"
			}
		}
		items = append(items, map[string]any{"entity_type": item.EntityType, "id": item.ID, "name": item.Name, "kind": item.Kind, "type": item.Kind, "authority": item.Authority, "expires_at": nullableJSON(item.ExpiresAt), "expires": nullableJSON(item.ExpiresAt), "days_remaining": days, "days": days, "status": status, "checked_at": item.CheckedAt, "source": item.Source, "state": item.State})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) exportInventory(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(chi.URLParam(r, "format"))
	includePrivate := !isShareRequest(r) && queryBool(r, "include_private", true)
	options := exportpkg.Options{IncludePrivate: includePrivate, MaskDomains: queryBool(r, "mask_domains", false), MaskExternalIPs: queryBool(r, "mask_external_ips", false)}
	archive, err := exportpkg.NewLoader(s.store.DB()).Load(r.Context(), options)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	var body []byte
	var contentType, filename string
	switch format {
	case "markdown", "md":
		body, contentType, filename = exportpkg.Markdown(archive), "text/markdown; charset=utf-8", "homedex-inventory.md"
	case "json":
		body, err = exportpkg.JSON(archive)
		contentType, filename = "application/json", "homedex-inventory.json"
	case "csv":
		filter := exportpkg.FilterOptions{Query: r.URL.Query().Get("q"), State: r.URL.Query().Get("state"), Status: r.URL.Query().Get("status")}
		if hostID, parseErr := strconv.ParseInt(r.URL.Query().Get("host_id"), 10, 64); parseErr == nil && hostID > 0 {
			filter.HostID = hostID
		}
		if raw := r.URL.Query().Get("published"); raw != "" {
			if value, parseErr := strconv.ParseBool(raw); parseErr == nil {
				filter.Published = &value
			}
		}
		archive = exportpkg.Filter(archive, filter)
		body, err = exportpkg.CSV(archive, r.URL.Query().Get("view"))
		contentType, filename = "text/csv; charset=utf-8", "homedex-"+defaultText(r.URL.Query().Get("view"), "services")+".csv"
	case "context":
		var report exportpkg.Truncation
		body, report = exportpkg.Context(archive)
		contentType, filename = "text/markdown; charset=utf-8", "homedex-context.md"
		w.Header().Set("X-Homedex-Context-Bytes", strconv.Itoa(report.Bytes))
		if omitted, _ := json.Marshal(report.Omitted); len(omitted) > 0 {
			w.Header().Set("X-Homedex-Truncation", string(omitted))
		}
	default:
		http.Error(w, "export format must be markdown, csv, json, or context", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	items, err := s.shares.List(r.Context())
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name           string `json:"name"`
		ExpiresAt      string `json:"expires_at"`
		ExpiresInHours int    `json:"expires_in_hours"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var expiry *time.Time
	if input.ExpiresAt != "" {
		value, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil {
			http.Error(w, "expires_at must be RFC3339", http.StatusBadRequest)
			return
		}
		expiry = &value
	} else if input.ExpiresInHours != 0 {
		if input.ExpiresInHours < 1 || input.ExpiresInHours > 24*3650 {
			http.Error(w, "expires_in_hours must be between 1 and 87600", http.StatusBadRequest)
			return
		}
		value := time.Now().Add(time.Duration(input.ExpiresInHours) * time.Hour)
		expiry = &value
	}
	created, err := s.shares.Create(r.Context(), strings.TrimSpace(input.Name), expiry)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": created.ID, "name": created.Name, "created_at": created.CreatedAt, "expires_at": created.ExpiresAt, "active": created.Active, "token": created.Token, "share_url": "/share/" + created.Token})
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.shares.Revoke(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "share token not found", http.StatusNotFound)
		} else {
			http.Error(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getEntity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid entity id", http.StatusBadRequest)
		return
	}
	detail, err := s.entities.Detail(r.Context(), chi.URLParam(r, "type"), id)
	if err != nil {
		writeEntityError(w, err)
		return
	}
	if raw, ok := detail.Entity["raw_labels"].(string); ok {
		if isShareRequest(r) {
			delete(detail.Entity, "raw_labels")
		} else {
			labels := map[string]string{}
			_ = json.Unmarshal([]byte(raw), &labels)
			safe := exportpkg.Sanitize(exportpkg.Archive{SchemaVersion: exportpkg.SchemaVersion, Services: []exportpkg.Service{{Labels: labels}}}, exportpkg.Options{IncludePrivate: true})
			detail.Entity["raw_labels"] = safe.Services[0].Labels
		}
	}
	if isShareRequest(r) {
		detail.Notes = ""
		detail.CustomFields = nil
		for _, history := range detail.History {
			delete(history, "note")
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) patchEntity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid entity id", http.StatusBadRequest)
		return
	}
	var patch store.EntityPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.Notes == nil && patch.Tags == nil && patch.CustomFields == nil {
		http.Error(w, "notes, tags, or custom_fields is required", http.StatusBadRequest)
		return
	}
	if err = s.entities.Patch(r.Context(), chi.URLParam(r, "type"), id, patch); err != nil {
		writeEntityError(w, err)
		return
	}
	detail, err := s.entities.Detail(r.Context(), chi.URLParam(r, "type"), id)
	if err != nil {
		writeEntityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) createEntity(w http.ResponseWriter, r *http.Request) {
	var input store.ManualEntityInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := s.entities.CreateManual(r.Context(), input)
	if err != nil {
		writeEntityError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func (s *Server) deleteEntity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid entity id", http.StatusBadRequest)
		return
	}
	if err = s.entities.DeleteManual(r.Context(), chi.URLParam(r, "type"), id); err != nil {
		writeEntityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type changeReview struct {
	Seen *bool   `json:"seen"`
	Note *string `json:"note"`
	IDs  []int64 `json:"ids"`
}

func (s *Server) reviewChange(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input changeReview
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Seen == nil && input.Note == nil {
		http.Error(w, "seen or note is required", http.StatusBadRequest)
		return
	}
	query, args := changeUpdate(input, `id=?`, []any{id})
	result, err := s.store.DB().ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		http.Error(w, "change not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "seen": input.Seen, "note": input.Note})
}

func (s *Server) bulkReviewChanges(w http.ResponseWriter, r *http.Request) {
	var input changeReview
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Seen == nil && input.Note == nil {
		http.Error(w, "seen or note is required", http.StatusBadRequest)
		return
	}
	if len(input.IDs) > 1000 {
		http.Error(w, "at most 1000 change ids may be reviewed at once", http.StatusBadRequest)
		return
	}
	where := "1=1"
	whereArgs := []any{}
	if len(input.IDs) > 0 {
		placeholders := make([]string, len(input.IDs))
		for i, id := range input.IDs {
			if id <= 0 {
				http.Error(w, "change ids must be positive", http.StatusBadRequest)
				return
			}
			placeholders[i] = "?"
			whereArgs = append(whereArgs, id)
		}
		where = "id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query, args := changeUpdate(input, where, whereArgs)
	result, err := s.store.DB().ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	count, _ := result.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{"updated": count})
}

func changeUpdate(input changeReview, where string, whereArgs []any) (string, []any) {
	sets := []string{}
	args := []any{}
	if input.Seen != nil {
		sets = append(sets, "seen=?")
		args = append(args, *input.Seen)
	}
	if input.Note != nil {
		sets = append(sets, "note=?")
		args = append(args, *input.Note)
	}
	args = append(args, whereArgs...)
	return `UPDATE changes SET ` + strings.Join(sets, ",") + ` WHERE ` + where, args
}

func (s *Server) listNotificationRules(w http.ResponseWriter, r *http.Request) {
	if !s.notificationsReady(w) {
		return
	}
	items, err := s.notify.List(r.Context())
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) createNotificationRule(w http.ResponseWriter, r *http.Request) {
	if !s.notificationsReady(w) {
		return
	}
	var input notify.RuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := s.notify.Create(r.Context(), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) updateNotificationRule(w http.ResponseWriter, r *http.Request) {
	if !s.notificationsReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input notify.RuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := s.notify.Update(r.Context(), id, input)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "notification rule not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteNotificationRule(w http.ResponseWriter, r *http.Request) {
	if !s.notificationsReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.notify.Delete(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "notification rule not found", http.StatusNotFound)
		} else {
			http.Error(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testNotificationRule(w http.ResponseWriter, r *http.Request) {
	if !s.notificationsReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.notify.Test(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "notification rule not found", http.StatusNotFound)
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) notificationsReady(w http.ResponseWriter) bool {
	if s.notify == nil {
		http.Error(w, "notifications are unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) testUnsavedConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	var input connectorInput
	if !decodeJSON(w, r, &input) {
		return
	}
	connector, ok := s.registry.Get(input.Kind)
	if !ok {
		http.Error(w, "unknown connector kind", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	started := time.Now()
	if err := connector.Validate(ctx, input.Config); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error(), "duration_ms": time.Since(started).Milliseconds()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "kind": input.Kind, "duration_ms": time.Since(started).Milliseconds()})
}

func writeEntityError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "entity not found", http.StatusNotFound)
		return
	}
	message := err.Error()
	if strings.Contains(message, "required") || strings.Contains(message, "unsupported") || strings.Contains(message, "must") || strings.Contains(message, "cannot") || strings.Contains(message, "duplicate") || strings.Contains(message, "not found") {
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	http.Error(w, "database error", http.StatusInternalServerError)
}

func nullableJSON(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func queryBool(r *http.Request, key string, fallback bool) bool {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func defaultText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
