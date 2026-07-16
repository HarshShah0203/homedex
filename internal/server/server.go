package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/notify"
	"github.com/HarshShah0203/homedex/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static/*
var staticFiles embed.FS

type Config struct {
	Version          string
	NoAuth           bool
	SecureCookies    bool
	TrustedProxies   TrustedProxySet
	ConnectorConfigs *store.ConnectorConfigs
	Registry         *connectors.Registry
	Runner           *engine.Runner
	Notifications    *notify.Manager
}
type Server struct {
	store    *store.Store
	sessions *auth.SessionManager
	broker   *Broker
	cfg      Config
	limiter  *loginLimiter
	configs  *store.ConnectorConfigs
	registry *connectors.Registry
	runner   *engine.Runner
	shares   *auth.ShareManager
	entities *store.EntityManager
	notify   *notify.Manager
	setupMu  sync.Mutex
}

func New(s *store.Store, b *Broker, cfg Config) http.Handler {
	x := &Server{store: s, sessions: auth.NewSessionManager(s.DB(), 24*time.Hour), shares: auth.NewShareManager(s.DB()), entities: store.NewEntityManager(s), broker: b, cfg: cfg, limiter: newLoginLimiter(), configs: cfg.ConnectorConfigs, registry: cfg.Registry, runner: cfg.Runner, notify: cfg.Notifications}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Timeout(75*time.Second))
	r.Get("/api/health", x.health)
	r.Get("/api/version", x.version)
	r.Get("/api/setup/status", x.setupStatus)
	r.Post("/api/setup", x.setup)
	r.Post("/api/auth/login", x.login)
	r.Get("/share/{token}", x.acceptShare)
	r.Group(func(api chi.Router) {
		api.Use(x.authenticate)
		api.Use(x.authorizeShareScope)
		api.Use(x.csrf)
		api.Post("/api/auth/logout", x.logout)
		api.Get("/api/summary", x.summary)
		api.Get("/api/services", x.listServices)
		api.Get("/api/hosts", x.listHosts)
		api.Get("/api/ports", x.listPorts)
		api.Get("/api/ports/conflicts", x.portConflicts)
		api.Get("/api/ports/next-free", x.nextFreePort)
		api.Get("/api/routes", x.listRoutes)
		api.Get("/api/certs", x.list("certs", []string{"id", "subject", "issuer", "not_after", "chain_valid", "source", "endpoint", "state"}))
		api.Get("/api/domains", x.list("domains", []string{"id", "name", "registrar", "expires_at", "source", "last_checked", "state"}))
		api.Get("/api/expiry", x.expiry)
		api.Get("/api/changes", x.listChanges)
		api.Patch("/api/changes", x.bulkReviewChanges)
		api.Post("/api/changes/review", x.bulkReviewChanges)
		api.Patch("/api/changes/{id}", x.reviewChange)
		api.Get("/api/search", x.search)
		api.Get("/api/entities/{type}/{id}", x.getEntity)
		api.Patch("/api/entities/{type}/{id}", x.patchEntity)
		api.Delete("/api/entities/{type}/{id}", x.deleteEntity)
		api.Post("/api/entities", x.createEntity)
		api.Get("/api/export/{format}", x.exportInventory)
		api.Get("/api/share", x.listShares)
		api.Post("/api/share", x.createShare)
		api.Delete("/api/share/{id}", x.revokeShare)
		api.Post("/api/share/{id}/revoke", x.revokeShare)
		api.Get("/api/notify/rules", x.listNotificationRules)
		api.Post("/api/notify/rules", x.createNotificationRule)
		api.Put("/api/notify/rules/{id}", x.updateNotificationRule)
		api.Patch("/api/notify/rules/{id}", x.updateNotificationRule)
		api.Delete("/api/notify/rules/{id}", x.deleteNotificationRule)
		api.Post("/api/notify/rules/{id}/test", x.testNotificationRule)
		api.Get("/api/connectors", x.listConnectors)
		api.Get("/api/connectors/{id}", x.getConnector)
		api.Post("/api/connectors/test", x.testUnsavedConnector)
		api.Post("/api/connectors", x.createConnector)
		api.Put("/api/connectors/{id}", x.updateConnector)
		api.Patch("/api/connectors/{id}", x.updateConnector)
		api.Delete("/api/connectors/{id}", x.deleteConnector)
		api.Post("/api/connectors/{id}/test", x.testConnector)
		api.Post("/api/connectors/{id}/scan", x.scanConnector)
		api.Get("/api/connectors/{id}/scans", x.connectorScans)
		api.Get("/api/events", x.events)
	})
	assets, _ := fs.Sub(staticFiles, "static")
	r.Handle("/*", spaFileServer(assets))
	return r
}

