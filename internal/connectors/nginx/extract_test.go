package nginx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
)

func routesOf(t *testing.T, conf string, extra map[string]any) []domain.Route {
	t.Helper()
	kv := map[string]any{"path": writeTree(t, map[string]string{"nginx.conf": conf})}
	for k, v := range extra {
		kv[k] = v
	}
	return scan(t, New(), kv)
}

// upstreams maps each route key to host:port, and flags TLS with a trailing "+tls".
func upstreams(rs []domain.Route) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		v := fmt.Sprintf("%s:%d", r.UpstreamHost, r.UpstreamPort)
		if r.TLS {
			v += "+tls"
		}
		out[r.Key] = v
	}
	return out
}

func serversOf(t *testing.T, conf string) []server {
	t.Helper()
	ss, err := collect(mustParse(t, conf), defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func scanErr(t *testing.T, conf string) error {
	t.Helper()
	root := writeTree(t, map[string]string{"nginx.conf": conf})
	_, err := New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
	return err
}

func TestVariables(t *testing.T) {
	got := upstreams(routesOf(t, `http { server {
	server_name v.example.com;
	set $app api;
	set $Port 8080;
	location /base { proxy_pass http://$app:$port; }
	location /override { set $app other; proxy_pass http://$app:$port; }
	location /outer {
		set $app outer;
		proxy_pass http://$app:1111;
		location /outer/inner { proxy_pass http://$app:2222; }
	}
	location /braced { proxy_pass http://${APP}:${port}/x; }
	location /chain { set $proto https; set $url $proto://chain; proxy_pass $url; }
	location /iffy { if ($request_method = POST) { set $app posted; } proxy_pass http://$app:1; }
	location /broken { proxy_pass http://$backend; }
	location /bare { proxy_pass $backend; }
	location /runtime { proxy_pass http://app:8080$request_uri; }
	location /runtime2 { proxy_pass http://$app${uri}; }
}}`, nil))
	want := map[string]string{
		"nginx:v.example.com:/base":        "api:8080",
		"nginx:v.example.com:/override":    "other:8080",
		"nginx:v.example.com:/outer":       "outer:1111",
		"nginx:v.example.com:/outer/inner": "api:2222",
		"nginx:v.example.com:/braced":      "api:8080",
		"nginx:v.example.com:/chain":       "chain:443",
		"nginx:v.example.com:/iffy":        "api:1",
		"nginx:v.example.com:/broken":      "$backend:0",
		"nginx:v.example.com:/bare":        "$backend:0",
		"nginx:v.example.com:/runtime":     "app:8080",
		"nginx:v.example.com:/runtime2":    "api:80",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestVariableExpansionIsBounded(t *testing.T) {
	var b strings.Builder
	// 64 bytes doubled seven times is exactly 4096; the eighth doubling (line 9) is over.
	b.WriteString("http { server { server_name a.example.com;\n set $a " + strings.Repeat("x", 64) + ";\n")
	for i := 0; i < 8; i++ {
		b.WriteString(" set $a $a$a;\n")
	}
	b.WriteString("}}")
	if err := scanErr(t, b.String()); err == nil || err.Error() != "nginx: nginx.conf:9: variable expansion longer than 4096 bytes" {
		t.Fatalf("got %v", err)
	}
}

func TestListenAndTLS(t *testing.T) {
	ss := serversOf(t, `http {
	ssl_certificate /etc/ssl/cert.pem;
	server { server_name plain.example.com; }
	server { listen 443 ssl http2; server_name ssl.example.com; }
	server { listen [::]:443 ssl; listen [::]; server_name v6.example.com; }
	server { listen 127.0.0.1:8080; listen 8080; server_name local.example.com; }
	server { listen *:80; listen localhost; server_name star.example.com; }
	server { listen unix:/run/nginx.sock; server_name unix.example.com; }
	server { listen 443 quic reuseport; server_name quic.example.com; }
	server { listen 80; ssl on; server_name legacy.example.com; }
}`)
	type got struct {
		ports     []int
		unix, tls bool
	}
	want := []got{
		{[]int{80}, false, false},
		{[]int{443}, false, true},
		{[]int{80, 443}, false, true},
		{[]int{8080}, false, false},
		{[]int{80}, false, false},
		{nil, true, false},
		{[]int{443}, false, true},
		{[]int{80}, false, true},
	}
	if len(ss) != len(want) {
		t.Fatalf("servers %d", len(ss))
	}
	for i, s := range ss {
		if g := (got{s.ports, s.unix, s.tls}); !reflect.DeepEqual(g, want[i]) {
			t.Fatalf("%s: got %+v want %+v", s.names[0], g, want[i])
		}
	}
	if ss := serversOf(t, "http { ssl on; server { listen 80; } server { listen 80; ssl off; } }"); !ss[0].tls || ss[1].tls {
		t.Fatal("http-level ssl on is the default, server-level ssl off overrides it")
	}
	if err := scanErr(t, "http { server {\n listen 99999;\n} }"); err == nil || err.Error() != "nginx: nginx.conf:2: listen has an invalid port" {
		t.Fatalf("got %v", err)
	}
	if err := scanErr(t, "http { server {\n listen 10.0.0.1:http;\n} }"); err == nil || err.Error() != "nginx: nginx.conf:2: listen has an invalid port" {
		t.Fatalf("got %v", err)
	}
}

func TestServerNames(t *testing.T) {
	ss := serversOf(t, `http {
	server { server_name A.Example.com a.example.com. *.wild.example.com .dot.example.com _ "" "~^(?<x>.+)\.example\.com$" $hostname 192.0.2.10 [fd00::1]; }
	server { server_name first.example.com; server_name second.example.com first.example.com; }
	server { listen 80; }
	server { server_name _ 192.0.2.11; }
}`)
	want := [][]string{
		{"a.example.com", "*.wild.example.com", "dot.example.com", "*.dot.example.com"},
		{"first.example.com", "second.example.com"},
		{"_"},
		{"_"},
	}
	for i, s := range ss {
		if !reflect.DeepEqual(s.names, want[i]) {
			t.Fatalf("server %d names %#v", i, s.names)
		}
	}
}

func TestCatchAllNeedsBaseDomain(t *testing.T) {
	conf := `http {
	server { listen 443 ssl; server_name _; location /app/ { proxy_pass http://app:3000; } }
	server { listen 443 ssl; server_name radarr.* radarr.example.com; location / { proxy_pass http://radarr:7878; } }
}`
	if got := upstreams(routesOf(t, conf, nil)); !reflect.DeepEqual(got, map[string]string{
		"nginx:radarr.*:/":           "radarr:7878+tls",
		"nginx:radarr.example.com:/": "radarr:7878+tls",
	}) {
		t.Fatalf("without base_domain: %v", got)
	}
	routes := routesOf(t, conf, map[string]any{"base_domain": "Example.com."})
	want := []domain.Route{
		{Key: "nginx:_:/app/", Domain: "example.com", PathPrefix: "/app/", UpstreamHost: "app", UpstreamPort: 3000, TLS: true, Status: "unknown"},
		{Key: "nginx:radarr.*:/", Domain: "radarr.example.com", PathPrefix: "/", UpstreamHost: "radarr", UpstreamPort: 7878, TLS: true, Status: "unknown"},
	}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("with base_domain: %#v", routes)
	}
}

func TestLocationsAndProxyPass(t *testing.T) {
	got := upstreams(routesOf(t, `http {
upstream MyGroup { server 10.0.0.31:81; server 10.0.0.32:82 backup; }
upstream empty { server unix:/run/a.sock; server 10.0.0.33 down; }
server {
	server_name loc.example.com;
	location = /health { proxy_pass http://health:1; }
	location =/exact { proxy_pass http://exact:1; }
	location ~^/api/ { proxy_pass http://regex:1; }
	location ~* \.png$ { proxy_pass http://img:1; }
	location ^~ /static/ { proxy_pass http://static:1; }
	location @app { proxy_pass http://named:1; }
	location /int { internal; proxy_pass http://int:1; }
	location /nest { location /nest/child { proxy_pass http://child:1; } }
	location /https { proxy_pass https://secure; }
	location /port { proxy_pass http://p:8081/some/path?x=1; }
	location /v6 { proxy_pass http://[fd00::1]:8080; }
	location /v6bare { proxy_pass https://[fd00::2]/; }
	location /user { proxy_pass http://user:SENTINEL-7f3a@creds:9000; }
	location /sock { proxy_pass http://unix:/run/app.sock:/; }
	location /grp { proxy_pass http://mygroup; }
	location /grpport { proxy_pass http://MyGroup:9999; }
	location /emptygrp { proxy_pass http://empty; }
	location /badport { proxy_pass http://bad:99999; }
	location /noproxy { return 204; }
}}`, nil))
	want := map[string]string{
		"nginx:loc.example.com:= /health":   "health:1",
		"nginx:loc.example.com:= /exact":    "exact:1",
		"nginx:loc.example.com:~ ^/api/":    "regex:1",
		"nginx:loc.example.com:~* \\.png$":  "img:1",
		"nginx:loc.example.com:/static/":    "static:1",
		"nginx:loc.example.com:@app":        "named:1",
		"nginx:loc.example.com:/int":        "int:1",
		"nginx:loc.example.com:/nest/child": "child:1",
		"nginx:loc.example.com:/https":      "secure:443",
		"nginx:loc.example.com:/port":       "p:8081",
		"nginx:loc.example.com:/v6":         "fd00::1:8080",
		"nginx:loc.example.com:/v6bare":     "fd00::2:443",
		"nginx:loc.example.com:/user":       "creds:9000",
		"nginx:loc.example.com:/grp:0":      "10.0.0.31:81",
		"nginx:loc.example.com:/grp:1":      "10.0.0.32:82",
		"nginx:loc.example.com:/grpport":    "MyGroup:9999",
		"nginx:loc.example.com:/badport":    "bad:0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if err := scanErr(t, "http { server {\n location ~~ /x { proxy_pass http://a:1; }\n} }"); err == nil || err.Error() != "nginx: nginx.conf:2: invalid location modifier" {
		t.Fatalf("got %v", err)
	}
	if err := scanErr(t, "http { server {\n location /x {\n  proxy_pass;\n }\n} }"); err == nil || err.Error() != "nginx: nginx.conf:3: proxy_pass expects 1 argument" {
		t.Fatalf("got %v", err)
	}
}

func TestSecondaryAndNestedLocationsFold(t *testing.T) {
	got := upstreams(routesOf(t, `http { server {
	server_name fold.example.com;
	location / { proxy_pass http://app:80; }
	location /sub { proxy_pass http://app; }
	location ~ ^/api { proxy_pass http://APP:80; }
	location = /exact { proxy_pass http://app:80; }
	location @fallback { proxy_pass http://app:80; }
	location /other { proxy_pass http://other:80; location /other/deep { proxy_pass http://other:80; } }
	location /private { internal; proxy_pass http://other:80; }
	location ~ \.cgi$ { proxy_pass http://cgi:80; }
}}`, nil))
	want := map[string]string{
		"nginx:fold.example.com:/":         "app:80",
		"nginx:fold.example.com:/other":    "other:80",
		"nginx:fold.example.com:~ \\.cgi$": "cgi:80",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestKeyCollisions(t *testing.T) {
	got := upstreams(routesOf(t, `http {
	server { listen 80; server_name twin.example.com; location / { proxy_pass http://plain:80; } }
	server { listen 443 ssl; server_name twin.example.com; location / { proxy_pass http://secure:80; } }
	server { listen 80; server_name three.example.com; location / { proxy_pass http://a:80; } }
	server { listen 80; server_name three.example.com; location / { proxy_pass http://b:80; } }
	server { listen 443 ssl; listen 80; server_name three.example.com; location / { proxy_pass http://c:80; } }
	server { listen unix:/run/x.sock; server_name three.example.com; location / { proxy_pass http://d:80; } }
}`, nil))
	want := map[string]string{
		"nginx:twin.example.com:/":       "secure:80+tls",
		"nginx:twin.example.com:/:80":    "plain:80",
		"nginx:three.example.com:/":      "c:80+tls",
		"nginx:three.example.com:/:80":   "a:80",
		"nginx:three.example.com:/:80:2": "b:80",
		"nginx:three.example.com:/:unix": "d:80",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestKeySurvivesMoveAndCertbotEdit(t *testing.T) {
	before := writeTree(t, map[string]string{
		"nginx.conf":    "http { include conf.d/*.conf; }",
		"conf.d/a.conf": "server { listen 80; server_name m.example.com; location / { proxy_pass http://10.0.0.5:80; } }",
	})
	after := writeTree(t, map[string]string{
		"nginx.conf":    "http { include conf.d/*.conf; }",
		"conf.d/z.conf": "server {\n listen 443 ssl;\n server_name m.example.com;\n location / { proxy_pass http://10.0.0.6:80; }\n}",
	})
	a := scan(t, New(), map[string]any{"path": before})
	b := scan(t, New(), map[string]any{"path": after})
	if len(a) != 1 || len(b) != 1 || a[0].Key != "nginx:m.example.com:/" || b[0].Key != a[0].Key || !b[0].TLS {
		t.Fatalf("before %#v after %#v", a, b)
	}
}

func TestCreationOrderDoesNotChangeOutput(t *testing.T) {
	files := []string{"a.conf", "b.conf", "c.conf"}
	build := func(order []string) string {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("http { include conf.d/*.conf; }"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range order {
			body := "server { listen 80; server_name same.example.com; location / { proxy_pass http://" + strings.TrimSuffix(f, ".conf") + ":80; } }"
			if err := os.WriteFile(filepath.Join(root, "conf.d", f), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	a := scan(t, New(), map[string]any{"path": build(files)})
	b := scan(t, New(), map[string]any{"path": build([]string{files[2], files[1], files[0]})})
	if !reflect.DeepEqual(a, b) || len(a) != 3 || a[0].Key != "nginx:same.example.com:/" || a[0].UpstreamHost != "a" {
		t.Fatalf("a %#v\nb %#v", a, b)
	}
}
