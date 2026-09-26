package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
)

// DefaultTimeout is the request timeout used by the shared HTTP client. Unlike
// http.DefaultClient (which has no timeout at all), every connector must use a
// client with an explicit deadline so a slow or unresponsive upstream cannot
// hang a scan indefinitely.
const DefaultTimeout = 30 * time.Second

// MaxResponseBytes caps how much of an HTTP JSON response GetJSON, PostJSON and
// PostForm will read.
// Connector upstreams are not always administrator-chosen (the RDAP servers in
// particular are discovered from IANA bootstrap data), so a hostile or
// compromised upstream must not be able to stream unbounded JSON and exhaust
// memory. 8 MiB is far larger than any legitimate connector payload.
const MaxResponseBytes = 8 << 20

// Client returns an *http.Client with an explicit timeout. A non-positive
// timeout falls back to DefaultTimeout. Redirect handling is left at the Go
// default (follow up to 10), which GetJSON uses only for a request that
// carries no credential: see GetJSON.
func Client(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// StatusError is returned by GetJSON, PostJSON and PostForm when an upstream
// responds with a non-2xx status. It preserves the raw code so callers can
// react to specific statuses (npm refreshes its token on 401) while its message
// keeps each connector's existing "<Label> API returned <status>" phrasing.
type StatusError struct {
	Label      string
	StatusCode int
	Status     string
	// RedirectRefused is set when the status is a redirect the helper would
	// not follow because the request carried a credential or a POST body.
	RedirectRefused bool
}

func (e *StatusError) Error() string {
	msg := "API returned " + e.Status
	if e.Label != "" {
		msg = e.Label + " " + msg
	}
	if e.RedirectRefused {
		msg += "; Homedex does not follow a redirect with credentials, check the URL"
	}
	return msg
}

type requestOptions struct {
	label string
	// credential is set by every option that adds a header, because the
	// helpers cannot tell an API key header from a harmless one.
	credential bool
	requestFns []func(*http.Request)
}

// Option customizes a GetJSON, PostJSON or PostForm request.
type Option func(*requestOptions)

// WithLabel sets the connector name used in StatusError messages so a non-2xx
// response reads e.g. "Traefik API returned 401 Unauthorized".
func WithLabel(label string) Option {
	return func(o *requestOptions) { o.label = label }
}

// WithBasicAuth adds HTTP basic authentication to the request, which then
// never follows a redirect.
func WithBasicAuth(username, password string) Option {
	return func(o *requestOptions) {
		o.credential = true
		o.requestFns = append(o.requestFns, func(r *http.Request) { r.SetBasicAuth(username, password) })
	}
}

// WithHeader sets a single request header. It is applied after the request's
// own headers, so it can also replace the Content-Type PostJSON sets. The
// header is treated as a credential, such as an X-API-Key, so the request
// never follows a redirect.
func WithHeader(key, value string) Option {
	return func(o *requestOptions) {
		o.credential = true
		o.requestFns = append(o.requestFns, func(r *http.Request) { r.Header.Set(key, value) })
	}
}

// WithBearerToken sets an Authorization: Bearer header, so the request never
// follows a redirect.
func WithBearerToken(token string) Option {
	return WithHeader("Authorization", "Bearer "+token)
}

// GetJSON issues a GET request with the given context and decodes a 2xx JSON
// response into out. It is the shared, hardened replacement for the connectors'
// previously duplicated get/load implementations: the response body is read
// through an io.LimitReader(MaxResponseBytes) size cap, and callers are expected
// to use a client with an explicit timeout (see Client). A non-2xx response
// yields a *StatusError carrying the status code and the caller's label.
//
// GetJSON follows redirects as the client does only when no WithBasicAuth,
// WithBearerToken or WithHeader option is given. With any of them it never
// follows one, whatever the client's own policy: Go resends a custom header
// such as X-API-Key to any host a redirect names, and Authorization to the same
// host over plain HTTP. The redirect comes back as a *StatusError with
// RedirectRefused set instead.
func GetJSON(ctx context.Context, client *http.Client, rawURL string, out any, opts ...Option) error {
	return doJSON(ctx, client, http.MethodGet, rawURL, "", nil, out, opts)
}

// PostJSON issues a POST with the given context, sending body encoded as JSON
// (Content-Type: application/json), and decodes a 2xx JSON response into out
// exactly as GetJSON does: bounded by MaxResponseBytes, with a *StatusError
// for any other status. It is for the retrieval a read-only connector needs to
// POST (a login, a GraphQL query), never for a request that changes the
// connected system.
//
// PostJSON never follows a redirect, whatever the client's own policy: a 307
// or 308 would resend the body, which often carries a credential, to whatever
// host the redirect names. The redirect comes back as a *StatusError with
// RedirectRefused set instead.
func PostJSON(ctx context.Context, client *http.Client, rawURL string, body, out any, opts ...Option) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request body: %w", err)
	}
	return doJSON(ctx, client, http.MethodPost, rawURL, "application/json", payload, out, opts)
}

