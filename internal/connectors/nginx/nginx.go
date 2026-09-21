// Package nginx reads plain nginx and linuxserver SWAG configuration from a
// read-only mount. nginx has no admin API to ask, so the files on disk are the
// only record of which names it proxies where. The connector opens config
// files only, never certificates, keys or password files, and makes no
// network connections.
package nginx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

type Config struct {
	Path       string   `json:"path"`        // nginx.conf, or a config directory
	PathMap    []string `json:"path_map"`    // NGINX_PATH=HOMEDEX_PATH for absolute includes and links
	Host       string   `json:"host"`        // proxy host or container, for the proxies endpoint
	BaseDomain string   `json:"base_domain"` // names SWAG's "app.*" and catch-all servers
}

// limits bound the work one scan can do on an arbitrary mounted tree. The
// input caps alone are not enough: names x locations and repeated variable
// copies multiply a small file, so the output is capped too. Hitting any cap
// fails the scan; nothing is silently truncated.
type limits struct {
	fileBytes, totalBytes int64
	files                 int
	includes              int
	directives            int
	includeDepth          int
	blockDepth            int
	tokenBytes            int
	symlinkHops           int
	varExpansion          int
	names                 int   // per server block
	members               int   // per upstream group
	routes                int   // per scan
	expandBytes           int64 // all variable expansions in a scan
}

func defaultLimits() limits {
	return limits{
		fileBytes:    1 << 20,
		totalBytes:   8 << 20,
		files:        512,
		includes:     4096,
		directives:   100000,
		includeDepth: 16,
		blockDepth:   64,
		tokenBytes:   4096,
		symlinkHops:  40,
		varExpansion: 4096,
		names:        256,
		members:      256,
		routes:       20000,
		expandBytes:  1 << 20,
	}
}

type Connector struct{ lim limits }

func New() *Connector           { return &Connector{lim: defaultLimits()} }
func (*Connector) Kind() string { return "nginx" }

func (c *Connector) bounds() limits {
	if c.lim == (limits{}) {
		return defaultLimits()
	}
	return c.lim
}

type settings struct {
	path, host, baseDomain string
	maps                   []mapping
}

var (
	hostRE        = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)
	baseDomainRE  = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
	errSystemRoot = errors.New("path must name nginx's config directory, not a system root")
)

func systemRoot(p string) bool {
	if p == filepath.Dir(p) {
		return true
	}
	for _, r := range []string{"/proc", "/sys", "/dev"} {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// decode validates the config without touching the filesystem, so
// ProxyEndpoint works even when the mount is missing.
func decode(raw connectors.Config) (settings, error) {
	cfg, err := connectors.DecodeConfig[Config](raw)
	if err != nil {
		return settings{}, err
	}
	var s settings
	p := strings.TrimSpace(cfg.Path)
	if p == "" {
		return s, errors.New("path is required")
	}
	if p = filepath.Clean(p); !filepath.IsAbs(p) {
		return s, errors.New("path must be an absolute path inside the Homedex container")
	}
	if len(p) > 4096 {
		return s, errors.New("path must be at most 4096 bytes")
	}
	if systemRoot(p) {
		return s, errSystemRoot
	}
	s.path = p
	if len(cfg.PathMap) > 16 {
		return s, errors.New("path_map allows at most 16 entries")
	}
	seen := map[string]bool{}
	for i, e := range cfg.PathMap {
		from, to, ok := strings.Cut(e, "=")
		if !ok {
			return s, fmt.Errorf("path_map entry %d needs NGINX_PATH=HOMEDEX_PATH", i+1)
		}
		from, to = filepath.Clean(strings.TrimSpace(from)), filepath.Clean(strings.TrimSpace(to))
		switch {
		case !filepath.IsAbs(from) || !filepath.IsAbs(to):
			return s, fmt.Errorf("path_map entry %d must map an absolute nginx path to an absolute Homedex path", i+1)
		case systemRoot(to):
			return s, fmt.Errorf("path_map entry %d must not map onto a system root", i+1)
		case seen[from]:
			return s, fmt.Errorf("path_map entry %d repeats nginx path %s", i+1, from)
		}
		seen[from] = true
		s.maps = append(s.maps, mapping{from, to})
	}
	sort.Slice(s.maps, func(i, j int) bool {
		a, b := s.maps[i].from, s.maps[j].from
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	if h := strings.TrimSpace(cfg.Host); h != "" {
		if net.ParseIP(h) == nil && !hostRE.MatchString(h) {
			return s, errors.New("host must be a host name or IP address, without scheme or port")
		}
		s.host = h
	}
	if b := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.BaseDomain)), "."); b != "" {
		if !baseDomainRE.MatchString(b) {
			return s, errors.New("base_domain must be a plain domain such as example.com")
		}
		s.baseDomain = b
	}
	return s, nil
}

// routes is the whole scan; Validate runs it too, so a config that would fail
// a scan never passes the Test button.
func (c *Connector) routes(ctx context.Context, raw connectors.Config) ([]domain.Route, error) {
	s, err := decode(raw)
	if err != nil {
		return nil, err
	}
	lim := c.bounds()
	l, tree, err := load(ctx, s, lim)
	if err != nil {
		return nil, err
	}
	servers, err := collect(ctx, tree, lim)
	if err != nil {
		return nil, err
	}
	// An empty result from a broken mount must fail rather than mark every
	// previously seen route as gone.
	if len(servers) == 0 {
		return nil, noServers(s.path, l.skipped)
	}
	return routes(ctx, servers, s.baseDomain, lim)
}

func noServers(path string, skipped []skip) error {
	msg := "nginx: found no server blocks in " + path
	if len(skipped) > 0 {
		f := skipped[0]
		at := f.file + " (" + f.reason + ")"
		if f.line > 0 {
			at = fmt.Sprintf("%s:%d (%s: %s)", f.file, f.line, f.pattern, f.reason)
		}
		msg += fmt.Sprintf("; %d include(s) not followed, first at %s. Mount nginx's config at the paths nginx uses, or add path_map entries", len(skipped), at)
	}
	return errors.New(msg)
}

func (c *Connector) Validate(ctx context.Context, raw connectors.Config) error {
	_, err := c.routes(ctx, raw)
	return err
}

func (c *Connector) Scan(ctx context.Context, raw connectors.Config) (domain.Snapshot, error) {
	rs, err := c.routes(ctx, raw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	return domain.Snapshot{Routes: rs}, nil
}

// ProxyEndpoint names the proxies row: file://<host><path>. A host that
// matches an inventory host or container links the proxy to it.
func (c *Connector) ProxyEndpoint(raw connectors.Config) (string, error) {
	s, err := decode(raw)
	if err != nil {
		return "", err
	}
	host := s.host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: "file", Host: host, Path: filepath.ToSlash(s.path)}).String(), nil
}
