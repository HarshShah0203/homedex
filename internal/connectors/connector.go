package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
)

// DefaultTimeout is the request timeout used by the shared HTTP client. Unlike
// http.DefaultClient (which has no timeout at all), every connector must use a
// client with an explicit deadline so a slow or unresponsive upstream cannot
// hang a scan indefinitely.
const DefaultTimeout = 30 * time.Second

// MaxResponseBytes caps how much of an HTTP JSON response GetJSON will read.
// Connector upstreams are not always administrator-chosen (the RDAP servers in
// particular are discovered from IANA bootstrap data), so a hostile or
// compromised upstream must not be able to stream unbounded JSON and exhaust
// memory. 8 MiB is far larger than any legitimate connector payload.
const MaxResponseBytes = 8 << 20

// Client returns an *http.Client with an explicit timeout. A non-positive
// timeout falls back to DefaultTimeout. Redirect handling is left at the Go
// default (follow up to 10), which is appropriate for a single-admin tool.
func Client(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// StatusError is returned by GetJSON when an upstream responds with a non-2xx
// status. It preserves the raw code so callers can react to specific statuses
// (npm refreshes its token on 401) while its message keeps each connector's
// existing "<Label> API returned <status>" phrasing.
type StatusError struct {
	Label      string
	StatusCode int
	Status     string
}

func (e *StatusError) Error() string {
	if e.Label != "" {
		return fmt.Sprintf("%s API returned %s", e.Label, e.Status)
	}
	return fmt.Sprintf("API returned %s", e.Status)
}

type getOptions struct {
	label      string
	requestFns []func(*http.Request)
}

// Option customizes a GetJSON request.
type Option func(*getOptions)

// WithLabel sets the connector name used in StatusError messages so a non-2xx
// response reads e.g. "Traefik API returned 401 Unauthorized".
func WithLabel(label string) Option {
	return func(o *getOptions) { o.label = label }
}

// WithBasicAuth adds HTTP basic authentication to the request.
func WithBasicAuth(username, password string) Option {
	return func(o *getOptions) {
		o.requestFns = append(o.requestFns, func(r *http.Request) { r.SetBasicAuth(username, password) })
	}
}

// WithHeader sets a single request header.
func WithHeader(key, value string) Option {
	return func(o *getOptions) {
		o.requestFns = append(o.requestFns, func(r *http.Request) { r.Header.Set(key, value) })
	}
}

// WithBearerToken sets an Authorization: Bearer header.
func WithBearerToken(token string) Option {
	return WithHeader("Authorization", "Bearer "+token)
}

// GetJSON issues a GET request with the given context and decodes a 2xx JSON
// response into out. It is the shared, hardened replacement for the connectors'
// previously duplicated get/load implementations: the response body is read
// through an io.LimitReader(MaxResponseBytes) size cap, and callers are expected
// to use a client with an explicit timeout (see Client). A non-2xx response
// yields a *StatusError carrying the status code and the caller's label.
func GetJSON(ctx context.Context, client *http.Client, url string, out any, opts ...Option) error {
	var o getOptions
	for _, fn := range opts {
		fn(&o)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
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
		return &StatusError{Label: o.label, StatusCode: res.StatusCode, Status: res.Status}
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
