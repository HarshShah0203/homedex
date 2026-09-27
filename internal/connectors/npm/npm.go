package npm

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Connector reads Nginx Proxy Manager and its fork NPMplus. Both log in with
// POST /api/tokens and serve the same list endpoints; they differ in where the
// login puts the session, which login() tells apart from the answer itself.
type Connector struct {
	Client   *http.Client
	mu       sync.Mutex
	sessions map[string]session
}

func New() *Connector {
	return &Connector{Client: connectors.Client(connectors.DefaultTimeout), sessions: map[string]session{}}
}
func (*Connector) Kind() string { return "npm" }

type config struct {
	URL      string `json:"url"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func decode(raw connectors.Config) (config, error) {
	x, e := connectors.DecodeConfig[config](raw)
	if x.URL == "" || x.Email == "" || x.Password == "" {
		e = fmt.Errorf("url, email, and password are required")
	}
	return x, e
}

func (x config) base() string { return strings.TrimRight(x.URL, "/") }
func (x config) key() string  { return x.base() + "|" + x.Email }

// session is how the reads after a login authenticate. NPM returns a JWT in
// the login body and reads send it as a bearer token. NPMplus never puts it
// in the body: it sets a signed HttpOnly cookie, and reads only look at that
// cookie, never at an Authorization header.
type session struct {
	bearer string
	cookie *http.Cookie
}

func (s session) auth() connectors.Option {
	if s.cookie != nil {
		return connectors.WithCookie(s.cookie)
	}
	return connectors.WithBearerToken(s.bearer)
}

// refused reports whether a read's status says the session is no longer
// accepted: its JWT expired (after a day on NPM and NPMplus releases up to
// 2026-07-24, an hour on later NPMplus), or NPMplus restarted without a fixed
// COOKIE_SECRET and can no longer check its cookie. NPM and current NPMplus
// answer 401; NPMplus releases up to 2026-07-24 answer 403, the same status
// as a missing permission, so for a cookie a 403 earns one new login too.
func (s session) refused(status int) bool {
	return status == http.StatusUnauthorized || (s.cookie != nil && status == http.StatusForbidden)
}

// The cookies NPMplus's POST /api/tokens sets: the session, or, after the
// 2026-07-24 release, a three-minute challenge when the account has a second
// factor, which only POST /api/tokens/totp with a current code turns into a
// session. That release and NPM answer a second factor in the body instead.
const (
	plusSessionCookie   = "__Host-Http-token"
	plusChallengeCookie = "__Host-Http-challenge_token"
)

var errSecondFactor = errors.New("this account has two-factor authentication (TOTP) turned on, and Homedex cannot enter a code on every scan; give Homedex its own account without two-factor authentication")

// certExpiryLayouts are tried in order against a certificate's expires_on
// value. NPM's API returns a space-separated timestamp ("2025-08-01 00:00:00"),
// not RFC3339, so a single RFC3339 parse silently failed and every NPM-managed
// certificate's not_after was stored as the zero time.
var certExpiryLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// parseExpiry returns the first layout that parses s, or the zero time if none
// match (callers leave not_after unset rather than crashing).
func parseExpiry(s string) time.Time {
	for _, layout := range certExpiryLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// login posts the credentials and returns the session the answer carries: a
// token in the body is NPM, no token but the session cookie is NPMplus.
func (c *Connector) login(ctx context.Context, x config) (session, error) {
	var v struct {
		Token string `json:"token"`
		// A second factor: NPMplus after 2026-07-24 answers requiresTotp and
		// its challenge cookie, NPM and earlier NPMplus answer requires_2fa
		// and a challenge token.
		RequiresTotp bool `json:"requiresTotp"`
		Requires2FA  bool `json:"requires_2fa"`
	}
	var cookies []*http.Cookie
	// The body carries the NPM password, so PostJSON follows a redirect with
	// it only to the same host over HTTPS, and bounds the answer like every
	// other request.
	e := connectors.PostJSON(ctx, c.Client, x.base()+"/api/tokens",
		map[string]string{"identity": x.Email, "secret": x.Password}, &v,
		connectors.WithLabel("NPM token"), connectors.WithResponseCookies(&cookies))
	if e != nil {
		return session{}, explainLogin(e)
	}
	if v.Token != "" {
		return session{bearer: v.Token}, nil
	}
	challenged := v.RequiresTotp || v.Requires2FA
	for _, ck := range cookies {
		switch {
		case ck.Name == plusSessionCookie && ck.Value != "":
			// Only the name and value go back; the attributes are the browser's.
			return session{cookie: &http.Cookie{Name: ck.Name, Value: ck.Value}}, nil
		case ck.Name == plusChallengeCookie:
			challenged = true
		}
	}
	if challenged {
		return session{}, errSecondFactor
	}
	return session{}, errors.New("NPM token API answered without a token or an NPMplus session cookie")
}

// explainLogin adds what a failed login most often means. NPM refuses a wrong
// email or password with 401, NPMplus with 400 (releases up to 2026-07-24) or
// 403 (later), and NPMplus refuses every password login the same way while
// OIDC_DISABLE_PASSWORD is true. It answers 429 once one address has failed
// ten (later five) logins within five minutes, and serves its admin port with
// a self-signed certificate by default. The upstream's own error text is never
// included.
func explainLogin(e error) error {
	var se *connectors.StatusError
	var untrusted *tls.CertificateVerificationError
	switch {
	case errors.As(e, &se) && (se.StatusCode == http.StatusBadRequest || se.StatusCode == http.StatusForbidden):
		return fmt.Errorf("%w: check the email and password; NPMplus also refuses every password login while OIDC_DISABLE_PASSWORD is true", e)
	case errors.As(e, &se) && se.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: NPMplus refuses logins from this address for a few minutes after repeated failures; check the email and password, then test again later", e)
	case errors.As(e, &untrusted):
		return fmt.Errorf("%w; if this is NPMplus, its admin port uses a self-signed certificate unless DEFAULT_CERT_ID names a trusted one: set it, or point the URL at a proxy host with a trusted certificate that forwards to the admin port", e)
	}
	return e
}

// session returns the cached session for x, logging in when there is none.
func (c *Connector) session(ctx context.Context, x config) (session, error) {
	c.mu.Lock()
	s, ok := c.sessions[x.key()]
	c.mu.Unlock()
	if ok {
		return s, nil
	}
	return c.fresh(ctx, x)
}

// fresh logs in and caches the session, replacing any cached one.
func (c *Connector) fresh(ctx context.Context, x config) (session, error) {
	s, e := c.login(ctx, x)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessions == nil {
		c.sessions = map[string]session{}
	}
	if e != nil {
		delete(c.sessions, x.key())
		return session{}, e
	}
	c.sessions[x.key()] = s
	return s, nil
}

// get reads path with the cached session. When the answer says the session
// is no longer accepted it logs in once more and retries once.
func (c *Connector) get(ctx context.Context, x config, path string, out any) error {
	s, e := c.session(ctx, x)
	if e != nil {
		return e
	}
	e = connectors.GetJSON(ctx, c.Client, x.base()+path, out, connectors.WithLabel("NPM"), s.auth())
	var se *connectors.StatusError
	if errors.As(e, &se) && s.refused(se.StatusCode) {
		if s, e = c.fresh(ctx, x); e != nil {
			return e
		}
		e = connectors.GetJSON(ctx, c.Client, x.base()+path, out, connectors.WithLabel("NPM"), s.auth())
	}
	if errors.As(e, &se) && se.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: the account needs view access to Proxy Hosts and Certificates", e)
	}
	return e
}

// Validate always logs in, so a changed password is tested rather than
// answered from the cache.
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	x, e := decode(raw)
	if e != nil {
		return e
	}
	_, e = c.fresh(ctx, x)
	return e
}

// flag decodes an enabled field. NPM before 2.12 sends 0 or 1; NPM 2.12 and
// later and NPMplus send true or false.
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	v, err := strconv.ParseBool(string(b))
	if err != nil {
		return fmt.Errorf("enabled is %s, want true, false, 1 or 0", b)
	}
	*f = flag(v)
	return nil
}

// upstream is where a proxy host or location forwards to. NPMplus's "path"
// scheme serves the files in the directory forward_host names (forward_port
// is then a PHP version) and "empty" serves nothing, so neither has one.
func upstream(scheme, host string, port int) (string, int) {
	if scheme == "path" || scheme == "empty" {
		return "", 0
	}
	return host, port
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	x, e := decode(raw)
	if e != nil {
		return domain.Snapshot{}, e
	}
	var hs []struct {
		ID            int      `json:"id"`
		DomainNames   []string `json:"domain_names"`
		ForwardHost   string   `json:"forward_host"`
		ForwardPort   int      `json:"forward_port"`
		ForwardScheme string   `json:"forward_scheme"`
		CertificateID int      `json:"certificate_id"`
		Enabled       flag     `json:"enabled"`
		Locations     []struct {
			Path          string `json:"path"`
			ForwardScheme string `json:"forward_scheme"`
			ForwardHost   string `json:"forward_host"`
			ForwardPort   int    `json:"forward_port"`
			// NPMplus can switch a single location off; NPM has no such field.
			Enabled *flag `json:"npmplus_enabled"`
		} `json:"locations"`
	}
	if e = c.get(ctx, x, "/api/nginx/proxy-hosts", &hs); e != nil {
		return domain.Snapshot{}, e
	}
	var cs []struct {
		ID          int
		DomainNames []string `json:"domain_names"`
		ExpiresOn   string   `json:"expires_on"`
		Provider    string
	}
	_ = c.get(ctx, x, "/api/nginx/certificates", &cs)
	var s domain.Snapshot
	for _, h := range hs {
		if !h.Enabled {
			continue
		}
		host, port := upstream(h.ForwardScheme, h.ForwardHost, h.ForwardPort)
		for _, d := range h.DomainNames {
			status := "unknown"
			s.Routes = append(s.Routes, domain.Route{Key: fmt.Sprintf("npm:%d:%s", h.ID, d), Domain: d, PathPrefix: "/", UpstreamHost: host, UpstreamPort: port, TLS: h.CertificateID > 0, Status: status})
			for _, l := range h.Locations {
				if l.Enabled != nil && !*l.Enabled {
					continue
				}
				lhost, lport := upstream(l.ForwardScheme, l.ForwardHost, l.ForwardPort)
				s.Routes = append(s.Routes, domain.Route{Key: fmt.Sprintf("npm:%d:%s:%s", h.ID, d, l.Path), Domain: d, PathPrefix: l.Path, UpstreamHost: lhost, UpstreamPort: lport, TLS: h.CertificateID > 0, Status: status})
			}
		}
	}
	for _, z := range cs {
		t := parseExpiry(z.ExpiresOn)
		for _, d := range z.DomainNames {
			endpoint := d + ":443"
			s.Certs = append(s.Certs, domain.Cert{Key: "tls:" + endpoint, Subject: d, SANs: z.DomainNames, NotAfter: t, Source: "proxy", Endpoint: endpoint})
		}
	}
	return s, nil
}
