package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type answer struct {
	Valid bool   `json:"valid"`
	Name  string `json:"name"`
}

// requestKinds calls each shared helper against target with the same options,
// so the tests below cover GET, JSON POST and form POST alike.
var requestKinds = map[string]func(ctx context.Context, client *http.Client, target string, out any, opts ...Option) error{
	"GetJSON": GetJSON,
	"PostJSON": func(ctx context.Context, client *http.Client, target string, out any, opts ...Option) error {
		return PostJSON(ctx, client, target, map[string]string{"password": "app-secret"}, out, opts...)
	},
	"PostForm": func(ctx context.Context, client *http.Client, target string, out any, opts ...Option) error {
		return PostForm(ctx, client, target, url.Values{"grant_type": {"client_credentials"}}, out, opts...)
	},
}

func TestPostJSONSendsBodyAndOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "k1" {
			t.Errorf("X-Api-Key %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization %q", got)
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Query != "query Probe { info }" || body.Variables != nil {
			t.Errorf("body %+v, %v", body, err)
		}
		_, _ = io.WriteString(w, `{"valid":true,"name":"nas"}`)
	}))
	defer srv.Close()
	var got answer
	err := PostJSON(context.Background(), Client(time.Second), srv.URL+"/api/auth",
		struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables,omitempty"`
		}{Query: "query Probe { info }"},
		&got, WithLabel("Unraid"), WithHeader("X-API-Key", "k1"), WithBearerToken("tok"))
	if err != nil || !got.Valid || got.Name != "nas" {
		t.Fatalf("PostJSON = %+v, %v", got, err)
	}
}

func TestPostFormSendsForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type %q", got)
		}
		user, pass, ok := r.BasicAuth()
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("scope") != "devices:core:read" || !ok || user != "id" || pass != "secret" {
			t.Errorf("form %v basic %q/%q/%v, %v", r.PostForm, user, pass, ok, err)
		}
		_, _ = io.WriteString(w, `{"valid":true}`)
	}))
	defer srv.Close()
	var got answer
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"devices:core:read"}}
	if err := PostForm(context.Background(), Client(time.Second), srv.URL, form, &got, WithBasicAuth("id", "secret")); err != nil || !got.Valid {
		t.Fatalf("PostForm = %+v, %v", got, err)
	}
}

func TestGetJSONSendsNoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet || len(body) != 0 || r.Header.Get("Content-Type") != "" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("GET sent %s body=%q Content-Type=%q Accept=%q", r.Method, body, r.Header.Get("Content-Type"), r.Header.Get("Accept"))
		}
		_, _ = io.WriteString(w, `{"valid":true}`)
	}))
	defer srv.Close()
	var got answer
	if err := GetJSON(context.Background(), Client(time.Second), srv.URL, &got, WithHeader("Accept", "application/json")); err != nil || !got.Valid {
		t.Fatalf("GetJSON = %+v, %v", got, err)
	}
}

func TestRequestHelpersReportStatusWithoutTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"bad password app-secret"}`)
	}))
	defer srv.Close()
	for name, call := range requestKinds {
		var got answer
		err := call(context.Background(), Client(time.Second), srv.URL, &got, WithLabel("Pi-hole"))
		var se *StatusError
		if !errors.As(err, &se) || se.StatusCode != http.StatusUnauthorized || se.Label != "Pi-hole" {
			t.Fatalf("%s: err = %#v, want a 401 StatusError", name, err)
		}
		if err.Error() != "Pi-hole API returned 401 Unauthorized" {
			t.Errorf("%s: message %q", name, err.Error())
		}
		if err = call(context.Background(), Client(time.Second), srv.URL, &got); err == nil || err.Error() != "API returned 401 Unauthorized" {
			t.Errorf("%s: unlabelled message %v", name, err)
		}
	}
}

func TestRequestHelpersRejectMalformedAndOversizedBodies(t *testing.T) {
	for body, write := range map[string]func(http.ResponseWriter){
		"malformed": func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{"valid":tru`) },
		"html":      func(w http.ResponseWriter) { _, _ = io.WriteString(w, `<html>login</html>`) },
		"oversized": func(w http.ResponseWriter) {
			// A syntactically valid document larger than the cap: only the cap
			// can make it fail.
			_, _ = io.WriteString(w, `{"name":"`)
			chunk := strings.Repeat("a", 64<<10)
			for written := 0; written < MaxResponseBytes+(1<<20); written += len(chunk) {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
			}
			_, _ = io.WriteString(w, `"}`)
		},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { write(w) }))
		for name, call := range requestKinds {
			var got answer
			if err := call(context.Background(), Client(5*time.Second), srv.URL, &got); err == nil {
				t.Errorf("%s: %s body accepted: %+v", name, body, got)
			}
		}
		srv.Close()
	}
}

func TestPostHelpersNeverFollowRedirects(t *testing.T) {
	var landed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed.Add(1)
		_, _ = io.WriteString(w, `{"valid":true}`)
	}))
	defer elsewhere.Close()
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+"/steal", code)
		}))
		// The shared client follows redirects for GETs; the POST helpers must not.
		client := Client(time.Second)
		for _, name := range []string{"PostJSON", "PostForm"} {
			var got answer
			err := requestKinds[name](context.Background(), client, srv.URL, &got, WithLabel("AdGuard Home"))
			var se *StatusError
			if !errors.As(err, &se) || se.StatusCode != code {
				t.Errorf("%s %d: err = %v, want a %d StatusError", name, code, err, code)
			}
		}
		if client.CheckRedirect != nil {
			t.Errorf("%d: the caller's client was changed", code)
		}
		srv.Close()
	}
	if n := landed.Load(); n != 0 {
		t.Fatalf("the redirect target received %d requests", n)
	}
}

func TestRequestHelpersHonourContextCancellation(t *testing.T) {
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	for name, call := range requestKinds {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			var got answer
			done <- call(ctx, Client(30*time.Second), srv.URL, &got)
		}()
		<-arrived
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s: err = %v, want context.Canceled", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: request outlived its context", name)
		}
	}
}

func TestPostJSONRejectsAnUnencodableBodyBeforeSending(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	var got answer
	if err := PostJSON(context.Background(), Client(time.Second), srv.URL, map[string]any{"bad": func() {}}, &got); err == nil {
		t.Fatal("unencodable body accepted")
	}
	if hits.Load() != 0 {
		t.Fatal("request sent despite the encoding error")
	}
}
