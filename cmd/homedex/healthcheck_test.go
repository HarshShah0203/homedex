package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthURL(t *testing.T) {
	cases := map[string]string{
		":7377":              "http://127.0.0.1:7377/api/health",
		"0.0.0.0:8080":       "http://127.0.0.1:8080/api/health",
		"[::]:7377":          "http://127.0.0.1:7377/api/health",
		"127.0.0.1:17391":    "http://127.0.0.1:17391/api/health",
		"[::1]:7377":         "http://[::1]:7377/api/health",
		"192.168.10.20:7377": "http://192.168.10.20:7377/api/health",
	}
	for listen, want := range cases {
		got, err := healthURL(listen)
		if err != nil || got != want {
			t.Fatalf("healthURL(%q)=%q, %v; want %q", listen, got, err, want)
		}
	}
	for _, invalid := range []string{"7377", "localhost", ":"} {
		if _, err := healthURL(invalid); err == nil {
			t.Fatalf("healthURL(%q) accepted an address without a usable port", invalid)
		}
	}
}

func TestHealthcheckExitCodes(t *testing.T) {
	respond := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/health" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	healthy := respond(http.StatusOK, `{"status":"ok"}`)
	defer healthy.Close()
	failing := respond(http.StatusServiceUnavailable, `{"status":"error"}`)
	defer failing.Close()
	wrongBody := respond(http.StatusOK, `not json`)
	defer wrongBody.Close()

	closed := httptest.NewServer(http.NotFoundHandler())
	closedAddr := closed.Listener.Addr().String()
	closed.Close()

	for _, tc := range []struct {
		name, listen string
		want         int
	}{
		{"healthy", healthy.Listener.Addr().String(), 0},
		{"unhealthy", failing.Listener.Addr().String(), 1},
		{"not a health answer", wrongBody.Listener.Addr().String(), 1},
		{"nothing listening", closedAddr, 1},
	} {
		var stderr bytes.Buffer
		if got := runHealthcheck([]string{"-listen", tc.listen}, &stderr); got != tc.want {
			t.Fatalf("%s: exit=%d want %d (stderr %q)", tc.name, got, tc.want, stderr.String())
		}
	}

	// Without -listen the probe follows HOMEDEX_LISTEN, as the server does.
	_, port, _ := net.SplitHostPort(healthy.Listener.Addr().String())
	t.Setenv("HOMEDEX_LISTEN", ":"+port)
	var stderr bytes.Buffer
	if got := runHealthcheck(nil, &stderr); got != 0 {
		t.Fatalf("HOMEDEX_LISTEN probe exit=%d stderr=%q", got, stderr.String())
	}
	if got := runHealthcheck([]string{"-bogus"}, &stderr); got != 2 || !strings.Contains(stderr.String(), "bogus") {
		t.Fatalf("unknown flag exit=%d stderr=%q", got, stderr.String())
	}
}
