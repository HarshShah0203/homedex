// Package registry checks whether a newer image has been published for the
// tags the inventory's containers run. It asks each image's registry what the
// tag resolves to with anonymous, read-only Registry HTTP API v2 calls: an
// optional token fetch the registry's challenge asks for, then a HEAD of the
// tag's manifest (a GET only when HEAD is refused), reading the
// Docker-Content-Digest header. It never requests layers or blobs, never sends
// credentials, and never reads the database: the engine hands it the
// references to check and compares the answers with what Docker recorded.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/imageref"
)

const (
	defaultTimeout = 10 * time.Second
	// maxImages bounds the distinct tags one scan looks up. Each needs at most
	// four requests (HEAD, token, authenticated HEAD, GET fallback).
	maxImages = 200
	// maxRequests is a hard ceiling on registry and token requests per scan.
	maxRequests = 4*maxImages + 8
	// concurrency bounds simultaneous lookups across every registry.
	concurrency = 4
	maxExclude  = 100
	// maxTokenBytes caps a token response; real anonymous tokens are a few KiB.
	maxTokenBytes     = 64 << 10
	maxChallengeBytes = 4 << 10
	maxRedirects      = 5
	userAgent         = "homedex-registry-check"
	// maxTestRegistries bounds the registries one test pings.
	maxTestRegistries = 3
)

// manifestAccept asks for exactly what `docker pull` stores a digest of: the
// OCI index or Docker manifest list of a multi-arch image, or the single
// manifest of a single-arch one.
var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
}, ", ")

type Connector struct {
	// baseURL maps a registry domain to the scheme and host its API is served
	// from. Tests point every registry at an httptest server.
	baseURL func(domain string) string
	// insecure permits plain-HTTP token services and redirects (tests only).
	insecure  bool
	transport http.RoundTripper
	now       func() time.Time
}

func New() *Connector           { return &Connector{baseURL: defaultBaseURL, now: time.Now} }
func (*Connector) Kind() string { return "registry" }

// defaultBaseURL is where a registry's API lives. Docker Hub's canonical
// domain is docker.io but its API is served from registry-1.docker.io; every
// other registry serves the API on its own name, over HTTPS only.
func defaultBaseURL(domain string) string {
	if domain == "docker.io" {
		return "https://registry-1.docker.io"
	}
	return "https://" + domain
}

