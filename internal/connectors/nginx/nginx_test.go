package nginx

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
)

const sentinel = "SENTINEL-7f3a"

var _ connectors.ProxyEndpointer = (*Connector)(nil)

func cfgOf(t *testing.T, kv map[string]any) connectors.Config {
	t.Helper()
	c := connectors.Config{}
	for k, v := range kv {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		c[k] = b
	}
	return c
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// copyFixture copies testdata/<name> into a temp dir. Git holds no symlinks,
// so tests that need them add them to the copy.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
}

func scan(t *testing.T, c *Connector, kv map[string]any) []domain.Route {
	t.Helper()
	snap, err := c.Scan(context.Background(), cfgOf(t, kv))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Hosts)+len(snap.Services)+len(snap.Ports)+len(snap.Certs)+len(snap.Domains) != 0 {
		t.Fatalf("nginx must report routes only: %+v", snap)
	}
	return snap.Routes
}

func domains(rs []domain.Route) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Domain)
	}
	return out
}

func TestKind(t *testing.T) {
	if New().Kind() != "nginx" {
		t.Fatal("kind")
	}
}

func TestDecodeRejects(t *testing.T) {
	seventeen := make([]string, 17)
	for i := range seventeen {
		seventeen[i] = fmt.Sprintf("/n%d=/m%d", i, i)
	}
	cases := []struct {
		name string
		kv   map[string]any
		want string
	}{
		{"missing path", map[string]any{}, "path is required"},
		{"relative path", map[string]any{"path": "etc/nginx"}, "path must be an absolute path inside the Homedex container"},
		{"root", map[string]any{"path": "/"}, "path must name nginx's config directory, not a system root"},
		{"proc", map[string]any{"path": "/proc/x"}, "path must name nginx's config directory, not a system root"},
		{"dev", map[string]any{"path": "/dev"}, "path must name nginx's config directory, not a system root"},
		{"map without =", map[string]any{"path": "/n", "path_map": []string{"/etc/nginx=/n", "/etc/x"}}, "path_map entry 2 needs NGINX_PATH=HOMEDEX_PATH"},
		{"relative nginx side", map[string]any{"path": "/n", "path_map": []string{"etc/nginx=/n"}}, "path_map entry 1"},
		{"relative homedex side", map[string]any{"path": "/n", "path_map": []string{"/etc/nginx=n"}}, "path_map entry 1"},
		{"target root", map[string]any{"path": "/n", "path_map": []string{"/etc/nginx=/"}}, "path_map entry 1"},
		{"target sys", map[string]any{"path": "/n", "path_map": []string{"/etc/nginx=/sys/x"}}, "path_map entry 1"},
		{"duplicate", map[string]any{"path": "/n", "path_map": []string{"/etc/nginx=/n", "/etc/nginx/=/m"}}, "path_map entry 2"},
		{"too many", map[string]any{"path": "/n", "path_map": seventeen}, "path_map"},
		{"host scheme", map[string]any{"path": "/n", "host": "http://nas"}, "host must be a host name or IP address, without scheme or port"},
		{"host port", map[string]any{"path": "/n", "host": "nas:80"}, "host must be"},
		{"host path", map[string]any{"path": "/n", "host": "nas/x"}, "host must be"},
		{"base wildcard", map[string]any{"path": "/n", "base_domain": "*.example.com"}, "base_domain must be a plain domain such as example.com"},
		{"base leading dot", map[string]any{"path": "/n", "base_domain": ".example.com"}, "base_domain must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := cfgOf(t, tc.kv)
			if _, err := decode(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decode: %v", err)
			}
			if _, err := New().ProxyEndpoint(raw); err == nil {
				t.Fatal("ProxyEndpoint accepted it")
			}
			if err := New().Validate(context.Background(), raw); err == nil {
				t.Fatal("Validate accepted it")
			}
		})
	}
}

