package rdap

import (
	"context"
	"encoding/json"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestScanUsesIANAAndParsesExpiryRegistrarAndNameservers(t *testing.T) {
	var srv *httptest.Server
	var bootstrapRequests, domainRequests atomic.Int32
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dns.json" {
			bootstrapRequests.Add(1)
			_, _ = w.Write([]byte(`{"services":[[["com"],["` + srv.URL + `/"]]]}`))
			return
		}
		domainRequests.Add(1)
		_, _ = w.Write([]byte(`{"events":[{"eventAction":"expiration","eventDate":"2028-01-02T03:04:05Z"}],"nameservers":[{"ldhName":"NS1.EXAMPLE.NET"}],"entities":[{"roles":["registrar"],"vcardArray":["vcard",[["fn",{},"text","Example Registrar"]]]}]}`))
	}))
	defer srv.Close()
	c := New()
	c.BootstrapURL = srv.URL + "/dns.json"
	snap, e := c.Scan(context.Background(), connectors.Config{"domains": json.RawMessage(`["app.example.com","nas.local","10.0.0.2"]`)})
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Domains) != 1 {
		t.Fatalf("domains=%#v", snap.Domains)
	}
	d := snap.Domains[0]
	if d.Name != "example.com" || d.Registrar != "Example Registrar" || d.ExpiresAt == nil || len(d.Nameservers) != 1 {
		t.Fatalf("domain=%#v", d)
	}
	if _, e = c.Scan(context.Background(), connectors.Config{"domains": json.RawMessage(`["example.com"]`)}); e != nil {
		t.Fatal(e)
	}
	if bootstrapRequests.Load() != 1 || domainRequests.Load() != 1 {
		t.Fatalf("cache missed: bootstrap=%d domain=%d", bootstrapRequests.Load(), domainRequests.Load())
	}
}
func TestRegistrableSkipsInternalAndIPs(t *testing.T) {
	for _, v := range []string{"nas.local", "printer.lan", "10.0.0.2", "localhost"} {
		if _, e := registrable(v); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
	if d, e := registrable("photos.example.co.uk"); e != nil || d != "example.co.uk" {
		t.Fatalf("got %q %v", d, e)
	}
}