type config struct {
	// Images is injected by the engine before every scan: the distinct
	// references of containers in the inventory.
	Images []string `json:"images"`
	// Exclude skips references that start with any of these prefixes, as
	// written or fully qualified (docker.io/library/nginx).
	Exclude        []string `json:"exclude"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

func parse(raw connectors.Config) (config, error) {
	x, err := connectors.DecodeConfig[config](raw)
	if err != nil {
		return x, err
	}
	if x.TimeoutSeconds < 0 || x.TimeoutSeconds > 60 {
		return x, fmt.Errorf("timeout_seconds must be between 1 and 60")
	}
	exclude := make([]string, 0, len(x.Exclude))
	for _, prefix := range x.Exclude {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			exclude = append(exclude, prefix)
		}
	}
	if len(exclude) > maxExclude {
		return x, fmt.Errorf("at most %d exclude prefixes are supported", maxExclude)
	}
	x.Exclude = exclude
	return x, nil
}

func (x config) timeout() time.Duration {
	if x.TimeoutSeconds == 0 {
		return defaultTimeout
	}
	return time.Duration(x.TimeoutSeconds) * time.Second
}

// Validate checks the config and that the registries the containers use
// answer the API base endpoint anonymously. It pings up to three of them at
// once and passes as soon as one answers, the same rule a scan uses: one
// reachable registry is enough for a scan to count.
func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	cfg, err := parse(raw)
	if err != nil {
		return err
	}
	domains := c.testDomains(cfg)
	s := c.session(cfg)
	ctx, cancel := context.WithCancel(ctx)
	var pings sync.WaitGroup
	// Passing early cancels the other pings. Wait for them to finish, so a
	// connection attempt they abandoned is never handed by the shared
	// transport to a later request for the same host, which would then fail
	// with this call's cancellation.
	defer func() {
		cancel()
		pings.Wait()
	}()
	type answer struct {
		i   int
		err error
	}
	answers := make(chan answer, len(domains))
	for i, domain := range domains {
		pings.Add(1)
		go func(i int, domain string) {
			defer pings.Done()
			answers <- answer{i, s.ping(ctx, domain)}
		}(i, domain)
	}
	errs := make([]error, len(domains))
	for range domains {
		a := <-answers
		if a.err == nil {
			return nil
		}
		errs[a.i] = a.err
	}
	if len(errs) == 1 {
		return errs[0]
	}
	messages := make([]string, len(errs))
	for i, err := range errs {
		messages[i] = err.Error()
	}
	return fmt.Errorf("none of the registries your containers use answered: %s", strings.Join(messages, "; "))
}

// testDomains picks the registries a test pings: Docker Hub first when a
// container uses it, then the others by how many tags they serve, at most
// maxTestRegistries. With no containers known yet it is Docker Hub alone.
func (c *Connector) testDomains(cfg config) []string {
	counts := map[string]int{}
	for _, ref := range c.targets(cfg) {
		if parsed, err := imageref.Parse(ref); err == nil && parsed.Digest == "" {
			counts[parsed.Domain]++
		}
	}
	if len(counts) == 0 {
		return []string{"docker.io"}
	}
	domains := make([]string, 0, len(counts))
	for domain := range counts {
		domains = append(domains, domain)
	}
	sort.Slice(domains, func(i, j int) bool {
		a, b := domains[i], domains[j]
		if (a == "docker.io") != (b == "docker.io") {
			return a == "docker.io"
		}
		if counts[a] != counts[b] {
			return counts[a] > counts[b]
		}
		return a < b
	})
	if len(domains) > maxTestRegistries {
		domains = domains[:maxTestRegistries]
	}
	return domains
}

// ping sends one anonymous GET of the registry's API base endpoint. 200 and
// 401 (a challenge) both mean the registry is there.
func (s *session) ping(ctx context.Context, domain string) error {
	res, err := s.do(ctx, http.MethodGet, s.c.baseURL(domain)+"/v2/", "", "application/json")
	if err != nil {
		return fmt.Errorf("registry %s is not reachable: %w", domain, err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusUnauthorized {
		return nil
	}
	return fmt.Errorf("registry %s answered %s", domain, res.Status)
}

// targets returns the references to check: unique, sorted, not excluded.
func (c *Connector) targets(cfg config) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range cfg.Images {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] || excluded(ref, cfg.Exclude) {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func excluded(ref string, prefixes []string) bool {
	qualified := ""
	if parsed, err := imageref.Parse(ref); err == nil {
		qualified = parsed.Key()
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(ref, prefix) || (qualified != "" && strings.HasPrefix(qualified, prefix)) {
			return true
		}
	}
	return false
}

// outcome is one lookup's answer before it becomes a domain.ImageUpdate.
type outcome struct {
	lookup    string
	digest    string
	reason    string
	transient bool
	at        time.Time
}

func unknown(reason string) outcome { return outcome{lookup: imageref.LookupUnknown, reason: reason} }
func transient(reason string) outcome {
	return outcome{lookup: imageref.LookupUnknown, reason: reason, transient: true}
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	cfg, err := parse(raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	refs := c.targets(cfg)
	results := make(map[string]outcome, len(refs))
	// One lookup per tag: "nginx:1.27" and "docker.io/library/nginx:1.27" share it.
	parsedByKey := map[string]imageref.Ref{}
	var keys []string
	keyOf := map[string]string{}
	for _, ref := range refs {
		parsed, e := imageref.Parse(ref)
		switch {
		case errors.Is(e, imageref.ErrImageID):
			results[ref] = unknown("The container names an image ID, not a tag.")
		case e != nil:
			results[ref] = unknown("The image reference could not be parsed.")
		case parsed.Digest != "":
			results[ref] = outcome{lookup: imageref.LookupPinned}
		default:
			key := parsed.Key()
			if _, ok := parsedByKey[key]; !ok {
				parsedByKey[key] = parsed
				keys = append(keys, key)
			}
			keyOf[ref] = key
		}
	}
	sort.Strings(keys)
	answers := make(map[string]outcome, len(keys))
	if len(keys) > maxImages {
		for _, key := range keys[maxImages:] {
			answers[key] = unknown(fmt.Sprintf("Not checked: one scan looks up at most %d image tags.", maxImages))
		}
		keys = keys[:maxImages]
	}
	s := c.session(cfg)
	var mu sync.Mutex
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range jobs {
				answer := s.lookup(ctx, parsedByKey[key])
				answer.at = c.now().UTC()
				mu.Lock()
				answers[key] = answer
				mu.Unlock()
			}
		}()
	}
feed:
	for _, key := range feedOrder(keys, parsedByKey, c.now().Unix()/86400) {
		select {
		case jobs <- key:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return domain.Snapshot{}, ctx.Err()
	}
	// Like a TLS probe whose every target failed: when not one registry
	// answered anything, whether every request failed or the scan's time ran
	// out first, the scan fails, so the source reads as not connected and the
	// previous answers stay, instead of a success that checked nothing.
	if len(keys) > 0 && !s.answeredAny() {
		cause := s.firstFailure()
		if cause == nil {
			cause = errors.New("no registry answered before the scan's time limit")
		}
		return domain.Snapshot{}, fmt.Errorf("no registry could be reached for %s; allow HTTPS egress to the registries your images come from: %w", tagCount(len(keys)), cause)
	}
	now := c.now().UTC()
	var snap domain.Snapshot
	for _, ref := range refs {
		result, ok := results[ref]
		if !ok {
			if result, ok = answers[keyOf[ref]]; !ok {
				// The scan deadline passed before this tag was reached.
				result = transient("The scan timed out before this image was checked.")
			}
		}
		if result.at.IsZero() {
			result.at = now
		}
		snap.ImageUpdates = append(snap.ImageUpdates, domain.ImageUpdate{Ref: ref, Lookup: result.lookup, RemoteDigest: result.digest, Reason: result.reason, Transient: result.transient, CheckedAt: result.at})
	}
	return snap, nil
}

// feedOrder is the order lookups start in. Tags are interleaved by registry,
// so one slow registry cannot hold every worker while the others wait, and
// each registry's list starts at an offset that moves every day, so a scan
// its deadline cuts short does not skip the same tags every time.
func feedOrder(keys []string, parsed map[string]imageref.Ref, day int64) []string {
	byDomain := map[string][]string{}
	var domains []string
	for _, key := range keys {
		d := parsed[key].Domain
		if _, ok := byDomain[d]; !ok {
			domains = append(domains, d)
		}
		byDomain[d] = append(byDomain[d], key)
	}
	sort.Strings(domains)
	longest := 0
	for _, d := range domains {
		list := byDomain[d]
		if n := len(list); n > 1 {
			start := int(day % int64(n))
			if start < 0 {
				start += n
			}
			byDomain[d] = append(append(make([]string, 0, n), list[start:]...), list[:start]...)
		}
		if len(list) > longest {
			longest = len(list)
		}
	}
	out := make([]string, 0, len(keys))
	for i := 0; i < longest; i++ {
		for _, d := range domains {
			if list := byDomain[d]; i < len(list) {
				out = append(out, list[i])
			}
		}
	}
	return out
}

func tagCount(n int) string {
	if n == 1 {
		return "1 image tag"
	}
	return fmt.Sprintf("%d image tags", n)
}

// session is the per-scan state: one HTTP client, cached anonymous tokens,
// registries that answered 429, which hosts answered or failed at the network
// level, and the request budget.
type session struct {
	c        *Connector
	client   *http.Client
	requests atomic.Int32
	mu       sync.Mutex
	tokens   map[string]*tokenEntry
	limited  map[string]bool
	// answered holds every host that returned an HTTP response in this scan.
	answered map[string]bool
	// netFailures counts connections to a host that failed (refused, dropped,
	// timed out, no DNS) while it had not answered anything. From downAfter
	// on, the host is down: not contacted again in this scan, so a firewall
	// that drops packets costs a timeout per worker instead of one per tag,
	// while a single hiccup costs only the one lookup.
	netFailures map[string]int
	down        map[string]error
	// failure is the first request error, to explain a scan nothing answered.
	failure error
}

// downAfter is how many network failures, with no answer in between, mark a
// host down for the rest of a scan.
const downAfter = 2

// skippedError is returned instead of contacting a host that is down.
type skippedError struct{ cause error }

func (e *skippedError) Error() string {
	return "not contacted again after it could not be reached earlier in this scan: " + e.cause.Error()
}
func (e *skippedError) Unwrap() error { return e.cause }

// tokenEntry is one token fetch, shared by every lookup that needs the same
// scope while it is in flight or after it finished.
type tokenEntry struct {
	done  chan struct{}
	token string
	out   outcome
	ok    bool
}

var errBudget = errors.New("request budget for this scan exhausted")

func (c *Connector) session(cfg config) *session {
	transport := c.transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	s := &session{c: c, tokens: map[string]*tokenEntry{}, limited: map[string]bool{}, answered: map[string]bool{}, netFailures: map[string]int{}, down: map[string]error{}}
	s.client = &http.Client{
		Timeout:   cfg.timeout(),
		Transport: transport,
		// Go drops the Authorization header when a redirect leaves the host,
		// so a token only ever reaches the registry that asked for it.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// The redirect itself is an answer from the host that sent it.
			s.markAnswered(via[len(via)-1].URL.Host)
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && !c.insecure {
				return errors.New("registry redirected to a non-HTTPS address")
			}
			return nil
		},
	}
	return s
}

func (s *session) do(ctx context.Context, method, target, token, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	host := req.URL.Host
	if err = s.downError(host); err != nil {
		return nil, err
	}
	if s.requests.Add(1) > maxRequests {
		return nil, errBudget
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		// A request the scan's own deadline or cancellation cut short says
		// nothing about the host.
		if ctx.Err() == nil {
			s.recordFailure(host, err)
		}
		return nil, err
	}
	s.markAnswered(host)
	return res, nil
}

func (s *session) markAnswered(host string) {
	s.mu.Lock()
	s.answered[host] = true
	delete(s.netFailures, host)
	delete(s.down, host)
	s.mu.Unlock()
}

func (s *session) recordFailure(host string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure == nil {
		s.failure = err
	}
	// A host that answered earlier is reachable, only slow or flaky: its other
	// lookups still get their own attempt.
	if !networkFailure(err) || s.answered[host] {
		return
	}
	s.netFailures[host]++
	if _, ok := s.down[host]; !ok && s.netFailures[host] >= downAfter {
		s.down[host] = err
	}
}

func (s *session) downError(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.down[host]; ok {
		return &skippedError{cause: err}
	}
	return nil
}

func (s *session) answeredAny() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.answered) > 0
}

func (s *session) firstFailure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure
}

// networkFailure reports an error of the network itself (a refused, reset or
// dropped connection, a DNS failure, a timeout), as opposed to a host that
// answered but not in a way HTTP accepts (a TLS or protocol error, a refused
// redirect).
func networkFailure(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func (s *session) isLimited(registry string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limited[registry]
}

func (s *session) markLimited(registry string) {
	s.mu.Lock()
	s.limited[registry] = true
	s.mu.Unlock()
}

// lookup resolves one tag to the digest its registry serves for it now.
func (s *session) lookup(ctx context.Context, ref imageref.Ref) outcome {
	if s.isLimited(ref.Domain) {
		return transient("Not checked: the registry rate limited an earlier request in this scan.")
	}
	target := s.c.baseURL(ref.Domain) + "/v2/" + ref.Path + "/manifests/" + url.PathEscape(ref.Tag)
	res, err := s.do(ctx, http.MethodHead, target, "", manifestAccept)
	if err != nil {
		return s.failed(ctx, err)
	}
	res.Body.Close()
	token := ""
	if res.StatusCode == http.StatusUnauthorized {
		// Retry where the request finally landed: a registry may redirect the
		// manifest to another host that issues its own challenge.
		target = res.Request.URL.String()
		var out outcome
		var ok bool
		if token, out, ok = s.token(ctx, res.Header.Get("WWW-Authenticate"), ref); !ok {
			return out
		}
		if res, err = s.do(ctx, http.MethodHead, target, token, manifestAccept); err != nil {
			return s.failed(ctx, err)
		}
		res.Body.Close()
	}
	out, fallback := s.classify(ref, res, true)
	if !fallback {
		return out
	}
	// HEAD was refused or carried no digest. GET returns the same headers (on
	// Docker Hub a GET would count as a pull, but Hub always answers HEAD).
	// The body is never read: the digest must come from the header, because a
	// registry may re-encode a manifest and hashing it would guess.
	if res, err = s.do(ctx, http.MethodGet, target, token, manifestAccept); err != nil {
		return s.failed(ctx, err)
	}
	res.Body.Close()
	out, _ = s.classify(ref, res, false)
	return out
}

func (s *session) failed(ctx context.Context, err error) outcome {
	var skipped *skippedError
	switch {
	case errors.Is(err, errBudget):
		return transient("Not checked: this scan reached its request limit.")
	case errors.As(err, &skipped):
		return transient("Not checked: the registry could not be reached earlier in this scan.")
	case ctx.Err() != nil:
		return transient("The scan timed out before this image was checked.")
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return transient("The registry did not answer within the timeout.")
		}
		return transient("The registry could not be reached.")
	}
}

// classify turns a manifest response into an outcome. fallback reports that
// a HEAD should be retried as GET.
func (s *session) classify(ref imageref.Ref, res *http.Response, head bool) (outcome, bool) {
	switch code := res.StatusCode; {
	case code == http.StatusOK:
		if d := strings.TrimSpace(res.Header.Get("Docker-Content-Digest")); imageref.ValidDigest(d) {
			return outcome{lookup: imageref.LookupResolved, digest: d}, false
		}
		if head {
			return outcome{}, true
		}
		return unknown("The registry returned no valid Docker-Content-Digest header."), false
	case code == http.StatusMethodNotAllowed || code == http.StatusNotImplemented:
		if head {
			return outcome{}, true
		}
		return unknown(fmt.Sprintf("The registry refused the manifest request (%s).", res.Status)), false
	case code == http.StatusUnauthorized:
		return unknown("The registry refused anonymous access: the image is private, or it does not exist there (for example, it was built locally)."), false
	case code == http.StatusForbidden:
		return unknown("The registry denied anonymous access to this image."), false
	case code == http.StatusNotFound:
		return unknown("The registry has no manifest for this tag."), false
	case code == http.StatusTooManyRequests:
		s.markLimited(ref.Domain)
		return transient("The registry rate limited this check (HTTP 429)."), false
	case code >= 500:
		return transient(fmt.Sprintf("The registry answered %s.", res.Status)), false
	default:
		return unknown(fmt.Sprintf("The registry answered %s.", res.Status)), false
	}
}

// token obtains an anonymous pull token as the registry's Bearer challenge
// directs. Only pull scope for the one repository is ever requested, and a
// token is cached for the rest of the scan.
func (s *session) token(ctx context.Context, header string, ref imageref.Ref) (string, outcome, bool) {
	if len(header) > maxChallengeBytes {
		return "", unknown("The registry sent a malformed authentication challenge."), false
	}
	scheme, params, ok := parseChallenge(header)
	switch {
	case !ok || scheme == "":
		return "", unknown("The registry sent a malformed authentication challenge."), false
	case scheme == "basic":
		return "", unknown("The registry requires credentials; private registries are not checked."), false
	case scheme != "bearer":
		return "", unknown("The registry asked for an unsupported authentication scheme."), false
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Host == "" || realm.User != nil || (realm.Scheme != "https" && !(s.c.insecure && realm.Scheme == "http")) {
		return "", unknown("The registry named an invalid token service."), false
	}
	query := realm.Query()
	if service := params["service"]; service != "" {
		query.Set("service", service)
	}
	query.Set("scope", pullScope(params["scope"], ref.Path))
	realm.RawQuery = query.Encode()
	key := realm.String()
	s.mu.Lock()
	entry, hit := s.tokens[key]
	if !hit {
		entry = &tokenEntry{done: make(chan struct{})}
		s.tokens[key] = entry
	}
	s.mu.Unlock()
	if hit {
		select {
		case <-entry.done:
			return entry.token, entry.out, entry.ok
		case <-ctx.Done():
			return "", s.failed(ctx, ctx.Err()), false
		}
	}
	entry.token, entry.out, entry.ok = s.fetchToken(ctx, key, ref)
	close(entry.done)
	return entry.token, entry.out, entry.ok
}

func (s *session) fetchToken(ctx context.Context, target string, ref imageref.Ref) (string, outcome, bool) {
	res, err := s.do(ctx, http.MethodGet, target, "", "application/json")
	if err != nil {
		return "", s.failed(ctx, err), false
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		s.markLimited(ref.Domain)
		return "", transient("The registry's token service rate limited this check (HTTP 429)."), false
	case res.StatusCode >= 500:
		return "", transient(fmt.Sprintf("The registry's token service answered %s.", res.Status)), false
	case res.StatusCode != http.StatusOK:
		return "", unknown("The registry refused anonymous access: the image is private, or it does not exist there (for example, it was built locally)."), false
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, maxTokenBytes)).Decode(&body); err != nil {
		// Usually a response cut short by the timeout or a proxy's error page:
		// a hiccup, not an answer about the image, so the last answer stays.
		return "", transient("The registry's token service returned a malformed response."), false
	}
	token := body.Token
	if token == "" {
		token = body.AccessToken
	}
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", unknown("The registry's token service returned no usable token."), false
	}
	return token, outcome{}, true
}

// pullScope is the token scope to request: the repository the challenge
// names (a redirected manifest may live under another name), but only ever
// with the pull action, whatever else the challenge suggests.
func pullScope(challenge, path string) string {
	parts := strings.Split(challenge, ":")
	if len(parts) == 3 && parts[0] == "repository" && parts[1] != "" && !strings.ContainsAny(parts[1], " \t") {
		return "repository:" + parts[1] + ":pull"
	}
	return "repository:" + path + ":pull"
}

// parseChallenge reads a WWW-Authenticate header such as
// `Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`.
// Quoted values may contain commas and backslash escapes.
func parseChallenge(header string) (string, map[string]string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", nil, false
	}
	scheme, rest, _ := strings.Cut(header, " ")
	params := map[string]string{}
	for {
		rest = strings.TrimLeft(rest, " \t,")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return strings.ToLower(scheme), params, false
		}
		name := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = strings.TrimLeft(rest[eq+1:], " \t")
		var value string
		if strings.HasPrefix(rest, `"`) {
			var b strings.Builder
			closed := false
			i := 1
			for ; i < len(rest); i++ {
				ch := rest[i]
				if ch == '\\' && i+1 < len(rest) {
					i++
					b.WriteByte(rest[i])
					continue
				}
				if ch == '"' {
					closed = true
					break
				}
				b.WriteByte(ch)
			}
			if !closed {
				return strings.ToLower(scheme), params, false
			}
			value, rest = b.String(), rest[i+1:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			value, rest = strings.TrimSpace(rest[:end]), rest[end:]
		}
		params[name] = value
	}
	return strings.ToLower(scheme), params, true
}