// spaFileServer serves fingerprinted build assets directly and falls back to
// index.html for client-side routes such as /hosts/1 and /settings/connectors.
// Unknown API paths remain real 404s instead of returning the application shell.
func spaFileServer(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(assets, path); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "api/") {
			http.NotFound(w, r)
			return
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

type connectorInput struct {
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	Config          connectors.Config `json:"config"`
	Enabled         *bool             `json:"enabled"`
	ScheduleMinutes int               `json:"schedule_minutes"`
}

func (s *Server) discoveryReady(w http.ResponseWriter) bool {
	if s.configs == nil || s.registry == nil || s.runner == nil {
		http.Error(w, "connector discovery is unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}
func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	v, e := s.configs.List(r.Context())
	if e != nil {
		http.Error(w, "database error", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"items": v, "total": len(v)})
}
func (s *Server) getConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	v, e := s.configs.Record(r.Context(), id)
	if e != nil {
		http.Error(w, "connector not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (s *Server) createConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	var in connectorInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Name == "" || in.Kind == "" {
		http.Error(w, "name and kind are required", 400)
		return
	}
	if _, ok := s.registry.Get(in.Kind); !ok {
		http.Error(w, "unknown connector kind", 400)
		return
	}
	id, e := s.configs.Create(r.Context(), in.Kind, in.Name, in.Config)
	if e != nil {
		http.Error(w, "database error", 500)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	schedule := in.ScheduleMinutes
	if schedule == 0 {
		schedule = 15
	}
	if e = s.configs.Update(r.Context(), id, in.Name, in.Config, enabled, schedule); e != nil {
		http.Error(w, "database error", 500)
		return
	}
	var run int64
	var changes int
	var scanErr error
	if enabled {
		run, changes, scanErr = s.runner.Scan(r.Context(), id)
	}
	rec, _ := s.configs.Record(r.Context(), id)
	status := http.StatusCreated
	if scanErr != nil {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{"connector": rec, "scan_run_id": run, "changes": changes, "scan_error": errorString(scanErr)})
}
func (s *Server) updateConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in connectorInput
	if !decodeJSON(w, r, &in) {
		return
	}
	old, e := s.configs.Record(r.Context(), id)
	if e != nil {
		http.Error(w, "connector not found", 404)
		return
	}
	if in.Kind != "" && in.Kind != old.Kind {
		http.Error(w, "connector kind cannot be changed", 400)
		return
	}
	if in.Config == nil {
		if e = s.configs.Load(r.Context(), id, &in.Config); e != nil {
			http.Error(w, "database error", 500)
			return
		}
	}
	enabled := old.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if in.Name == "" {
		in.Name = old.Name
	}
	if in.ScheduleMinutes == 0 {
		in.ScheduleMinutes = old.ScheduleMinutes
	}
	if e = s.configs.Update(r.Context(), id, in.Name, in.Config, enabled, in.ScheduleMinutes); e != nil {
		http.Error(w, "database error", 500)
		return
	}
	var run int64
	var changes int
	var scanErr error
	if enabled {
		run, changes, scanErr = s.runner.Scan(r.Context(), id)
	}
	rec, _ := s.configs.Record(r.Context(), id)
	status := 200
	if scanErr != nil {
		status = 502
	}
	writeJSON(w, status, map[string]any{"connector": rec, "scan_run_id": run, "changes": changes, "scan_error": errorString(scanErr)})
}
func (s *Server) deleteConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if e := s.configs.Delete(r.Context(), id); e != nil {
		http.Error(w, "connector not found", 404)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) testConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if e := s.runner.Test(r.Context(), id); e != nil {
		writeJSON(w, 502, map[string]any{"status": "error", "error": e.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func (s *Server) scanConnector(w http.ResponseWriter, r *http.Request) {
	if !s.discoveryReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, changes, e := s.runner.Scan(r.Context(), id)
	if e != nil {
		writeJSON(w, 502, map[string]any{"status": "error", "error": e.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "success", "scan_run_id": run, "changes": changes})
}
func (s *Server) connectorScans(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rows, e := s.store.DB().QueryContext(r.Context(), `SELECT id,started_at,finished_at,status,error,stats FROM scan_runs WHERE connector_id=? ORDER BY id DESC LIMIT 50`, id)
	if e != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var rid int64
		var start, status, scanErr, stats string
		var finish any
		if e = rows.Scan(&rid, &start, &finish, &status, &scanErr, &stats); e != nil {
			http.Error(w, "database error", 500)
			return
		}
		out = append(out, map[string]any{"id": rid, "started_at": start, "finished_at": finish, "status": status, "error": scanErr, "stats": json.RawMessage(stats)})
	}
	writeJSON(w, 200, map[string]any{"items": out, "total": len(out)})
}
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if e != nil || id <= 0 {
		http.Error(w, "invalid id", 400)
		return 0, false
	}
	return id, true
}
func errorString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, map[string]any{"items": []any{}, "total": 0})
		return
	}
	terms := strings.Fields(q)
	for i := range terms {
		terms[i] = `"` + strings.ReplaceAll(terms[i], `"`, `""`) + `"*`
	}
	rows, e := s.store.DB().QueryContext(r.Context(), `SELECT entity_type,entity_id,title,body,bm25(search_index) rank FROM search_index WHERE search_index MATCH ? ORDER BY rank LIMIT 50`, strings.Join(terms, " "))
	if e != nil {
		http.Error(w, "invalid search query", 400)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var typ, title, body string
		var id int64
		var rank float64
		if e = rows.Scan(&typ, &id, &title, &body, &rank); e != nil {
			http.Error(w, "database error", 500)
			return
		}
		out = append(out, map[string]any{"entity_type": typ, "entity_id": id, "title": title, "body": body, "rank": rank})
	}
	rows.Close()
	like := "%" + q + "%"
	queries := []struct{ sql, typ string }{
		{`SELECT id,name,registrar FROM domains WHERE state!='gone' AND (name LIKE ? OR registrar LIKE ?) LIMIT 20`, "domain"},
		{`SELECT id,subject,endpoint FROM certs WHERE state!='gone' AND (subject LIKE ? OR sans LIKE ? OR endpoint LIKE ?) LIMIT 20`, "cert"},
	}
	for _, query := range queries {
		args := []any{like, like}
		if query.typ == "cert" {
			args = append(args, like)
		}
		extra, err := s.store.DB().QueryContext(r.Context(), query.sql, args...)
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		for extra.Next() {
			var id int64
			var title, body string
			if err = extra.Scan(&id, &title, &body); err != nil {
				extra.Close()
				http.Error(w, "database error", 500)
				return
			}
			out = append(out, map[string]any{"entity_type": query.typ, "entity_id": id, "title": title, "body": body, "rank": 0})
		}
		extra.Close()
	}
	if number, err := strconv.Atoi(q); err == nil {
		extra, err := s.store.DB().QueryContext(r.Context(), `SELECT id,number,host_ip FROM ports WHERE number=? OR container_port=? LIMIT 20`, number, number)
		if err == nil {
			for extra.Next() {
				var id, n int64
				var host string
				if extra.Scan(&id, &n, &host) == nil {
					out = append(out, map[string]any{"entity_type": "port", "entity_id": id, "title": strconv.FormatInt(n, 10), "body": host, "rank": 0})
				}
			}
			extra.Close()
		}
	}
	metadata, err := s.store.DB().QueryContext(r.Context(), `
		SELECT entity_type,entity_id,entity_type||' #'||entity_id,notes FROM entity_notes WHERE notes LIKE ?
		UNION ALL
		SELECT cf.entity_type,cf.entity_id,cf.entity_type||' #'||cf.entity_id,cf.key||': '||cf.value FROM custom_fields cf WHERE cf.key LIKE ? OR cf.value LIKE ?
		UNION ALL
		SELECT et.entity_type,et.entity_id,et.entity_type||' #'||et.entity_id,'tag: '||t.name FROM entity_tags et JOIN tags t ON t.id=et.tag_id WHERE t.name LIKE ?
		UNION ALL
		SELECT 'expiry',id,name,kind||' '||authority FROM manual_expiries WHERE name LIKE ? OR authority LIKE ?
		LIMIT 30`, like, like, like, like, like, like)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	for metadata.Next() {
		var typ, title, body string
		var id int64
		if err = metadata.Scan(&typ, &id, &title, &body); err != nil {
			metadata.Close()
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		out = append(out, map[string]any{"entity_type": typ, "entity_id": id, "title": title, "body": body, "rank": 0})
	}
	metadata.Close()
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, 200, map[string]any{"items": out, "total": len(out)})
}
func (s *Server) portConflicts(w http.ResponseWriter, r *http.Request) {
	rows, e := s.store.DB().QueryContext(r.Context(), `WITH conflicting AS (SELECT DISTINCT p.id FROM ports p JOIN ports q ON p.id!=q.id AND p.host_id=q.host_id AND p.number=q.number AND p.protocol=q.protocol AND (p.host_ip=q.host_ip OR p.host_ip IN ('','0.0.0.0','::') OR q.host_ip IN ('','0.0.0.0','::')) WHERE p.published=1 AND q.published=1) SELECT p.host_id,p.number,p.protocol,COUNT(*) count,GROUP_CONCAT(p.service_id) services FROM ports p JOIN conflicting c ON c.id=p.id GROUP BY p.host_id,p.number,p.protocol ORDER BY p.number`)
	if e != nil {
		http.Error(w, "database error", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var host any
		var number, count int
		var protocol, services string
		if e = rows.Scan(&host, &number, &protocol, &count, &services); e != nil {
			http.Error(w, "database error", 500)
			return
		}
		out = append(out, map[string]any{"host_id": host, "number": number, "protocol": protocol, "count": count, "service_ids": strings.Split(services, ",")})
	}
	writeJSON(w, 200, map[string]any{"items": out, "total": len(out)})
}
func (s *Server) nextFreePort(w http.ResponseWriter, r *http.Request) {
	start := queryInt(r, "start", 1024, 1, 65535)
	end := queryInt(r, "end", 65535, start, 65535)
	protocol := r.URL.Query().Get("protocol")
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		http.Error(w, "protocol must be tcp or udp", 400)
		return
	}
	hostID, e := strconv.ParseInt(r.URL.Query().Get("host_id"), 10, 64)
	if e != nil {
		http.Error(w, "host_id is required", 400)
		return
	}
	var hostExists int
	if e = s.store.DB().QueryRowContext(r.Context(), `SELECT COUNT(*) FROM hosts WHERE id=?`, hostID).Scan(&hostExists); e != nil || hostExists == 0 {
		http.Error(w, "host not found", 404)
		return
	}
	used := map[int]bool{}
	rows, e := s.store.DB().QueryContext(r.Context(), `SELECT number FROM ports WHERE host_id=? AND protocol=? AND published=1`, hostID, protocol)
	if e != nil {
		http.Error(w, "database error", 500)
		return
	}
	for rows.Next() {
		var p int
		_ = rows.Scan(&p)
		used[p] = true
	}
	rows.Close()
	for p := start; p <= end; p++ {
		if !used[p] {
			writeJSON(w, 200, map[string]any{"host_id": hostID, "protocol": protocol, "port": p})
			return
		}
	}
	http.Error(w, "no free port in range", 404)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.store.DB().PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.cfg.Version})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := s.store.DB().QueryRowContext(r.Context(), `SELECT COUNT(*) FROM settings WHERE key='admin_password_hash'`).Scan(&count); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"configured": count > 0, "auth_disabled": s.cfg.NoAuth})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	var count int
	if err := s.store.DB().QueryRowContext(r.Context(), `SELECT COUNT(*) FROM settings WHERE key='admin_password_hash'`).Scan(&count); err != nil {
		http.Error(w, "database error", 500)
		return
	}
	if count > 0 {
		http.Error(w, "setup already completed", http.StatusConflict)
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tx, err := s.store.DB().BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO settings(key,value) VALUES('admin_password_hash',?)`, hash); err != nil {
		tx.Rollback()
		// A concurrent first-run request may have won after the existence check.
		// Return the documented one-time setup conflict instead of a generic 500.
		if countErr := s.store.DB().QueryRowContext(r.Context(), `SELECT COUNT(*) FROM settings WHERE key='admin_password_hash'`).Scan(&count); countErr == nil && count > 0 {
			http.Error(w, "setup already completed", http.StatusConflict)
			return
		}
		http.Error(w, "database error", 500)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, "database error", 500)
		return
	}
	s.issueSession(w, r)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(s.cfg.TrustedProxies.clientIP(r), time.Now()) {
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var hash string
	if err := s.store.DB().QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&hash); err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if !auth.VerifyPassword(hash, in.Password) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	s.issueSession(w, r)
}
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessions.Create(r.Context(), "admin")
	if err != nil {
		http.Error(w, "database error", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "homedex_session", Value: session.Token, Path: "/", Expires: session.ExpiresAt, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"csrf": session.CSRF, "expires_at": session.ExpiresAt})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("homedex_session"); err == nil {
		_ = s.sessions.Delete(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "homedex_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.NoAuth {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal{kind: "admin"})))
			return
		}
		if c, err := r.Cookie("homedex_session"); err == nil {
			if _, err = s.sessions.Validate(r.Context(), c.Value); err == nil {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal{kind: "admin"})))
				return
			}
		}
		token := shareTokenFromRequest(r)
		if _, err := s.shares.Validate(r.Context(), token); err == nil {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal{kind: "share"})))
			return
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
	})
}
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if isShareRequest(r) {
			http.Error(w, "share tokens are read-only", http.StatusForbidden)
			return
		}
		if s.cfg.NoAuth {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("homedex_session")
		if err != nil || !s.sessions.ValidateCSRF(r.Context(), c.Value, r.Header.Get("X-Homedex-CSRF")) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) list(table string, columns []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := queryInt(r, "limit", 100, 1, 500)
		offset := queryInt(r, "offset", 0, 0, 1_000_000)
		query := fmt.Sprintf("SELECT %s FROM %s ORDER BY id LIMIT ? OFFSET ?", strings.Join(columns, ","), table)
		rows, err := s.store.DB().QueryContext(r.Context(), query, limit, offset)
		if err != nil {
			http.Error(w, "database error", 500)
			return
		}
		defer rows.Close()
		items := make([]map[string]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				http.Error(w, "database error", 500)
				return
			}
			item := map[string]any{}
			for i, c := range columns {
				if b, ok := values[i].([]byte); ok {
					item[c] = string(b)
				} else {
					item[c] = values[i]
				}
			}
			items = append(items, item)
		}
		var total int
		if err = s.store.DB().QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+table).Scan(&total); err != nil {
			http.Error(w, "database error", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
	}
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, unsubscribe := s.broker.Subscribe()
	defer unsubscribe()
	fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}
func queryInt(r *http.Request, key string, def, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

type loginLimiter struct {
	mu              sync.Mutex
	buckets         map[string]loginBucket
	lastCleanup     time.Time
	window          time.Duration
	cleanupInterval time.Duration
	maxAttempts     int
	maxBuckets      int
}

type loginBucket struct {
	attempts []time.Time
}

const overflowLoginBucket = "__overflow__"

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		buckets:         map[string]loginBucket{},
		window:          time.Minute,
		cleanupInterval: time.Minute,
		maxAttempts:     5,
		maxBuckets:      10_000,
	}
}

func (l *loginLimiter) allow(client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastCleanup.IsZero() || now.Before(l.lastCleanup) || now.Sub(l.lastCleanup) >= l.cleanupInterval {
		l.cleanup(now)
	}
	if _, exists := l.buckets[client]; !exists && len(l.buckets) >= l.maxBuckets-1 {
		client = overflowLoginBucket
	}
	bucket := l.buckets[client]
	bucket.attempts = activeAttempts(bucket.attempts, now.Add(-l.window))
	if len(bucket.attempts) >= l.maxAttempts {
		l.buckets[client] = bucket
		return false
	}
	bucket.attempts = append(bucket.attempts, now)
	l.buckets[client] = bucket
	return true
}

func (l *loginLimiter) cleanup(now time.Time) {
	cutoff := now.Add(-l.window)
	for client, bucket := range l.buckets {
		bucket.attempts = activeAttempts(bucket.attempts, cutoff)
		if len(bucket.attempts) == 0 {
			delete(l.buckets, client)
			continue
		}
		l.buckets[client] = bucket
	}
	l.lastCleanup = now
}

func activeAttempts(attempts []time.Time, cutoff time.Time) []time.Time {
	kept := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	return kept
}
