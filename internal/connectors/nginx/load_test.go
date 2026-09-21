package nginx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func loadWith(t *testing.T, lim limits, kv map[string]any) (*loader, []*directive, error) {
	t.Helper()
	s, err := decode(cfgOf(t, kv))
	if err != nil {
		t.Fatal(err)
	}
	return load(context.Background(), s, lim)
}

// firstNames lists each server block's first server_name in tree order.
func firstNames(t *testing.T, tree []*directive) []string {
	t.Helper()
	ss, err := collect(tree, defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range ss {
		out = append(out, s.names[0])
	}
	return out
}

func site(name string) string {
	return "server { server_name " + name + "; location / { proxy_pass http://app:8080; } }\n"
}

func TestRelativeIncludeResolvesAgainstMainConfigDir(t *testing.T) {
	root := writeTree(t, map[string]string{
		"nginx.conf":               "http { include conf.d/*.conf; }",
		"conf.d/a.conf":            "include snippets/app.conf;",
		"snippets/app.conf":        site("right.example.com"),
		"conf.d/snippets/app.conf": site("wrong.example.com"),
	})
	_, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"right.example.com"}) {
		t.Fatalf("got %v", got)
	}
}

func TestGlobIsSortedAndSkipsDotfiles(t *testing.T) {
	root := writeTree(t, map[string]string{"nginx.conf": "http { include conf.d/*.conf; include conf.d/*; }"})
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"z", "m", "a"} {
		if err := os.WriteFile(filepath.Join(root, "conf.d", n+".conf"), []byte(site(n+".example.com")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "conf.d", ".hidden.conf"), []byte("broken { "+sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	_, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.example.com", "m.example.com", "z.example.com", "a.example.com", "m.example.com", "z.example.com"}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestMissingIncludesAreSkippedNotFatal(t *testing.T) {
	root := writeTree(t, map[string]string{"nginx.conf": "http {\n include conf.d/*.conf;\n include missing.conf;\n" + site("a.example.com") + "}"})
	l, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"a.example.com"}) {
		t.Fatalf("got %v", got)
	}
	want := []skip{{file: "nginx.conf", line: 3, pattern: "missing.conf", reason: "not found"}}
	if !reflect.DeepEqual(l.skipped, want) {
		t.Fatalf("skipped %#v", l.skipped)
	}
}

func TestBadIncludePatternNamesFileAndLine(t *testing.T) {
	root := writeTree(t, map[string]string{"nginx.conf": "http {\n include [;\n}"})
	_, _, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err == nil || err.Error() != "nginx: nginx.conf:2: invalid include pattern" {
		t.Fatalf("got %v", err)
	}
}

func TestIncludeArgumentCount(t *testing.T) {
	root := writeTree(t, map[string]string{"nginx.conf": "http {\n include a.conf b.conf;\n}"})
	_, _, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err == nil || err.Error() != "nginx: nginx.conf:2: include expects 1 argument" {
		t.Fatalf("got %v", err)
	}
}

func TestPathMapFollowsAbsoluteIncludes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"nginx.conf":    "http { include /etc/nginx/conf.d/*.conf; }",
		"conf.d/a.conf": site("decoy.example.com"),
		"other/a.conf":  site("longest.example.com"),
	})
	_, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root, "path_map": []string{"/etc/nginx=" + root}})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"decoy.example.com"}) {
		t.Fatalf("single mapping: %v", got)
	}
	_, tree, err = loadWith(t, defaultLimits(), map[string]any{"path": root, "path_map": []string{"/etc/nginx=" + root, "/etc/nginx/conf.d=" + filepath.Join(root, "other")}})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"longest.example.com"}) {
		t.Fatalf("longest prefix: %v", got)
	}
}

