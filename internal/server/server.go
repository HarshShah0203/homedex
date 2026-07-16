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
	"github.com/HarshShah0203/homedex/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static/*
var staticFiles embed.FS

type Config struct {
	Version       string
	NoAuth        bool
	SecureCookies bool
}
type Server struct {
	store    *store.Store
	sessions *auth.SessionManager
	broker   *Broker
	cfg      Config
	limiter  *loginLimiter
}

func New(s *store.Store, b *Broker, cfg Config) http.Handler {
	x := &Server{store: s, sessions: auth.NewSessionManager(s.DB(), 24*time.Hour), broker: b, cfg: cfg, limiter: newLoginLimiter()}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, middleware.Timeout(30*time.Second))
	r.Get("/api/health", x.health)
	r.Get("/api/version", x.version)
	r.Post("/api/setup", x.setup)
	r.Post("/api/auth/login", x.login)
	r.Group(func(api chi.Router) {
		api.Use(x.authenticate)
		api.Use(x.csrf)
		api.Post("/api/auth/logout", x.logout)
		api.Get("/api/services", x.list("services", []string{"id", "name", "kind", "stack", "image", "tag", "state", "first_seen", "last_seen", "natural_key"}))
		api.Get("/api/hosts", x.list("hosts", []string{"id", "name", "kind", "address", "os", "arch", "state", "first_seen", "last_seen", "natural_key"}))
		api.Get("/api/ports", x.list("ports", []string{"id", "service_id", "host_id", "number", "protocol", "published", "host_ip", "container_port", "source"}))
		api.Get("/api/routes", x.list("routes", []string{"id", "domain", "path_prefix", "upstream_host", "upstream_port", "resolve_confidence", "tls", "status", "state"}))
		api.Get("/api/certs", x.list("certs", []string{"id", "subject", "issuer", "not_after", "chain_valid", "source", "endpoint", "state"}))
		api.Get("/api/domains", x.list("domains", []string{"id", "name", "registrar", "expires_at", "source", "last_checked", "state"}))
		api.Get("/api/changes", x.list("changes", []string{"id", "scan_run_id", "entity_type", "entity_id", "change_kind", "summary", "diff", "seen", "created_at"}))
		api.Get("/api/events", x.events)
	})
	assets, _ := fs.Sub(staticFiles, "static")
	r.Handle("/*", http.FileServer(http.FS(assets)))
	return r
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

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
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
	if !s.limiter.allow(r.RemoteAddr, time.Now()) {
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
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("homedex_session")
		if err != nil {
			http.Error(w, "authentication required", 401)
			return
		}
		if _, err = s.sessions.Validate(r.Context(), c.Value); err != nil {
			http.Error(w, "authentication required", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.NoAuth || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
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
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{attempts: map[string][]time.Time{}} }
func (l *loginLimiter) allow(remote string, now time.Time) bool {
	host := remote
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-time.Minute)
	kept := l.attempts[host][:0]
	for _, t := range l.attempts[host] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 5 {
		l.attempts[host] = kept
		return false
	}
	l.attempts[host] = append(kept, now)
	return true
}
