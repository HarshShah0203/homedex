package nginx

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// allocated reports the bytes f allocates, to pin that a small config cannot
// make a scan allocate without bound.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

const allocBudget = 128 << 20

func locationsConf(names, locs int) string {
	var b strings.Builder
	b.WriteString("http { server {\n server_name")
	for i := 0; i < names; i++ {
		fmt.Fprintf(&b, " n%d.example.com", i)
	}
	b.WriteString(";\n")
	for i := 0; i < locs; i++ {
		fmt.Fprintf(&b, " location /l%d { proxy_pass http://app%d:80; }\n", i, i)
	}
	b.WriteString("} }")
	return b.String()
}

func TestNameAndLocationProductIsBounded(t *testing.T) {
	cases := []struct {
		name        string
		names, locs int
		want        string
	}{
		{"names per server", 3000, 300, "nginx: nginx.conf:2: more than 256 names in one server block"},
		{"routes", 256, 300, "more than 20000 routes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"nginx.conf": locationsConf(tc.names, tc.locs)})
			var err error
			if n := allocated(func() {
				_, err = New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
			}); n > allocBudget {
				t.Fatalf("allocated %d bytes", n)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !fileLineErr.MatchString(err.Error()) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestSetExpansionIsBoundedAcrossTheScan(t *testing.T) {
	var b strings.Builder
	b.WriteString("http { server { server_name a.example.com;\n set $a " + strings.Repeat("x", 4000) + ";\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, " set $v%d $a;\n", i)
	}
	b.WriteString("} }")
	root := writeTree(t, map[string]string{"nginx.conf": b.String()})
	var err error
	if n := allocated(func() {
		_, err = New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
	}); n > allocBudget {
		t.Fatalf("allocated %d bytes", n)
	}
	// 4000-byte values: the 263rd expansion (line 265) passes 1 MiB.
	if err == nil || err.Error() != "nginx: nginx.conf:265: variable expansions exceed 1048576 bytes in total" {
		t.Fatalf("got %v", err)
	}
}

// Server-level sets must not be copied into every location: 15k of each would
// be 225M map entries.
func TestServerVariablesAreNotCopiedPerLocation(t *testing.T) {
	var b strings.Builder
	b.WriteString("http { server { server_name a.example.com;\n")
	for i := 0; i < 15000; i++ {
		fmt.Fprintf(&b, "set $v%d 1;\n", i)
	}
	for i := 0; i < 15000; i++ {
		fmt.Fprintf(&b, "location /l%d { return 204; }\n", i)
	}
	b.WriteString("location / { proxy_pass http://$v7:$v1$v4; }\n} }")
	root := writeTree(t, map[string]string{"nginx.conf": b.String()})
	var routes []string
	if n := allocated(func() {
		for _, r := range scan(t, New(), map[string]any{"path": root}) {
			routes = append(routes, fmt.Sprintf("%s %s:%d", r.Key, r.UpstreamHost, r.UpstreamPort))
		}
	}); n > allocBudget {
		t.Fatalf("allocated %d bytes", n)
	}
	if len(routes) != 1 || routes[0] != "nginx:a.example.com:/ 1:11" {
		t.Fatalf("routes %v", routes)
	}
}

func TestLoweredCapsFailWithFileAndLine(t *testing.T) {
	cases := []struct {
		name string
		conf string
		tune func(*limits)
		want string
	}{
		{"names", "http { server {\n server_name a.example.com b.example.com;\n server_name c.example.com;\n} }", func(l *limits) { l.names = 2 }, "nginx: nginx.conf:3: more than 2 names in one server block"},
		{"members", "http {\n upstream pool { server 10.0.0.1; server 10.0.0.2; server 10.0.0.3; }\n}", func(l *limits) { l.members = 2 }, "nginx: nginx.conf:2: more than 2 servers in one upstream"},
		{"routes", "http { server { server_name a.example.com b.example.com;\n location / { proxy_pass http://a:1; }\n location /x { proxy_pass http://b:1; }\n} }", func(l *limits) { l.routes = 3 }, "nginx: nginx.conf:3: more than 3 routes"},
		{"expansion", "http { server { server_name a.example.com;\n set $a 12345;\n set $b $a$a;\n location / { proxy_pass http://$b; }\n} }", func(l *limits) { l.expandBytes = 12 }, "nginx: nginx.conf:4: variable expansions exceed 12 bytes in total"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lim := defaultLimits()
			tc.tune(&lim)
			root := writeTree(t, map[string]string{"nginx.conf": tc.conf})
			_, err := (&Connector{lim: lim}).Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestExtractionHonorsCancellation(t *testing.T) {
	tree := mustParse(t, locationsConf(3, 3))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collect(ctx, tree, defaultLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("collect: %v", err)
	}
	servers, err := collect(context.Background(), tree, defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routes(ctx, servers, "", defaultLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("routes: %v", err)
	}
}