// PostForm is PostJSON for an application/x-www-form-urlencoded body, such as
// an OAuth client-credentials token request. It never follows a redirect
// either.
func PostForm(ctx context.Context, client *http.Client, rawURL string, form url.Values, out any, opts ...Option) error {
	return doJSON(ctx, client, http.MethodPost, rawURL, "application/x-www-form-urlencoded", []byte(form.Encode()), out, opts)
}

// withoutRedirects returns a copy of client that hands a redirect back to the
// caller instead of following it. The copy shares the transport, so it keeps
// the client's timeout, TLS settings and connection pool.
func withoutRedirects(client *http.Client) *http.Client {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// isRedirect reports the statuses Go's client follows.
func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func doJSON(ctx context.Context, client *http.Client, method, rawURL, contentType string, body []byte, out any, opts []Option) error {
	var o requestOptions
	for _, fn := range opts {
		fn(&o)
	}
	// Only an anonymous GET may follow a redirect: a POST body or a
	// credential header would be resent to wherever the redirect points.
	refuseRedirects := method != http.MethodGet || o.credential
	if refuseRedirects {
		client = withoutRedirects(client)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, fn := range o.requestFns {
		fn(req)
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return &StatusError{Label: o.label, StatusCode: res.StatusCode, Status: res.Status,
			RedirectRefused: refuseRedirects && isRedirect(res.StatusCode)}
	}
	return json.NewDecoder(io.LimitReader(res.Body, MaxResponseBytes)).Decode(out)
}

type Config map[string]json.RawMessage

// DecodeConfig converts the connector framework's raw JSON config into a
// connector-specific typed config while preserving JSON decoding errors.
func DecodeConfig[T any](cfg Config) (T, error) {
	var decoded T
	b, err := json.Marshal(cfg)
	if err != nil {
		return decoded, err
	}
	if err = json.Unmarshal(b, &decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

type Connector interface {
	Kind() string
	Validate(context.Context, Config) error
	Scan(context.Context, Config) (domain.Snapshot, error)
}

// ProxyEndpointer is implemented by reverse-proxy connectors whose proxies
// row is not described by a "url" config key (the file-based nginx
// connector). The value is stored in proxies.endpoint; when it parses as a
// URL with a host, that host links the proxy to an inventory host and scopes
// Docker-network resolution exactly as a Traefik/Caddy/NPM url does.
type ProxyEndpointer interface {
	ProxyEndpoint(Config) (string, error)
}

type Registry struct {
	mu         sync.RWMutex
	connectors map[string]Connector
}

func NewRegistry() *Registry { return &Registry{connectors: make(map[string]Connector)} }

func (r *Registry) Register(c Connector) error {
	if c == nil || c.Kind() == "" {
		return fmt.Errorf("connector kind is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connectors[c.Kind()]; exists {
		return fmt.Errorf("connector %q already registered", c.Kind())
	}
	r.connectors[c.Kind()] = c
	return nil
}

func (r *Registry) Get(kind string) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[kind]
	return c, ok
}
