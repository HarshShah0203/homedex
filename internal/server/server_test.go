package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/HarshShah0203/homedex/internal/store"
)

func TestHealthVersionAndListEndpoints(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := New(st, NewBroker(), Config{Version: "v0.1-test", NoAuth: true})
	for path, status := range map[string]int{"/api/health": 200, "/api/version": 200, "/api/services": 200, "/api/hosts": 200, "/api/ports": 200, "/api/routes": 200, "/api/certs": 200, "/api/domains": 200, "/api/changes": 200} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Errorf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var body struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Items == nil || body.Total != 0 {
		t.Fatalf("unexpected list response: %s", rec.Body.String())
	}
}

func TestListsRequireAuthenticationByDefault(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := New(st, NewBroker(), Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}
