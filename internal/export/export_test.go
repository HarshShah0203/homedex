package export

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/store"
)

func fixtureArchive() Archive {
	hostID := int64(1)
	serviceID := int64(2)
	return Archive{SchemaVersion: SchemaVersion,
		Hosts:    []Host{{ID: hostID, Name: "nas", Kind: "docker", Address: "10.0.0.2", OS: "Linux", Arch: "amd64", State: "active", FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-02T00:00:00Z", Metadata: Metadata{Notes: "main rack"}}},
		Services: []Service{{ID: serviceID, HostID: &hostID, Host: "nas", Name: "photos", Kind: "container", Stack: "media", Image: "ghcr.io/example/photos", Tag: "v1", State: "running", Labels: map[string]string{"com.example.api-token": "top-secret", "purpose": "photo api"}, FirstSeen: "2026-01-01T00:00:00Z", LastSeen: "2026-01-02T00:00:00Z", Metadata: Metadata{Notes: "See photos.example.com and 8.8.8.8", Tags: []store.TagInput{{Name: "media"}}}}},
		Ports:    []Port{{ID: 3, ServiceID: serviceID, Service: "photos", HostID: &hostID, Host: "nas", Number: 2283, ContainerPort: 2283, Protocol: "tcp", Published: true, HostIP: "0.0.0.0", Source: "docker"}},
		Routes:   []Route{{ID: 4, Domain: "photos.example.com", PathPrefix: "/", Proxy: "caddy", UpstreamHost: "10.0.0.2", UpstreamPort: 2283, ResolvedServiceID: &serviceID, Service: "photos", ResolveConfidence: "high", TLS: true, Status: "ok", State: "active"}},
		Expiry:   []Expiry{{EntityType: "domain", ID: 5, Name: "photos.example.com", Kind: "domain", Authority: "Example Registrar", ExpiresAt: "2027-01-01T00:00:00Z", Source: "rdap", State: "active"}},
	}
}

func TestDaysRemainingMarksPartialPastDayExpired(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	days, err := DaysRemaining(now, now.Add(-time.Hour).Format(time.RFC3339Nano))
	if err != nil || days == nil || *days != -1 {
		t.Fatalf("days=%v error=%v, want -1", days, err)
	}
	days, err = DaysRemaining(now, now.Add(24*time.Hour+time.Minute).Format(time.RFC3339Nano))
	if err != nil || days == nil || *days != 2 {
		t.Fatalf("days=%v error=%v, want 2", days, err)
	}
}

func TestGoldenExportsAreDeterministic(t *testing.T) {
	data := Sanitize(fixtureArchive(), Options{IncludePrivate: true})
	jsonBytes, err := JSON(data)
	if err != nil {
		t.Fatal(err)
	}
	csvBytes, err := CSV(data, "services")
	if err != nil {
		t.Fatal(err)
	}
	contextBytes, _ := Context(data)
	cases := map[string][]byte{"markdown.golden": Markdown(data), "services.csv.golden": csvBytes, "inventory.json.golden": jsonBytes, "context.golden": contextBytes}
	for name, got := range cases {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

func TestMandatoryRedactionAndContextBudget(t *testing.T) {
	data := fixtureArchive()
	data.Services[0].Labels["innocent"] = "PASSWORD=hunter2"
	data.Services[0].Labels["credential_auth"] = "opaque-value"
	safe := Sanitize(data, Options{IncludePrivate: true, MaskDomains: true, MaskExternalIPs: true})
	encoded, _ := JSON(safe)
	for _, secret := range []string{"top-secret", "PASSWORD=hunter2", "opaque-value", "photos.example.com", "8.8.8.8"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Errorf("redaction leaked %q: %s", secret, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte(redacted)) || !bytes.Contains(encoded, []byte("domain-001.invalid")) || !bytes.Contains(encoded, []byte("[EXTERNAL-IP]")) {
		t.Fatalf("expected masks missing: %s", encoded)
	}
	large := safe
	for i := 0; i < 5000; i++ {
		x := large.Services[0]
		x.ID = int64(100 + i)
		x.Name = strings.Repeat("service", 20)
		large.Services = append(large.Services, x)
	}
	pack, report := Context(large)
	if len(pack) > ContextLimit {
		t.Fatalf("context size=%d exceeds %d", len(pack), ContextLimit)
	}
	if report.Omitted["services"] == 0 || !bytes.Contains(pack, []byte("Truncation report")) {
		t.Fatalf("missing truncation report: %#v", report)
	}
}

func TestShareExportOmitsPrivateMetadataAndLabels(t *testing.T) {
	safe := Sanitize(fixtureArchive(), Options{IncludePrivate: false})
	b, _ := JSON(safe)
	for _, private := range []string{"main rack", "top-secret", "photo api"} {
		if bytes.Contains(b, []byte(private)) {
			t.Errorf("share export leaked %q", private)
		}
	}
}

func TestCSVViewFiltersMatchInventoryFilters(t *testing.T) {
	data := fixtureArchive()
	published := false
	filtered := Filter(data, FilterOptions{Query: "photos", HostID: 1, State: "running", Published: &published})
	if len(filtered.Services) != 1 || len(filtered.Ports) != 0 || len(filtered.Hosts) != 0 {
		t.Fatalf("unexpected filtered archive: %#v", filtered)
	}
}