func TestDecodeNormalizes(t *testing.T) {
	s, err := decode(cfgOf(t, map[string]any{
		"path":        " /etc/nginx/ ",
		"path_map":    []string{" /etc = /a ", "/etc/nginx/conf.d=/c", "/etc/nginx=/b", "/" + "=/r"},
		"host":        " fd00::1 ",
		"base_domain": " Example.COM. ",
		"unknown":     true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []mapping{{"/etc/nginx/conf.d", "/c"}, {"/etc/nginx", "/b"}, {"/etc", "/a"}, {"/", "/r"}}
	if s.path != "/etc/nginx" || s.host != "fd00::1" || s.baseDomain != "example.com" || !reflect.DeepEqual(s.maps, want) {
		t.Fatalf("got %#v", s)
	}
}

func TestProxyEndpoint(t *testing.T) {
	for _, tc := range []struct {
		kv   map[string]any
		want string
	}{
		{map[string]any{"path": "/etc/nginx", "host": "nas"}, "file://nas/etc/nginx"},
		{map[string]any{"path": "/etc/nginx", "host": "fd00::1"}, "file://[fd00::1]/etc/nginx"},
		{map[string]any{"path": "/etc/nginx"}, "file:///etc/nginx"},
		{map[string]any{"path": "/config/nginx/", "host": "swag", "base_domain": "example.com"}, "file://swag/config/nginx"},
		{map[string]any{"path": "/nginx", "host": "192.0.2.10"}, "file://192.0.2.10/nginx"},
	} {
		got, err := New().ProxyEndpoint(cfgOf(t, tc.kv))
		if err != nil || got != tc.want {
			t.Fatalf("got %q %v, want %q", got, err, tc.want)
		}
	}
}

func debianTree(t *testing.T) string {
	t.Helper()
	root := copyFixture(t, "debian")
	symlink(t, "../sites-available/photos", filepath.Join(root, "sites-enabled", "photos"))
	symlink(t, "/etc/nginx/sites-available/docs", filepath.Join(root, "sites-enabled", "docs"))
	return root
}

func TestDebianLayout(t *testing.T) {
	root := debianTree(t)
	kv := map[string]any{"path": root, "path_map": []string{"/etc/nginx=" + root}}
	routes := scan(t, New(), kv)
	want := []domain.Route{
		{Key: "nginx:docs.example.com:/:0", Domain: "docs.example.com", PathPrefix: "/", UpstreamHost: "10.0.0.21", UpstreamPort: 8080, Status: "unknown"},
		{Key: "nginx:docs.example.com:/:1", Domain: "docs.example.com", PathPrefix: "/", UpstreamHost: "10.0.0.22", UpstreamPort: 80, Status: "unknown"},
		{Key: "nginx:photos.example.com:/", Domain: "photos.example.com", PathPrefix: "/", UpstreamHost: "127.0.0.1", UpstreamPort: 2283, TLS: true, Status: "unknown"},
		{Key: "nginx:photos.example.com:/api/", Domain: "photos.example.com", PathPrefix: "/api/", UpstreamHost: "immich", UpstreamPort: 3001, TLS: true, Status: "unknown"},
		{Key: "nginx:www.docs.example.com:/:0", Domain: "www.docs.example.com", PathPrefix: "/", UpstreamHost: "10.0.0.21", UpstreamPort: 8080, Status: "unknown"},
		{Key: "nginx:www.docs.example.com:/:1", Domain: "www.docs.example.com", PathPrefix: "/", UpstreamHost: "10.0.0.22", UpstreamPort: 80, Status: "unknown"},
	}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("got %#v", routes)
	}
	if again := scan(t, New(), kv); !reflect.DeepEqual(again, routes) {
		t.Fatal("a second scan changed the snapshot")
	}
	if err := New().Validate(context.Background(), cfgOf(t, kv)); err != nil {
		t.Fatal(err)
	}
	l, _, err := loadWith(t, defaultLimits(), kv)
	if err != nil {
		t.Fatal(err)
	}
	if want := []skip{{file: "nginx.conf", line: 16, pattern: "/etc/nginx/mime.types", reason: "not found"}}; !reflect.DeepEqual(l.skipped, want) {
		t.Fatalf("skipped %#v", l.skipped)
	}
	swag := copyFixture(t, "swag")
	for _, r := range append(routes, scan(t, New(), map[string]any{"path": swag, "path_map": []string{"/config/nginx=" + swag}, "base_domain": "example.com"})...) {
		for _, ip := range []string{"127.0.0.1", "10.0.0.", "192.0.2.", "fd00"} {
			if strings.Contains(r.Key, ip) {
				t.Fatalf("key %q carries an address", r.Key)
			}
		}
	}
}

func TestDebianWithoutPathMapExplainsWhy(t *testing.T) {
	root := debianTree(t)
	err := New().Validate(context.Background(), cfgOf(t, map[string]any{"path": root}))
	if err == nil || !strings.Contains(err.Error(), "found no server blocks in "+root+"; 4 include(s) not followed, first at nginx.conf:5 (/etc/nginx/modules-enabled/*.conf: outside the mounted paths)") {
		t.Fatalf("got %v", err)
	}
}

func swagTree(t *testing.T) string {
	t.Helper()
	root := copyFixture(t, "swag")
	if err := os.Chmod(filepath.Join(root, ".htpasswd"), 0); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSwagLayout(t *testing.T) {
	root := swagTree(t)
	kv := map[string]any{"path": root, "path_map": []string{"/config/nginx=" + root}, "host": "swag", "base_domain": "example.com"}
	snap, err := New().Scan(context.Background(), cfgOf(t, kv))
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Route{
		{Key: "nginx:_:/sonarr/", Domain: "example.com", PathPrefix: "/sonarr/", UpstreamHost: "sonarr", UpstreamPort: 8989, TLS: true, Status: "unknown"},
		{Key: "nginx:radarr.*:/", Domain: "radarr.example.com", PathPrefix: "/", UpstreamHost: "radarr", UpstreamPort: 7878, TLS: true, Status: "unknown"},
	}
	if !reflect.DeepEqual(snap.Routes, want) {
		t.Fatalf("got %#v", snap.Routes)
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), sentinel) {
		t.Fatal("snapshot carries a proxy.conf secret")
	}
	l, _, err := loadWith(t, defaultLimits(), kv)
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, s := range l.skipped {
		if s.reason != "outside the mounted paths" {
			t.Fatalf("skip %#v", s)
		}
		patterns = append(patterns, s.pattern)
	}
	wantSkipped := []string{"/etc/nginx/modules/*.conf", "/etc/nginx/conf.d/*.conf", "/etc/nginx/mime.types", "/etc/nginx/http.d/*.conf", "/etc/nginx/fastcgi_params"}
	if !reflect.DeepEqual(patterns, wantSkipped) {
		t.Fatalf("skipped %v", patterns)
	}

	delete(kv, "base_domain")
	if got := scan(t, New(), kv); len(got) != 1 || got[0].Key != "nginx:radarr.*:/" || got[0].Domain != "radarr.*" {
		t.Fatalf("without base_domain: %#v", got)
	}
}

func TestErrorsNeverEchoConfigValues(t *testing.T) {
	root := swagTree(t)
	if err := os.WriteFile(filepath.Join(root, "proxy-confs", "bad.subdomain.conf"), []byte("server {\n listen \"10.0.0.1:"+sentinel+"\";\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root, "path_map": []string{"/config/nginx=" + root}}))
	if err == nil || err.Error() != "nginx: proxy-confs/bad.subdomain.conf:2: listen has an invalid port" {
		t.Fatalf("got %v", err)
	}
}

func TestExampleConfig(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "examples", "connectors", "nginx.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw connectors.Config
	if err = json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if got, err := New().ProxyEndpoint(raw); err != nil || got != "file:///etc/nginx" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestSitesEnabledMountedAloneExplainsLinks(t *testing.T) {
	dir := filepath.Join(debianTree(t), "sites-enabled")
	err := New().Validate(context.Background(), cfgOf(t, map[string]any{"path": dir}))
	if err == nil || !strings.HasSuffix(err.Error(), "; 2 include(s) not followed, first at docs (outside the mounted paths). Mount nginx's config at the paths nginx uses, or add path_map entries") {
		t.Fatalf("got %v", err)
	}
}
