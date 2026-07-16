package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/notify"
	"github.com/HarshShah0203/homedex/internal/store"
)

type productSender struct{ messages []string }

func (s *productSender) Send(_ context.Context, target, message string) error {
	s.messages = append(s.messages, target+" "+message)
	return nil
}

type productFixtureConnector struct{}

func (productFixtureConnector) Kind() string                                      { return "fixture-product" }
func (productFixtureConnector) Validate(context.Context, connectors.Config) error { return nil }
func (productFixtureConnector) Scan(context.Context, connectors.Config) (domain.Snapshot, error) {
	return domain.Snapshot{}, nil
}

type adminClient struct {
	h      http.Handler
	cookie *http.Cookie
	csrf   string
}

func newProductServer(t *testing.T) (*store.Store, http.Handler, *productSender) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, _ := auth.NewSecretBox(bytes.Repeat([]byte{7}, 32))
	configs := store.NewConnectorConfigs(st, box)
	registry := connectors.NewRegistry()
	_ = registry.Register(productFixtureConnector{})
	runner := engine.NewRunner(st, configs, registry, engine.New(st, nil))
	sender := &productSender{}
	notifications, err := notify.NewManager(ctx, st, box, sender)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	h := New(st, NewBroker(), Config{ConnectorConfigs: configs, Registry: registry, Runner: runner, Notifications: notifications})
	return st, h, sender
}

func loginProductAdmin(t *testing.T, handler http.Handler) adminClient {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"password":"correct horse battery staple"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || response.CSRF == "" {
		t.Fatal("setup did not issue session and CSRF token")
	}
	return adminClient{h: handler, cookie: cookies[0], csrf: response.CSRF}
}