func TestUnmappedAbsoluteIncludeWorksWhenMountedAtSamePath(t *testing.T) {
	root := writeTree(t, map[string]string{"plain/one.conf": site("plain.example.com")})
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("http { include "+filepath.Join(root, "plain", "*.conf")+"; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"plain.example.com"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSymlinkOutsideRootsIsNeverRead(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "nginx")
	if err := os.MkdirAll(filepath.Join(base, "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "outside", "evil.conf"), []byte("server { set $x \""+sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("http {\n include sites-enabled/*;\n server { return 444; }\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, "../../outside/evil.conf", filepath.Join(root, "sites-enabled", "evil"))
	symlink(t, "loop", filepath.Join(root, "sites-enabled", "loop"))
	l, _, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	want := []skip{
		{file: "nginx.conf", line: 2, pattern: "sites-enabled/*", reason: "outside the mounted paths"},
		{file: "nginx.conf", line: 2, pattern: "sites-enabled/*", reason: "symlink loop"},
	}
	if !reflect.DeepEqual(l.skipped, want) {
		t.Fatalf("skipped %#v", l.skipped)
	}
	if routes := scan(t, New(), map[string]any{"path": root}); len(routes) != 0 {
		t.Fatalf("routes %#v", routes)
	}
}

func TestSymlinkedGlobDirectoryResolvesThroughMapping(t *testing.T) {
	root := writeTree(t, map[string]string{
		"nginx.conf":    "http { include /etc/nginx/conf.d/*.conf; }",
		"real.d/a.conf": site("linked.example.com"),
	})
	symlink(t, "/etc/nginx/real.d", filepath.Join(root, "conf.d"))
	_, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root, "path_map": []string{"/etc/nginx=" + root}})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"linked.example.com"}) {
		t.Fatalf("got %v", got)
	}
}

func TestIncludeEscapingTheRootIsSkipped(t *testing.T) {
	root := filepath.Join(copyFixture(t, "escape"), "root")
	l, tree, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(t, tree); !reflect.DeepEqual(got, []string{"inside.example.com"}) {
		t.Fatalf("got %v", got)
	}
	want := []skip{
		{file: "nginx.conf", line: 6, pattern: "../outside/*.conf", reason: "outside the mounted paths"},
		{file: "nginx.conf", line: 7, pattern: "../outside/evil.conf", reason: "outside the mounted paths"},
	}
	if !reflect.DeepEqual(l.skipped, want) {
		t.Fatalf("skipped %#v", l.skipped)
	}
}

func TestIncludeCycleNamesTheChain(t *testing.T) {
	root := copyFixture(t, "cycle")
	_, _, err := loadWith(t, defaultLimits(), map[string]any{"path": root})
	want := "nginx: b.conf:3: include cycle: a.conf:2 -> b.conf:3 -> a.conf"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

var fileLineErr = regexp.MustCompile(`^nginx: [^:\s]+:\d+: `)

func TestLimitsNameFileAndLine(t *testing.T) {
	chain := map[string]string{"nginx.conf": "http { include d0.conf; }"}
	blowup := map[string]string{"nginx.conf": "http { include f1.conf; include f1.conf; }"}
	for i := 0; i < 6; i++ {
		chain[fmt.Sprintf("d%d.conf", i)] = fmt.Sprintf("include d%d.conf;", i+1)
		blowup[fmt.Sprintf("f%d.conf", i+1)] = fmt.Sprintf("include f%d.conf;\ninclude f%d.conf;", i+2, i+2)
	}
	three := map[string]string{"nginx.conf": "http {\n include a.conf;\n include b.conf;\n include c.conf;\n}", "a.conf": "#", "b.conf": "#", "c.conf": "#"}
	many := map[string]string{"nginx.conf": "http {\n" + strings.Repeat(" server_name a.example.com;\n", 20) + "}"}
	cases := []struct {
		name  string
		files map[string]string
		tune  func(*limits)
		want  string
	}{
		{"file size", nil, func(l *limits) { l.fileBytes = 1024 }, "nginx: nginx.conf:3: big.conf is larger than 1024 bytes"},
		{"distinct files", three, func(l *limits) { l.files = 2 }, "nginx: nginx.conf:3: more than 2 config files"},
		{"total bytes", three, func(l *limits) { l.totalBytes = 60 }, "nginx: nginx.conf:3: config files exceed 60 bytes in total"},
		{"include depth", chain, func(l *limits) { l.includeDepth = 3 }, "nginx: d1.conf:1: includes nested deeper than 3"},
		{"include expansions", blowup, func(l *limits) { l.includes = 20 }, "more than 20 include expansions"},
		{"directives", many, func(l *limits) { l.directives = 10 }, "nginx: nginx.conf:11: more than 10 directives"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var root string
			if tc.files == nil {
				root = copyFixture(t, "oversized")
			} else {
				root = writeTree(t, tc.files)
			}
			lim := defaultLimits()
			tc.tune(&lim)
			_, err := (&Connector{lim: lim}).Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !fileLineErr.MatchString(err.Error()) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Fatal("error echoes file contents")
			}
		})
	}
}

