package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestConcurrentSetupReturnsSuccessAndConflict(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(st, NewBroker(), Config{})
	body := []byte(`{"password":"correct horse battery staple"}`)

	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			statuses <- rec.Code
		}()
	}
	wg.Wait()
	close(statuses)
	got := map[int]int{}
	for status := range statuses {
		got[status]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusConflict] != 1 {
		t.Fatalf("setup statuses=%v, want one 200 and one 409", got)
	}
}

func TestHealthVersionAndListEndpoints(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := New(st, NewBroker(), Config{Version: "v0.1-test", NoAuth: true})
	for path, status := range map[string]int{"/api/health": 200, "/api/version": 200, "/api/services": 200, "/api/hosts": 200, "/api/ports": 200, "/api/routes": 200, "/api/certs": 200, "/api/domains": 200, "/api/changes": 200} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Errorf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var body struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Items == nil || body.Total != 0 {
		t.Fatalf("unexpected list response: %s", rec.Body.String())
	}
}

type apiFixtureConnector struct{}

func (apiFixtureConnector) Kind() string                                      { return "fixture" }
func (apiFixtureConnector) Validate(context.Context, connectors.Config) error { return nil }
func (apiFixtureConnector) Scan(context.Context, connectors.Config) (domain.Snapshot, error) {
	return domain.Snapshot{Hosts: []domain.Host{{Key: "host", Name: "nas", Kind: "docker"}}, Services: []domain.Service{{Key: "svc", HostKey: "host", Name: "jellyfin", Stack: "media"}}, Ports: []domain.Port{{ServiceKey: "svc", HostKey: "host", Number: 8096, ContainerPort: 8096, Published: true, Protocol: "tcp"}}}, nil
}
func TestConnectorCRUDScanSearchAndPortHelpers(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{4}, 32))
	configs := store.NewConnectorConfigs(st, box)
	reg := connectors.NewRegistry()
	_ = reg.Register(apiFixtureConnector{})
	runner := engine.NewRunner(st, configs, reg, engine.New(st, nil))
	h := New(st, NewBroker(), Config{NoAuth: true, ConnectorConfigs: configs, Registry: reg, Runner: runner})
	body := bytes.NewBufferString(`{"kind":"fixture","name":"Fixture lab","config":{"token":"secret"},"schedule_minutes":15}`)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/connectors", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || bytes.Contains(rec.Body.Bytes(), []byte("secret")) || bytes.Contains(rec.Body.Bytes(), []byte("config")) {
		t.Fatalf("connector response leaks config: %s", rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/connectors/1", bytes.NewBufferString(`{"schedule_minutes":30}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("metadata patch: %d %s", rec.Code, rec.Body.String())
	}
	var preserved map[string]string
	if e = configs.Load(ctx, 1, &preserved); e != nil || preserved["token"] != "secret" {
		t.Fatalf("preserved config=%v error=%v", preserved, e)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/search?q=jelly", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte("jellyfin")) {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/search?q=8096", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"entity_type":"port"`)) {
		t.Fatalf("port search: %s", rec.Body.String())
	}
	var hostID, svcID int64
	_ = st.DB().QueryRow(`SELECT id FROM hosts WHERE natural_key='host'`).Scan(&hostID)
	_ = st.DB().QueryRow(`SELECT id FROM services WHERE natural_key='svc'`).Scan(&svcID)
	_, _ = st.DB().Exec(`INSERT INTO services(connector_id,host_id,name,kind,state,first_seen,last_seen,natural_key,created_at,updated_at) SELECT connector_id,host_id,'other','container','running',first_seen,last_seen,'svc2',created_at,updated_at FROM services WHERE id=?`, svcID)
	var svc2 int64
	_ = st.DB().QueryRow(`SELECT id FROM services WHERE natural_key='svc2'`).Scan(&svc2)
	_, _ = st.DB().Exec(`INSERT INTO ports(connector_id,service_id,host_id,number,protocol,published,host_ip,container_port,source,natural_key) VALUES(1,?,?,8096,'tcp',1,'0.0.0.0',8096,'fixture','conflict')`, svc2, hostID)
	req = httptest.NewRequest(http.MethodGet, "/api/ports/conflicts", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"number":8096`)) {
		t.Fatalf("conflicts: %s", rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/ports/next-free?host_id=%d&start=8096&end=8098", hostID), nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"port":8097`)) {
		t.Fatalf("next free: %s", rec.Body.String())
	}
}

func TestListsRequireAuthenticationByDefault(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := New(st, NewBroker(), Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}
