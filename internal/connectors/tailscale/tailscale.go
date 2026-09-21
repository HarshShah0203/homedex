// Package tailscale reads a tailnet's device list from the Tailscale API and
// reports each device as a host, so a route upstream given as a tailnet IP or
// MagicDNS name can resolve to the machine behind it. It is read-only: the only
// calls are an OAuth token exchange and one GET of the device list with the
// default field set, which carries no endpoints.
package tailscale

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

const (
	defaultBaseURL = "https://api.tailscale.com"
	readScope      = "devices:core:read"
	tokenPath      = "/api/v2/oauth/token"
	devicesPath    = "/api/v2/tailnet/%s/devices"
	maxTokenBytes  = 64 << 10
)

type Connector struct {
	Client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	tokens map[string]cachedToken
}

// New never follows redirects: a 307/308 would resend the form-encoded client
// secret, or the bearer token, to whatever host the redirect names.
func New() *Connector {
	return &Connector{Client: noRedirectClient(), now: time.Now, tokens: map[string]cachedToken{}}
}

func noRedirectClient() *http.Client {
	client := connectors.Client(connectors.DefaultTimeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func (*Connector) Kind() string { return "tailscale" }

func (c *Connector) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return noRedirectClient()
}

func (c *Connector) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

type config struct {
	Tailnet           string `json:"tailnet"`
	BaseURL           string `json:"base_url"`
	OAuthClientID     string `json:"oauth_client_id"`
	OAuthClientSecret string `json:"oauth_client_secret"`
	APIKey            string `json:"api_key"`
}

func (x config) oauth() bool { return x.OAuthClientID != "" }

// decode validates without ever quoting a credential or URL back: the error
// text reaches the UI and the scan log.
func decode(raw connectors.Config) (config, error) {
	x, err := connectors.DecodeConfig[config](raw)
	if err != nil {
		return config{}, err
	}
	x.Tailnet = strings.TrimSpace(x.Tailnet)
	x.BaseURL = strings.TrimSpace(x.BaseURL)
	x.OAuthClientID = strings.TrimSpace(x.OAuthClientID)
	x.OAuthClientSecret = strings.TrimSpace(x.OAuthClientSecret)
	x.APIKey = strings.TrimSpace(x.APIKey)
	if x.Tailnet == "" {
		x.Tailnet = "-"
	}
	if len(x.Tailnet) > 255 || strings.ContainsAny(x.Tailnet, `/\?#`) || hasSpaceOrControl(x.Tailnet) {
		return config{}, errors.New(`tailnet must be "-" or a tailnet name or ID such as example.com`)
	}
	if x.BaseURL, err = normalizeBaseURL(x.BaseURL); err != nil {
		return config{}, err
	}
	if err = checkCredentials(x); err != nil {
		return config{}, err
	}
	return x, nil
}

func normalizeBaseURL(s string) (string, error) {
	if s == "" {
		return defaultBaseURL, nil
	}
	u, err := url.Parse(s)
	if err != nil || len(s) > 2048 || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("base_url must be an http(s) URL such as https://api.tailscale.com")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("base_url must not contain credentials, a path, a query, or a fragment")
	}
	return u.Scheme + "://" + u.Host, nil
}

func checkCredentials(x config) error {
	hasOAuth := x.OAuthClientID != "" || x.OAuthClientSecret != ""
	hasKey := x.APIKey != ""
	switch {
	case hasOAuth && hasKey:
		return errors.New("configure either an OAuth client or an API access token, not both")
	case hasOAuth && (x.OAuthClientID == "" || x.OAuthClientSecret == ""):
		return errors.New("oauth_client_id and oauth_client_secret are both required")
	case !hasOAuth && !hasKey:
		return errors.New("an OAuth client (oauth_client_id + oauth_client_secret) or an API access token (api_key) is required")
	case strings.HasPrefix(x.APIKey, "tskey-client-"):
		return errors.New("that is an OAuth client secret; enter it as the OAuth client secret together with its client ID")
	case strings.HasPrefix(x.APIKey, "tskey-auth-"):
		return errors.New("that is an auth key for adding devices, not an API access token")
	case strings.HasPrefix(x.OAuthClientSecret, "tskey-api-"):
		return errors.New("that is an API access token; use the API access token field instead")
	}
	for _, f := range []struct {
		name, value string
		max         int
	}{
		{"oauth_client_id", x.OAuthClientID, 256},
		{"oauth_client_secret", x.OAuthClientSecret, 512},
		{"api_key", x.APIKey, 512},
	} {
		if len(f.value) > f.max {
			return fmt.Errorf("%s is too long", f.name)
		}
		if hasSpaceOrControl(f.value) {
			return fmt.Errorf("%s contains whitespace", f.name)
		}
	}
	return nil
}

func hasSpaceOrControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// printable reports whether s is safe to store as a name: bounded, valid
// UTF-8, no control characters. Values failing it are dropped, never trimmed.
func printable(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	x, err := decode(raw)
	if err != nil {
		return err
	}
	_, err = c.devices(ctx, x)
	return err
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	x, err := decode(raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	list, err := c.devices(ctx, x)
	if err != nil {
		return domain.Snapshot{}, err
	}
	return snapshot(list), nil
}

// device allow-lists what is decoded. Keys, the owning user, endpoints,
// routes and tags are never read into memory, let alone stored.
type device struct {
	ID         string   `json:"id"`
	NodeID     string   `json:"nodeId"`
	Name       string   `json:"name"`
	Hostname   string   `json:"hostname"`
	Addresses  []string `json:"addresses"`
	OS         string   `json:"os"`
	LastSeen   string   `json:"lastSeen"`
	Authorized *bool    `json:"authorized"`
	IsExternal bool     `json:"isExternal"`
}

func (c *Connector) devices(ctx context.Context, x config) ([]device, error) {
	tok, err := c.bearer(ctx, x, false)
	if err != nil {
		return nil, err
	}
	list, err := c.fetch(ctx, x, tok)
	var se *connectors.StatusError
	if x.oauth() && errors.As(err, &se) && se.StatusCode == http.StatusUnauthorized {
		// A cached token can be revoked before it expires; one fresh exchange
		// tells that apart from an OAuth client that no longer exists.
		c.forget(x)
		if tok, err = c.bearer(ctx, x, true); err != nil {
			return nil, err
		}
		list, err = c.fetch(ctx, x, tok)
	}
	if err != nil {
		return nil, describe(err, x)
	}
	return list, nil
}

func (c *Connector) fetch(ctx context.Context, x config, tok string) ([]device, error) {
	var body struct {
		Devices *[]device `json:"devices"`
	}
	endpoint := x.BaseURL + fmt.Sprintf(devicesPath, url.PathEscape(x.Tailnet))
	err := connectors.GetJSON(ctx, c.client(), endpoint, &body,
		connectors.WithLabel("Tailscale"), connectors.WithBearerToken(tok), connectors.WithHeader("Accept", "application/json"))
	var se *connectors.StatusError
	var ue *url.Error
	switch {
	case errors.As(err, &se):
		return nil, err
	case errors.As(err, &ue):
		return nil, fmt.Errorf("read Tailscale devices: %w", err)
	case err != nil:
		return nil, fmt.Errorf("decode Tailscale device list: %w", err)
	case body.Devices == nil:
		// A 200 from the wrong endpoint or a captive proxy must not read as an
		// empty tailnet, which would mark every device gone.
		return nil, errors.New("Tailscale response has no devices list")
	}
	return *body.Devices, nil
}

func describe(err error, x config) error {
	var se *connectors.StatusError
	if !errors.As(err, &se) {
		return err
	}
	switch code := se.StatusCode; {
	case code == http.StatusUnauthorized && x.oauth():
		return errors.New("Tailscale rejected the OAuth access token (401 Unauthorized) after a fresh token exchange; confirm the OAuth client still exists")
	case code == http.StatusUnauthorized:
		return errors.New("Tailscale rejected the API access token (401 Unauthorized); it may have expired (access tokens last at most 90 days) or been revoked")
	case code == http.StatusForbidden:
		return fmt.Errorf("Tailscale API returned 403 Forbidden for tailnet %q: the credential needs the %s scope (OAuth client: Devices > Core > Read)", x.Tailnet, readScope)
	case code == http.StatusNotFound:
		return fmt.Errorf("Tailscale API returned 404 Not Found: tailnet %q is not visible to this credential; use \"-\" for the credential's own tailnet", x.Tailnet)
	case code/100 == 3:
		return fmt.Errorf("Tailscale API redirected (%s); Homedex does not follow redirects with credentials, check base_url", statusText(code))
	default:
		return &connectors.StatusError{Label: "Tailscale", StatusCode: code, Status: statusText(code)}
	}
}

// statusText rebuilds the status from its code: the reason phrase in
// res.Status is whatever the upstream chose to send, credentials included.
func statusText(code int) string {
	if text := http.StatusText(code); text != "" {
		return strconv.Itoa(code) + " " + text
	}
	return strconv.Itoa(code)
}

func snapshot(devices []device) domain.Snapshot {
	var s domain.Snapshot
	seen := map[string]bool{}
	for _, d := range devices {
		// A node shared in from another tailnet is not the user's machine, and
		// one awaiting approval is not yet a member; either could otherwise be
		// linked to a machine by name.
		if d.IsExternal || (d.Authorized != nil && !*d.Authorized) {
			continue
		}
		if h, ok := host(d); ok && !seen[h.Key] {
			seen[h.Key] = true
			s.Hosts = append(s.Hosts, h)
		}
	}
	sort.Slice(s.Hosts, func(i, j int) bool { return s.Hosts[i].Key < s.Hosts[j].Key })
	return s
}

func host(d device) (domain.Host, bool) {
	id := strings.TrimSpace(d.NodeID)
	if id == "" {
		id = strings.TrimSpace(d.ID)
	}
	if id == "" || !printable(id, 128) || hasSpaceOrControl(id) {
		return domain.Host{}, false
	}
	var addrs []string
	primary := -1
	for _, raw := range d.Addresses {
		a, ok := parseAddr(raw)
		if !ok {
			continue
		}
		if primary < 0 && a.Is4() {
			primary = len(addrs)
		}
		addrs = append(addrs, a.String())
	}
	// No address means an unreachable node, and an empty address would match
	// the resolver's legacy empty-upstream case.
	if len(addrs) == 0 {
		return domain.Host{}, false
	}
	if primary < 0 {
		primary = 0
	}
	address := addrs[primary]

	fqdn := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d.Name), "."))
	if !printable(fqdn, 253) || hasSpaceOrControl(fqdn) {
		fqdn = ""
	}
	short, _, _ := strings.Cut(fqdn, ".")

	// Android and some appliances report "localhost", which names no machine
	// and would let a proxy at localhost link to this device's machine.
	name := strings.TrimSpace(d.Hostname)
	if !printable(name, 253) || strings.EqualFold(name, "localhost") {
		name = ""
	}
	if name == "" {
		name = short
	}
	if name == "" {
		name = id
	}

	aliasSet := map[string]bool{fqdn: true, short: true}
	for _, a := range addrs {
		aliasSet[a] = true
	}
	delete(aliasSet, "")
	delete(aliasSet, address)
	aliases := make([]string, 0, len(aliasSet))
	for a := range aliasSet {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)

	osName := strings.TrimSpace(d.OS)
	if !printable(osName, 253) {
		osName = ""
	}
	return domain.Host{
		Key:              "tailscale:" + id,
		Name:             name,
		Kind:             domain.HostKindTailscale,
		Address:          address,
		OS:               osName,
		Aliases:          aliases,
		ReportedLastSeen: reportedLastSeen(d.LastSeen),
	}, true
}

// parseAddr accepts a bare address or the CIDR form some API versions use.
func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	a, err := netip.ParseAddr(s)
	if err != nil {
		p, perr := netip.ParsePrefix(s)
		if perr != nil {
			return netip.Addr{}, false
		}
		a = p.Addr()
	}
	return a.Unmap().WithZone(""), true
}

// reportedLastSeen drops the zero and epoch placeholders the API uses for a
// device that has never connected.
func reportedLastSeen(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil || t.Year() < 2000 {
		return nil
	}
	t = t.UTC()
	return &t
}