func TestOversizedFixtureLoadsUnderDefaultLimits(t *testing.T) {
	routes := scan(t, New(), map[string]any{"path": copyFixture(t, "oversized")})
	if len(routes) != 1 || routes[0].Domain != "small.example.com" {
		t.Fatalf("routes %#v", routes)
	}
}

func TestEntryModes(t *testing.T) {
	t.Run("nginx.conf wins", func(t *testing.T) {
		root := writeTree(t, map[string]string{"nginx.conf": "http {" + site("main.example.com") + "}", "other.conf": "broken {"})
		if got := domains(scan(t, New(), map[string]any{"path": root})); !reflect.DeepEqual(got, []string{"main.example.com"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("conf fragments", func(t *testing.T) {
		root := writeTree(t, map[string]string{
			"b.conf":    site("b.example.com"),
			"a.conf":    "upstream pool { server 10.0.0.9:81; }\nserver { server_name a.example.com; location / { proxy_pass http://pool; } }",
			"notes.txt": "broken { " + sentinel,
			".x.conf":   "broken {",
		})
		routes := scan(t, New(), map[string]any{"path": root})
		if got := domains(routes); !reflect.DeepEqual(got, []string{"a.example.com", "b.example.com"}) || routes[0].UpstreamHost != "10.0.0.9" || routes[0].UpstreamPort != 81 {
			t.Fatalf("got %#v", routes)
		}
	})
	t.Run("extensionless sites-enabled", func(t *testing.T) {
		root := writeTree(t, map[string]string{"photos": site("photos.example.com"), "docs": site("docs.example.com")})
		if got := domains(scan(t, New(), map[string]any{"path": root})); !reflect.DeepEqual(got, []string{"docs.example.com", "photos.example.com"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("file path", func(t *testing.T) {
		root := writeTree(t, map[string]string{"main.conf": "http { include conf.d/*.conf; }", "conf.d/a.conf": site("file.example.com")})
		if got := domains(scan(t, New(), map[string]any{"path": filepath.Join(root, "main.conf")})); !reflect.DeepEqual(got, []string{"file.example.com"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("empty directory", func(t *testing.T) {
		root := t.TempDir()
		_, err := New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
		if err == nil || !strings.Contains(err.Error(), "nginx: no nginx.conf or *.conf files in ") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("missing path", func(t *testing.T) {
		err := New().Validate(context.Background(), cfgOf(t, map[string]any{"path": filepath.Join(t.TempDir(), "absent")}))
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("missing path_map target", func(t *testing.T) {
		root := writeTree(t, map[string]string{"nginx.conf": "http {" + site("a.example.com") + "}"})
		err := New().Validate(context.Background(), cfgOf(t, map[string]any{"path": root, "path_map": []string{"/etc/nginx=" + filepath.Join(root, "absent")}}))
		if err == nil || !strings.Contains(err.Error(), "path_map target") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestNoServerBlocksExplainsSkippedIncludes(t *testing.T) {
	root := writeTree(t, map[string]string{"nginx.conf": "http {\n include /elsewhere/sites-enabled/*;\n}"})
	err := New().Validate(context.Background(), cfgOf(t, map[string]any{"path": root}))
	want := "nginx: found no server blocks in " + root + "; 1 include(s) not followed, first at nginx.conf:2 (/elsewhere/sites-enabled/*: outside the mounted paths). Mount nginx's config at the paths nginx uses, or add path_map entries"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	root = writeTree(t, map[string]string{"nginx.conf": "events {}"})
	if _, err = New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root})); err == nil || err.Error() != "nginx: found no server blocks in "+root {
		t.Fatalf("got %v", err)
	}
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := writeTree(t, map[string]string{"nginx.conf": "http {" + site("a.example.com") + "}"})
	if _, err := New().Scan(ctx, cfgOf(t, map[string]any{"path": root})); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestUnreadableFileNamesIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode-000 files")
	}
	root := writeTree(t, map[string]string{"nginx.conf": "http {\n include a.conf;\n}", "a.conf": site("a.example.com")})
	if err := os.Chmod(filepath.Join(root, "a.conf"), 0); err != nil {
		t.Fatal(err)
	}
	_, err := New().Scan(context.Background(), cfgOf(t, map[string]any{"path": root}))
	if err == nil || !strings.Contains(err.Error(), "nginx: nginx.conf:2: cannot read a.conf: permission denied") {
		t.Fatalf("got %v", err)
	}
}
