package notify

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
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

type credentialEchoSender struct{}

func (credentialEchoSender) Send(_ context.Context, target, _ string) error {
	return errors.New("could not deliver to " + target)
}

func testManager(t *testing.T, ctx context.Context, st *store.Store, sender Sender) *Manager {
	t.Helper()
	manager, err := NewManager(ctx, st, testSecretBox(t, 9), sender)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testSecretBox(t *testing.T, value byte) *auth.SecretBox {
	t.Helper()
	box, err := auth.NewSecretBox(bytes.Repeat([]byte{value}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestRuleTestAndExpiryEvaluationAreRedactedAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeSender{}
	m := testManager(t, ctx, st, fake)
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
	var legacy string
	var encrypted []byte
	if err = st.DB().QueryRow(`SELECT channels,channels_encrypted FROM notification_rules WHERE id=?`, rule.ID).Scan(&legacy, &encrypted); err != nil {
		t.Fatal(err)
	}
	if legacy != "[]" || len(encrypted) == 0 || bytes.Contains(encrypted, []byte("very-secret")) {
		t.Fatalf("notification destination not encrypted at rest: legacy=%q ciphertext=%q", legacy, encrypted)
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

func TestNotificationCRUDKeepsDestinationsEncryptedAndDeliversUpdatedURL(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeSender{}
	m := testManager(t, ctx, st, fake)
	rule, err := m.Create(ctx, RuleInput{Name: "Changes", Kind: "change", Channels: []string{"generic://first.example?token=first-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	rule, err = m.Update(ctx, rule.ID, RuleInput{Name: "Updated changes", Channels: []string{"generic://second.example?token=second-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if rule.Name != "Updated changes" || len(rule.Channels) != 1 || rule.Channels[0] != "generic" {
		t.Fatalf("unexpected updated rule: %#v", rule)
	}
	var legacy string
	var encrypted []byte
	if err = st.DB().QueryRow(`SELECT channels,channels_encrypted FROM notification_rules WHERE id=?`, rule.ID).Scan(&legacy, &encrypted); err != nil {
		t.Fatal(err)
	}
	if legacy != "[]" || bytes.Contains(encrypted, []byte("first-secret")) || bytes.Contains(encrypted, []byte("second-secret")) {
		t.Fatalf("updated destinations leaked at rest: legacy=%q ciphertext=%q", legacy, encrypted)
	}
	if err = m.Test(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 1 || !strings.Contains(fake.messages[0], "second.example?token=second-secret") || strings.Contains(fake.messages[0], "first-secret") {
		t.Fatalf("test delivery did not use decrypted updated destination: %#v", fake.messages)
	}
	listed, err := m.List(ctx)
	if err != nil || len(listed) != 1 || strings.Contains(strings.Join(listed[0].Channels, ""), "second-secret") {
		t.Fatalf("masked list=%#v error=%v", listed, err)
	}
	if err = m.Delete(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	listed, err = m.List(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("rules after delete=%#v error=%v", listed, err)
	}
}

func TestLegacyDestinationsArePreservedEncryptedAndPhysicallyScrubbed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	const target = "ntfy://legacy.example/topic?token=legacy-plaintext-token-7f3c"
	result, err := st.DB().Exec(`INSERT INTO notification_rules(name,kind,filters,channels,enabled,created_at,updated_at) VALUES('Legacy','change','{}',?,1,'before','before')`, `["`+target+`"]`)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	fake := &fakeSender{}
	m, err := NewManager(ctx, st, testSecretBox(t, 4), fake)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	var legacy string
	var encrypted []byte
	if err = st.DB().QueryRow(`SELECT channels,channels_encrypted FROM notification_rules WHERE id=?`, id).Scan(&legacy, &encrypted); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if legacy != "[]" || len(encrypted) == 0 || bytes.Contains(encrypted, []byte("legacy-plaintext-token")) {
		st.Close()
		t.Fatalf("legacy rule was not encrypted: legacy=%q ciphertext=%q", legacy, encrypted)
	}
	if err = m.Test(ctx, id); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if len(fake.messages) != 1 || !strings.Contains(fake.messages[0], target) {
		st.Close()
		t.Fatalf("legacy destination was not preserved for delivery: %#v", fake.messages)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		contents, readErr := os.ReadFile(candidate)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(contents, []byte("legacy-plaintext-token-7f3c")) {
			t.Fatalf("legacy destination credential remains in %s", filepath.Base(candidate))
		}
	}
}

func TestNotificationBackupRestoreRequiresExactSecretAndDoesNotCorruptCiphertext(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	original := filepath.Join(dir, "original.db")
	restored := filepath.Join(dir, "restored.db")
	key := testSecretBox(t, 5)
	st, err := store.Open(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ctx, st, key, &fakeSender{})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := m.Create(ctx, RuleInput{Name: "Backup", Kind: "change", Channels: []string{"generic://backup.example?token=restore-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(restored, database, 0600); err != nil {
		t.Fatal(err)
	}

	restoredStore, err := store.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSender{}
	restoredManager, err := NewManager(ctx, restoredStore, key, fake)
	if err != nil {
		t.Fatal(err)
	}
	if err = restoredManager.Test(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 1 || !strings.Contains(fake.messages[0], "restore-secret") {
		t.Fatalf("restored rule did not deliver: %#v", fake.messages)
	}
	var before []byte
	if err = restoredStore.DB().QueryRow(`SELECT channels_encrypted FROM notification_rules WHERE id=?`, rule.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = restoredStore.Close(); err != nil {
		t.Fatal(err)
	}

	wrongKeyStore, err := store.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewManager(ctx, wrongKeyStore, testSecretBox(t, 6), &fakeSender{}); err == nil || !strings.Contains(err.Error(), "decrypt notification rule") {
		wrongKeyStore.Close()
		t.Fatalf("wrong restore key error=%v", err)
	}
	var after []byte
	if err = wrongKeyStore.DB().QueryRow(`SELECT channels_encrypted FROM notification_rules WHERE id=?`, rule.ID).Scan(&after); err != nil {
		wrongKeyStore.Close()
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		wrongKeyStore.Close()
		t.Fatal("wrong restore key modified destination ciphertext")
	}
	if err = wrongKeyStore.Close(); err != nil {
		t.Fatal(err)
	}

	retryStore, err := store.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer retryStore.Close()
	if _, err = NewManager(ctx, retryStore, key, &fakeSender{}); err != nil {
		t.Fatalf("correct key did not recover after wrong-key attempt: %v", err)
	}
}

func TestNotificationManagerRejectsMissingSecretBox(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = NewManager(context.Background(), st, nil, &fakeSender{}); err == nil || !strings.Contains(err.Error(), "SecretBox") {
		t.Fatalf("missing SecretBox error=%v", err)
	}
}

func TestDeliveryErrorsDoNotExposeDestinationCredentials(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := testManager(t, ctx, st, credentialEchoSender{})
	rule, err := m.Create(ctx, RuleInput{Name: "Masked failure", Kind: "change", Channels: []string{"generic://notify.example?token=error-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	err = m.Test(ctx, rule.ID)
	if err == nil || strings.Contains(err.Error(), "error-secret") || err.Error() != "generic notification delivery failed" {
		t.Fatalf("unmasked delivery error=%v", err)
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
	m := testManager(t, ctx, st, fake)
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
	m := testManager(t, ctx, st, sender)
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
