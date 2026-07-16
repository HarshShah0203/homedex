package notify

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/store"
)

type fakeSender struct{ messages []string }

func (f *fakeSender) Send(_ context.Context, target, message string) error {
	f.messages = append(f.messages, target+" "+message)
	return nil
}

type retrySender struct{ calls int }

func (s *retrySender) Send(context.Context, string, string) error {
	s.calls++
	if s.calls == 1 {
		return errors.New("temporary delivery failure")
	}
	return nil
}

func TestRuleTestAndExpiryEvaluationAreRedactedAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeSender{}
	m := NewManager(st, fake)
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	threshold := 14
	rule, err := m.Create(ctx, RuleInput{Name: "TLS expiry", Kind: "expiry", ThresholdDays: &threshold, Channels: []string{"ntfy://example/topic?token=very-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rule.Channels) != 1 || rule.Channels[0] != "ntfy" || strings.Contains(strings.Join(rule.Channels, ""), "secret") {
		t.Fatalf("channel credentials leaked: %#v", rule)
	}
	if err = m.Test(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	expires := now.Add(10 * 24 * time.Hour).Format(time.RFC3339Nano)
	_, err = st.DB().Exec(`INSERT INTO certs(natural_key,subject,sans,issuer,not_after,chain_valid,source,endpoint,state,first_seen,last_seen,created_at,updated_at) VALUES('cert','photos.example','[]','issuer',?,1,'probe','photos.example:443','active','now','now','now','now')`, expires)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Evaluate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err = m.Evaluate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 2 { // one explicit test and one deduplicated expiry delivery
		t.Fatalf("messages=%#v", fake.messages)
	}
}

func TestChangeRuleFiltersCurrentScan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeSender{}
	m := NewManager(st, fake)
	rule, err := m.Create(ctx, RuleInput{Name: "Route changes", Kind: "change", Channels: []string{"generic://example"}, Filters: map[string]any{"entity_types": []any{"route"}}})
	if err != nil || rule.ID == 0 {
		t.Fatal(err)
	}
	_, _ = st.DB().Exec(`INSERT INTO scan_runs(id,started_at,status) VALUES(1,'now','success')`)
	_, _ = st.DB().Exec(`INSERT INTO changes(scan_run_id,entity_type,entity_id,change_kind,summary,created_at) VALUES(1,'service',1,'added','service added','now'),(1,'route',2,'modified','route changed','now')`)
	if err = m.Evaluate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 1 || !strings.Contains(fake.messages[0], "route changed") {
		t.Fatalf("messages=%#v", fake.messages)
	}
}

func TestFailedDeliveryRetriesButSuccessfulDeliveryDeduplicates(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sender := &retrySender{}
	m := NewManager(st, sender)
	if _, err = m.Create(ctx, RuleInput{Name: "Changes", Kind: "change", Channels: []string{"generic://example"}}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.DB().Exec(`INSERT INTO scan_runs(id,started_at,status) VALUES(1,'now','success')`)
	_, _ = st.DB().Exec(`INSERT INTO changes(scan_run_id,entity_type,entity_id,change_kind,summary,created_at) VALUES(1,'service',1,'added','service added','now')`)
	if err = m.Evaluate(ctx, 1); err == nil {
		t.Fatal("temporary send failure was not returned")
	}
	if err = m.Evaluate(ctx, 1); err != nil {
		t.Fatalf("failed delivery was not retried: %v", err)
	}
	if err = m.Evaluate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 2 {
		t.Fatalf("sender calls=%d, want one failure and one successful retry", sender.calls)
	}
}
