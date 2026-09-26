package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarshShah0203/homedex/internal/server"
	"github.com/HarshShah0203/homedex/internal/store"
)

const (
	envPassword  = "env bootstrap passphrase 7377"
	wizardSecret = "wizard chosen passphrase 1234"
)

func openBootstrapStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "homedex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// setAdminEnv sets both bootstrap variables for one test; t.Setenv restores
// them afterwards even though bootstrapAdminFromEnv unsets them.
func setAdminEnv(t *testing.T, password, file string) {
	t.Helper()
	t.Setenv(adminPasswordEnv, password)
	t.Setenv(adminPasswordFileEnv, file)
}

func captureLogs() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func postJSON(t *testing.T, handler http.Handler, path string, body any) int {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func setupConfigured(t *testing.T, handler http.Handler) bool {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status=%d", rec.Code)
	}
	var status struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status.Configured
}

func canLogin(t *testing.T, handler http.Handler, password string) bool {
	t.Helper()
	return postJSON(t, handler, "/api/auth/login", map[string]string{"password": password}) == http.StatusOK
}

func storedHash(t *testing.T, st *store.Store) string {
	t.Helper()
	var hash string
	err := st.DB().QueryRow(`SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&hash)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func assertNoLeak(t *testing.T, where, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("%s contains the admin password: %q", where, text)
		}
	}
}

func TestBootstrapAdminOnEmptyDatabase(t *testing.T) {
	st := openBootstrapStore(t)
	setAdminEnv(t, envPassword, "")
	logger, logs := captureLogs()
	if err := bootstrapAdminFromEnv(context.Background(), st, logger); err != nil {
		t.Fatal(err)
	}
	handler := server.New(st, server.NewBroker(), server.Config{})
	if !setupConfigured(t, handler) {
		t.Fatal("setup status is not configured after the environment bootstrap")
	}
	// The wizard must now behave as configured: no second admin can be set.
	if code := postJSON(t, handler, "/api/setup", map[string]string{"password": wizardSecret}); code != http.StatusConflict {
		t.Fatalf("setup after bootstrap=%d, want 409", code)
	}
	if !canLogin(t, handler, envPassword) {
		t.Fatal("the bootstrapped password does not sign in")
	}
	if canLogin(t, handler, wizardSecret) {
		t.Fatal("a password sent to setup after bootstrap signs in")
	}
	if !strings.Contains(logs.String(), "admin password set from the environment") || !strings.Contains(logs.String(), adminPasswordEnv) {
		t.Fatalf("bootstrap log=%q", logs.String())
	}
	assertNoLeak(t, "log", logs.String(), envPassword)
	assertNoLeak(t, "stored hash", storedHash(t, st), envPassword)
	if _, ok := os.LookupEnv(adminPasswordEnv); ok {
		t.Fatal("HOMEDEX_ADMIN_PASSWORD is still in the environment for child processes")
	}
}

func TestBootstrapAdminIsIgnoredWhenAnAdminExists(t *testing.T) {
	st := openBootstrapStore(t)
	handler := server.New(st, server.NewBroker(), server.Config{})
	if code := postJSON(t, handler, "/api/setup", map[string]string{"password": wizardSecret}); code != http.StatusOK {
		t.Fatalf("wizard setup=%d", code)
	}
	before := storedHash(t, st)
	for _, password := range []string{envPassword, "short"} {
		setAdminEnv(t, password, "")
		logger, logs := captureLogs()
		if err := bootstrapAdminFromEnv(context.Background(), st, logger); err != nil {
			t.Fatalf("existing admin with %d-character variable: %v", len(password), err)
		}
		if got := storedHash(t, st); got != before {
			t.Fatal("the environment bootstrap replaced an existing admin")
		}
		if n := strings.Count(logs.String(), "environment value is ignored"); n != 1 {
			t.Fatalf("ignored notices=%d in %q, want exactly 1", n, logs.String())
		}
		assertNoLeak(t, "log", logs.String(), password)
	}
	if !canLogin(t, handler, wizardSecret) || canLogin(t, handler, envPassword) {
		t.Fatal("the wizard password must stay the only admin password")
	}
}

func TestBootstrapAdminFromFile(t *testing.T) {
	cases := []struct {
		name, content, password string
	}{
		{"no trailing newline", envPassword, envPassword},
		{"one trailing newline", envPassword + "\n", envPassword},
		{"one CRLF", envPassword + "\r\n", envPassword},
		{"only one newline trimmed", envPassword + "\n\n", envPassword + "\n"},
		{"inner and leading spaces kept", "  " + envPassword, "  " + envPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openBootstrapStore(t)
			path := filepath.Join(t.TempDir(), "admin_password")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			setAdminEnv(t, "", path)
			logger, logs := captureLogs()
			if err := bootstrapAdminFromEnv(context.Background(), st, logger); err != nil {
				t.Fatal(err)
			}
			handler := server.New(st, server.NewBroker(), server.Config{})
			if !canLogin(t, handler, tc.password) {
				t.Fatalf("file content %q did not become password %q", tc.content, tc.password)
			}
			if tc.password != envPassword && canLogin(t, handler, envPassword) {
				t.Fatal("more than one trailing newline was trimmed")
			}
			if !strings.Contains(logs.String(), adminPasswordFileEnv) {
				t.Fatalf("log does not name the file variable: %q", logs.String())
			}
			assertNoLeak(t, "log", logs.String(), strings.TrimSpace(tc.password))
			if _, ok := os.LookupEnv(adminPasswordFileEnv); ok {
				t.Fatal("HOMEDEX_ADMIN_PASSWORD_FILE is still in the environment")
			}
		})
	}
}

func TestBootstrapAdminRejectsBothVariables(t *testing.T) {
	st := openBootstrapStore(t)
	path := filepath.Join(t.TempDir(), "admin_password")
	if err := os.WriteFile(path, []byte(wizardSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAdminEnv(t, envPassword, path)
	logger, logs := captureLogs()
	err := bootstrapAdminFromEnv(context.Background(), st, logger)
	if err == nil || !strings.Contains(err.Error(), adminPasswordEnv) || !strings.Contains(err.Error(), adminPasswordFileEnv) {
		t.Fatalf("both variables set: error=%v", err)
	}
	if setupConfigured(t, server.New(st, server.NewBroker(), server.Config{})) {
		t.Fatal("an admin was stored although both variables were set")
	}
	assertNoLeak(t, "error", err.Error(), envPassword, wizardSecret)
	assertNoLeak(t, "log", logs.String(), envPassword, wizardSecret)
}

func TestBootstrapAdminRefusesAWeakPassword(t *testing.T) {
	weak := "tooShort1!"
	weakFile := filepath.Join(t.TempDir(), "weak")
	if err := os.WriteFile(weakFile, []byte(weak+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, password, file, variable string }{
		{"variable", weak, "", adminPasswordEnv},
		{"file", "", weakFile, adminPasswordFileEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openBootstrapStore(t)
			setAdminEnv(t, tc.password, tc.file)
			logger, logs := captureLogs()
			err := bootstrapAdminFromEnv(context.Background(), st, logger)
			if err == nil || !strings.Contains(err.Error(), "at least 12 characters") || !strings.Contains(err.Error(), tc.variable) {
				t.Fatalf("weak password: error=%v", err)
			}
			if setupConfigured(t, server.New(st, server.NewBroker(), server.Config{})) {
				t.Fatal("a weak password was stored")
			}
			assertNoLeak(t, "error", err.Error(), weak)
			assertNoLeak(t, "log", logs.String(), weak)
		})
	}
}

func TestBootstrapAdminRejectsUnusableFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	newlineOnly := filepath.Join(dir, "newline")
	oversized := filepath.Join(dir, "oversized")
	for path, content := range map[string]string{empty: "", newlineOnly: "\n", oversized: strings.Repeat("x", maxAdminPasswordFile+1)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{empty, newlineOnly, oversized, filepath.Join(dir, "missing")} {
		st := openBootstrapStore(t)
		setAdminEnv(t, "", path)
		logger, _ := captureLogs()
		err := bootstrapAdminFromEnv(context.Background(), st, logger)
		if err == nil || !strings.Contains(err.Error(), adminPasswordFileEnv) {
			t.Fatalf("%s: error=%v", filepath.Base(path), err)
		}
		assertNoLeak(t, "error", err.Error(), strings.Repeat("x", 64))
	}
}

func TestBootstrapAdminDoesNothingWhenUnset(t *testing.T) {
	st := openBootstrapStore(t)
	setAdminEnv(t, "", "")
	logger, logs := captureLogs()
	if err := bootstrapAdminFromEnv(context.Background(), st, logger); err != nil {
		t.Fatal(err)
	}
	if setupConfigured(t, server.New(st, server.NewBroker(), server.Config{})) {
		t.Fatal("an admin exists although no bootstrap variable is set")
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected log output: %q", logs.String())
	}
}
