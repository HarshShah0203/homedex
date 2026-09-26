package imageref

import (
	"strings"
	"testing"
)

const (
	indexDigest    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	manifestDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	newDigest      = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

func TestParseNormalizesLikeDocker(t *testing.T) {
	cases := map[string]Ref{
		"nginx":                               {Domain: "docker.io", Path: "library/nginx", Tag: "latest"},
		"nginx:1.27-alpine":                   {Domain: "docker.io", Path: "library/nginx", Tag: "1.27-alpine"},
		"traefik/whoami:v1.10.0":              {Domain: "docker.io", Path: "traefik/whoami", Tag: "v1.10.0"},
		"docker.io/library/nginx:1.27":        {Domain: "docker.io", Path: "library/nginx", Tag: "1.27"},
		"index.docker.io/library/nginx:1.27":  {Domain: "docker.io", Path: "library/nginx", Tag: "1.27"},
		"registry-1.docker.io/traefik/whoami": {Domain: "docker.io", Path: "traefik/whoami", Tag: "latest"},
		"ghcr.io/immich-app/immich-server:v1": {Domain: "ghcr.io", Path: "immich-app/immich-server", Tag: "v1"},
		"lscr.io/linuxserver/radarr":          {Domain: "lscr.io", Path: "linuxserver/radarr", Tag: "latest"},
		"registry.lab.example:5000/app:2":     {Domain: "registry.lab.example:5000", Path: "app", Tag: "2"},
		"nginx@" + indexDigest:                {Domain: "docker.io", Path: "library/nginx", Tag: "latest", Digest: indexDigest},
		"nginx:1.27@" + indexDigest:           {Domain: "docker.io", Path: "library/nginx", Tag: "1.27", Digest: indexDigest},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "nginx/UPPER", "a1b2c3d4e5f6", strings.TrimPrefix(indexDigest, "sha256:"), indexDigest, "nginx:bad tag"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestJoinRebuildsStoredReferences(t *testing.T) {
	cases := [][3]string{
		{"traefik/whoami", "v1.10.0", "traefik/whoami:v1.10.0"},
		{"registry.lab.example:5000/app", "", "registry.lab.example:5000/app"},
		{"nginx", indexDigest, "nginx@" + indexDigest},
		{"nginx:1.27", indexDigest, "nginx:1.27@" + indexDigest},
		{"", "latest", ""},
	}
	for _, c := range cases {
		if got := Join(c[0], c[1]); got != c[2] {
			t.Errorf("Join(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestCompareDecidesFromTheSameRepositoryOnly(t *testing.T) {
	resolved := func(d string) Lookup { return Lookup{Outcome: LookupResolved, RemoteDigest: d} }
	cases := []struct {
		name        string
		image, tag  string
		repoDigests []string
		lookup      Lookup
		want        string
	}{
		// A multi-arch pull records the index digest, which is what the tag resolves to.
		{"index digest matches", "nginx", "1.27", []string{"nginx@" + indexDigest}, resolved(indexDigest), UpToDate},
		{"familiar and qualified spellings match", "docker.io/library/nginx", "1.27", []string{"nginx@" + indexDigest}, resolved(indexDigest), UpToDate},
		// Podman records both the index and the instance digest.
		{"any recorded digest matches", "nginx", "1.27", []string{"nginx@" + manifestDigest, "nginx@" + indexDigest}, resolved(indexDigest), UpToDate},
		// A single-arch image compares manifest digests.
		{"single-arch manifest matches", "app/single", "1", []string{"app/single@" + manifestDigest}, resolved(manifestDigest), UpToDate},
		{"tag moved", "nginx", "1.27", []string{"nginx@" + indexDigest}, resolved(newDigest), UpdateAvailable},
		// Digests from another repository say nothing about this tag.
		{"other repository only", "myapp", "1", []string{"alpine@" + indexDigest}, resolved(indexDigest), Unknown},
		{"locally built", "myapp", "dev", nil, resolved(indexDigest), Unknown},
		{"registry did not resolve", "nginx", "1.27", []string{"nginx@" + indexDigest}, Lookup{Outcome: LookupUnknown, Reason: "tag not found"}, Unknown},
		{"malformed remote digest", "nginx", "1.27", []string{"nginx@" + indexDigest}, resolved("sha256:nothex"), Unknown},
		{"pinned by digest", "nginx", indexDigest, []string{"nginx@" + indexDigest}, Lookup{}, Pinned},
		{"image id", "a1b2c3d4e5f6", "", nil, resolved(indexDigest), Unknown},
	}
	for _, c := range cases {
		got := Compare(c.image, c.tag, c.repoDigests, c.lookup)
		if got.Status != c.want {
			t.Errorf("%s: status %q (%s), want %q", c.name, got.Status, got.Reason, c.want)
		}
		if got.Status == Unknown && got.Reason == "" {
			t.Errorf("%s: unknown without a reason", c.name)
		}
	}
	if got := Compare("nginx", "1.27", []string{"nginx@" + indexDigest}, resolved(newDigest)); got.Running != indexDigest {
		t.Fatalf("running digest = %q", got.Running)
	}
}

func TestShortAbbreviatesDigests(t *testing.T) {
	if got := Short(indexDigest); got != "sha256:111111111111" {
		t.Fatalf("Short = %q", got)
	}
	if got := Short("sha256:abc"); got != "sha256:abc" {
		t.Fatalf("Short(short) = %q", got)
	}
	if !ValidDigest(indexDigest) || ValidDigest("sha256:abc") || ValidDigest("md5:"+strings.Repeat("a", 32)) {
		t.Fatal("ValidDigest")
	}
}
