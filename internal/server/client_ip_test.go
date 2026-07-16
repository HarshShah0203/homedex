package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestSpoofedForwardingHeadersCannotBypassLoginLimit(t *testing.T) {
	handler := loginTestHandler(t, TrustedProxySet{})
	for attempt := 0; attempt < 6; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"password":"wrong password"}`))
		req.RemoteAddr = "198.51.100.10:4242"
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(attempt+1))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("attempt %d status=%d, want %d", attempt+1, rec.Code, want)
		}
	}
}

func TestTrustedProxyForwardingSeparatesLoginClients(t *testing.T) {
	trusted, err := ParseTrustedProxies("198.51.100.10/32")
	if err != nil {
		t.Fatal(err)
	}
	handler := loginTestHandler(t, trusted)
	request := func(client string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"password":"wrong password"}`))
		req.RemoteAddr = "198.51.100.10:4242"
		req.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	for range 5 {
		if status := request("203.0.113.1"); status != http.StatusUnauthorized {
			t.Fatalf("first client status=%d, want 401", status)
		}
	}
	if status := request("203.0.113.1"); status != http.StatusTooManyRequests {
		t.Fatalf("limited client status=%d, want 429", status)
	}
	if status := request("203.0.113.2"); status != http.StatusUnauthorized {
		t.Fatalf("second client status=%d, want 401", status)
	}
}

func TestTrustedProxyUsesRightmostUntrustedForwardedAddress(t *testing.T) {
	trusted, err := ParseTrustedProxies("198.51.100.10, 198.51.100.11/32")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.10:4242"
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 192.0.2.5, 198.51.100.11")
	if got := trusted.clientIP(req); got != "192.0.2.5" {
		t.Fatalf("client IP=%q, want rightmost untrusted address", got)
	}
	if _, err = ParseTrustedProxies("not-an-address"); err == nil {
		t.Fatal("invalid trusted proxy was accepted")
	}
}

func TestParsePeerAddrSupportsIPv4AndIPv6(t *testing.T) {
	for input, want := range map[string]string{
		"198.51.100.10:7377":  "198.51.100.10",
		"[2001:db8::10]:7377": "2001:db8::10",
		"2001:db8::10":        "2001:db8::10",
	} {
		addr, ok := parsePeerAddr(input)
		if !ok || addr.String() != want {
			t.Errorf("parsePeerAddr(%q)=%q,%v; want %q,true", input, addr, ok, want)
		}
	}
}

func TestLoginLimiterCleansAndBoundsBuckets(t *testing.T) {
	limiter := newLoginLimiter()
	limiter.maxBuckets = 2
	limiter.maxAttempts = 1
	start := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	if !limiter.allow("one", start) || !limiter.allow("two", start.Add(time.Second)) {
		t.Fatal("initial limiter buckets were unexpectedly blocked")
	}
	if limiter.allow("three", start.Add(2*time.Second)) {
		t.Fatal("overflow bucket did not bound unique-client attempts")
	}
	if len(limiter.buckets) != 2 {
		t.Fatalf("bucket count=%d, want 2", len(limiter.buckets))
	}
	limiter.allow("four", start.Add(2*time.Minute))
	if len(limiter.buckets) != 1 {
		t.Fatalf("expired buckets were not deleted: %d remain", len(limiter.buckets))
	}
}

func loginTestHandler(t *testing.T, trusted TrustedProxySet) http.Handler {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`INSERT INTO settings(key,value) VALUES('admin_password_hash',?)`, hash); err != nil {
		t.Fatal(err)
	}
	return New(st, NewBroker(), Config{TrustedProxies: trusted})
}