func (c adminClient) request(method, path, body string, withCSRF bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(c.cookie)
	if withCSRF {
		req.Header.Set("X-Homedex-CSRF", c.csrf)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func TestEntityExpiryChangeAndNotificationVerticalAPIs(t *testing.T) {
	st, handler, sender := newProductServer(t)
	defer st.Close()
	admin := loginProductAdmin(t, handler)

	// Every authenticated mutation, including enrichment, requires the echoed
	// session CSRF token.
	rec := admin.request(http.MethodPost, "/api/entities", `{"entity_type":"host","name":"printer"}`, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mutation without CSRF status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = admin.request(http.MethodPost, "/api/entities", `{"entity_type":"host","name":"printer","address":"10.0.0.50","notes":"Office shelf","tags":[{"name":"hardware","color":"#abc"}],"custom_fields":[{"key":"serial","kind":"text","value":"P-123"}]}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create entity status=%d body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Entity map[string]any `json:"entity"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	hostID := int64(detail.Entity["id"].(float64))
	rec = admin.request(http.MethodPatch, "/api/entities/host/"+strconv64(hostID), `{"notes":"Moved to rack","tags":[{"name":"network"}],"custom_fields":[]}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Moved to rack") {
		t.Fatalf("patch entity status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = admin.request(http.MethodGet, "/api/search?q=Moved", "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Moved to rack") {
		t.Fatalf("enriched note search status=%d body=%s", rec.Code, rec.Body.String())
	}

	expires := time.Now().Add(10 * 24 * time.Hour).UTC().Format(time.RFC3339)
	rec = admin.request(http.MethodPost, "/api/entities", `{"entity_type":"expiry","name":"Router warranty","kind":"warranty","authority":"Vendor","expires_at":"`+expires+`"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create expiry status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = admin.request(http.MethodGet, "/api/expiry", "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Router warranty") || !strings.Contains(rec.Body.String(), "days_remaining") {
		t.Fatalf("merged expiry status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = admin.request(http.MethodGet, "/api/summary", "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hosts"`) || !strings.Contains(rec.Body.String(), `"expiry"`) {
		t.Fatalf("summary status=%d body=%s", rec.Code, rec.Body.String())
	}

	_, _ = st.DB().Exec(`INSERT INTO scan_runs(id,started_at,status) VALUES(50,'now','success')`)
	_, _ = st.DB().Exec(`INSERT INTO changes(id,scan_run_id,entity_type,entity_id,change_kind,summary,created_at) VALUES(60,50,'host',?,'modified','Printer changed','now'),(61,50,'host',?,'modified','Printer moved','now')`, hostID, hostID)
	rec = admin.request(http.MethodPatch, "/api/changes/60", `{"seen":true,"note":"reviewed"}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("review change status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = admin.request(http.MethodPatch, "/api/changes", `{"ids":[60,61],"seen":true}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":2`) {
		t.Fatalf("bulk review status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = admin.request(http.MethodPost, "/api/notify/rules", `{"name":"Warranty expiry","kind":"expiry","threshold_days":14,"channels":["ntfy://notify.example/topic?token=hidden"]}`, true)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "hidden") || !strings.Contains(rec.Body.String(), `"channels":["ntfy"]`) {
		t.Fatalf("create notification status=%d body=%s", rec.Code, rec.Body.String())
	}
	var legacyChannels string
	var encryptedChannels []byte
	if queryErr := st.DB().QueryRow(`SELECT channels,channels_encrypted FROM notification_rules WHERE id=1`).Scan(&legacyChannels, &encryptedChannels); queryErr != nil {
		t.Fatal(queryErr)
	}
	if legacyChannels != "[]" || len(encryptedChannels) == 0 || bytes.Contains(encryptedChannels, []byte("hidden")) {
		t.Fatalf("API persisted notification credentials in plaintext: legacy=%q ciphertext=%q", legacyChannels, encryptedChannels)
	}
	rec = admin.request(http.MethodPost, "/api/notify/rules/1/test", `{}`, true)
	if rec.Code != http.StatusOK || len(sender.messages) != 1 {
		t.Fatalf("test notification status=%d messages=%#v body=%s", rec.Code, sender.messages, rec.Body.String())
	}

	rec = admin.request(http.MethodPost, "/api/connectors/test", `{"kind":"fixture-product","name":"unsaved","config":{}}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unsaved connector test status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestShareTokensAreReadOnlyScopedRevocableAndExportsStayPrivate(t *testing.T) {
	st, handler, _ := newProductServer(t)
	defer st.Close()
	admin := loginProductAdmin(t, handler)

	result, err := st.DB().Exec(`INSERT INTO services(name,kind,state,first_seen,last_seen,raw_labels,notes,natural_key,created_at,updated_at) VALUES('photos','container','running','now','now','{"api_token":"never-export-me"}','base private note','manual:service:test','now','now')`)
	if err != nil {
		t.Fatal(err)
	}
	serviceID, _ := result.LastInsertId()
	_, _ = st.DB().Exec(`INSERT INTO entity_notes(entity_type,entity_id,notes,updated_at) VALUES('service',?,'private entity note','now')`, serviceID)

	rec := admin.request(http.MethodPost, "/api/share", `{"name":"temporary","expires_in_hours":24}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       int64  `json:"id"`
		Token    string `json:"token"`
		ShareURL string `json:"share_url"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || !strings.HasPrefix(created.ShareURL, "/share/") {
		t.Fatalf("invalid share response: %s", rec.Body.String())
	}
	rec = admin.request(http.MethodGet, "/api/share", "", false)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), created.Token) {
		t.Fatalf("share list leaked plaintext token: %s", rec.Body.String())
	}

	shareRequest := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Homedex-Share", created.Token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	if rec = shareRequest(http.MethodGet, "/api/services", ""); rec.Code != http.StatusOK {
		t.Fatalf("share inventory status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = shareRequest(http.MethodGet, "/api/connectors", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("share accessed settings status=%d", rec.Code)
	}
	if rec = shareRequest(http.MethodPatch, "/api/entities/service/"+strconv64(serviceID), `{"notes":"tamper"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("share mutation status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = shareRequest(http.MethodGet, "/api/entities/service/"+strconv64(serviceID), ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), "never-export-me") || strings.Contains(rec.Body.String(), "raw_labels") {
		t.Fatalf("share entity leaked private data: %d %s", rec.Code, rec.Body.String())
	}
	if rec = shareRequest(http.MethodGet, "/api/export/json", ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private") || strings.Contains(rec.Body.String(), "never-export-me") {
		t.Fatalf("share export leaked private data: %d %s", rec.Code, rec.Body.String())
	}

	redirectReq := httptest.NewRequest(http.MethodGet, created.ShareURL, nil)
	redirectRec := httptest.NewRecorder()
	handler.ServeHTTP(redirectRec, redirectReq)
	if redirectRec.Code != http.StatusSeeOther || len(redirectRec.Result().Cookies()) == 0 || redirectRec.Result().Cookies()[0].Name != "homedex_share" {
		t.Fatalf("share URL did not establish read-only cookie: %d %#v", redirectRec.Code, redirectRec.Result().Cookies())
	}

	rec = admin.request(http.MethodDelete, "/api/share/"+strconv64(created.ID), "", true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke share status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = shareRequest(http.MethodGet, "/api/services", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked share status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func strconv64(value int64) string {
	return fmt.Sprintf("%d", value)
}
