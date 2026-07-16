package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type principalContextKey struct{}

type principal struct {
	kind string
}

func isShareRequest(r *http.Request) bool {
	value, _ := r.Context().Value(principalContextKey{}).(principal)
	return value.kind == "share"
}

func shareTokenFromRequest(r *http.Request) string {
	if cookie, err := r.Cookie("homedex_share"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if token := strings.TrimSpace(r.Header.Get("X-Homedex-Share")); token != "" {
		return token
	}
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return strings.TrimSpace(authorization[len("Bearer "):])
	}
	if token := r.URL.Query().Get("share_token"); token != "" {
		return token
	}
	return r.URL.Query().Get("share")
}

func (s *Server) acceptShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	share, err := s.shares.Validate(r.Context(), token)
	if err != nil {
		http.Error(w, "share link is invalid, expired, or revoked", http.StatusUnauthorized)
		return
	}
	cookie := &http.Cookie{Name: "homedex_share", Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode}
	if share.ExpiresAt != nil {
		cookie.Expires = *share.ExpiresAt
	}
	http.SetCookie(w, cookie)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) authorizeShareScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isShareRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			http.Error(w, "share tokens are read-only", http.StatusForbidden)
			return
		}
		path := r.URL.Path
		allowed := path == "/api/summary" || path == "/api/services" || path == "/api/hosts" || path == "/api/ports" || path == "/api/ports/conflicts" || path == "/api/ports/next-free" || path == "/api/routes" || path == "/api/certs" || path == "/api/domains" || path == "/api/expiry" || path == "/api/changes" || strings.HasPrefix(path, "/api/entities/") || strings.HasPrefix(path, "/api/export/")
		if !allowed {
			http.Error(w, "share token scope does not allow this resource", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
