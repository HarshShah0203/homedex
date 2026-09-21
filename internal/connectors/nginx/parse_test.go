package nginx

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) []*directive {
	t.Helper()
	ds, err := parse([]byte(src), "t.conf", defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestTokenizer(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"double quotes", `set $a "x y";`, []string{"$a", "x y"}},
		{"single quotes", `set $a 'x "y"';`, []string{"$a", `x "y"`}},
		{"escapes", `set $a "q\"s\'b\\t\tn\nr\r";`, []string{"$a", "q\"s'b\\t\tn\nr\r"}},
		{"unknown escape kept", `set $a a\x;`, []string{"$a", `a\x`}},
		{"hash inside token", `server_name a#b;`, []string{"a#b"}},
		{"comment at token start", "server_name a #b;\n c;", []string{"a", "c"}},
		{"braced variable", `set $a ${b}c;`, []string{"$a", "${b}c"}},
		{"braced variable mid token", `set $a x${b};`, []string{"$a", "x${b}"}},
		{"closing brace in bare token", `set $a foo};`, []string{"$a", "foo}"}},
		{"specials inside quotes", `set $a ";{}#";`, []string{"$a", ";{}#"}},
		{"quoted include", `include "/config/nginx/proxy-confs/*.subfolder.conf";`, []string{"/config/nginx/proxy-confs/*.subfolder.conf"}},
		{"empty quoted", `server_name "" '';`, []string{"", ""}},
		{"crlf", "server_name a\r\n\tb;\r\n", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := mustParse(t, tc.src)
			if len(ds) != 1 || !reflect.DeepEqual(ds[0].args, tc.want) {
				t.Fatalf("got %#v", ds)
			}
		})
	}
}

func TestDirectiveLineIsFirstToken(t *testing.T) {
	ds := mustParse(t, "\n\nset\n  $a\n  \"x\ny\";\nserver_name b;")
	if len(ds) != 2 || ds[0].line != 3 || ds[1].line != 7 {
		t.Fatalf("lines: %d %d", ds[0].line, ds[1].line)
	}
}

func TestIfConditionTokenizes(t *testing.T) {
	ds := mustParse(t, "if ($x = \"y\") { return 301; }\nif ($request_method = POST ) {}\nserver_name a;")
	if len(ds) != 3 || ds[0].name != "if" || !ds[0].isBlock || ds[0].block != nil || ds[0].args != nil || ds[2].args[0] != "a" {
		t.Fatalf("got %#v", ds)
	}
}

func TestPruning(t *testing.T) {
	ds := mustParse(t, `
proxy_set_header Authorization "Basic SENTINEL-7f3a";
auth_basic_user_file /config/nginx/.htpasswd;
map $http_x $y { "SENTINEL-7f3a" 1; default 0; }
location /x { proxy_pass http://a:1; }
set $a b;
server_name a;
listen 80 ssl;
upstream u { server 10.0.0.1:80 weight=2; }
include x.conf;
ssl on;
server { listen 81; }
`)
	byName := map[string]*directive{}
	for _, d := range ds {
		byName[d.name] = d
	}
	if byName["proxy_set_header"].args != nil || byName["auth_basic_user_file"].args != nil {
		t.Fatal("secret-bearing args survived")
	}
	if m := byName["map"]; !m.isBlock || m.block != nil || m.args != nil {
		t.Fatalf("map kept: %#v", m)
	}
	for name, want := range map[string][]string{
		"location": {"/x"}, "set": {"$a", "b"}, "server_name": {"a"}, "listen": {"80", "ssl"},
		"upstream": {"u"}, "include": {"x.conf"}, "ssl": {"on"},
	} {
		if !reflect.DeepEqual(byName[name].args, want) {
			t.Fatalf("%s args %#v", name, byName[name].args)
		}
	}
	if l := byName["location"]; len(l.block) != 1 || !reflect.DeepEqual(l.block[0].args, []string{"http://a:1"}) {
		t.Fatalf("location children %#v", l.block)
	}
	if u := byName["upstream"]; len(u.block) != 1 || !reflect.DeepEqual(u.block[0].args, []string{"10.0.0.1:80", "weight=2"}) {
		t.Fatalf("upstream children %#v", u.block)
	}
	if s := byName["server"]; len(s.block) != 1 {
		t.Fatalf("server children %#v", s.block)
	}
	if strings.Contains(fmt.Sprintf("%+v", flatten(ds)), sentinel) {
		t.Fatal("sentinel survived parsing")
	}
}

func flatten(ds []*directive) []directive {
	var out []directive
	for _, d := range ds {
		out = append(out, *d)
		out = append(out, flatten(d.block)...)
	}
	return out
}

func TestMalformedNamesFileAndLineWithoutValues(t *testing.T) {
	cases := []struct {
		file string
		line int
		msg  string
	}{
		{"unbalanced-brace.conf", 6, `unexpected end of file, expecting "}"`},
		{"stray-brace.conf", 3, `unexpected "}"`},
		{"missing-semicolon.conf", 3, `unexpected end of file, expecting ";" or "}"`},
		{"unterminated-quote.conf", 2, "unterminated quoted string"},
		{"after-quote.conf", 2, "unexpected character after quoted string"},
		{"bare-semicolon.conf", 2, `unexpected ";"`},
		{"bare-brace.conf", 2, `unexpected "{"`},
		{"long-token.conf", 2, "token longer than 4096 bytes"},
		{"deep-nesting.conf", 66, "blocks nested deeper than 64"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			b, err := os.ReadFile("testdata/malformed/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), sentinel) {
				t.Fatal("fixture must carry the sentinel")
			}
			_, err = parse(b, tc.file, defaultLimits())
			want := fmt.Sprintf("nginx: %s:%d: %s", tc.file, tc.line, tc.msg)
			if err == nil || err.Error() != want {
				t.Fatalf("got %v, want %s", err, want)
			}
		})
	}
}
