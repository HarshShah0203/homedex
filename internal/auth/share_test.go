package auth_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestShareTokenLifecycleAndExpiry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	m := auth.NewShareManager(st.DB())
	expiry := now.Add(time.Hour)
	created, err := m.Create(ctx, "temporary", &expiry)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Token) < 22 || created.ID == 0 {
		t.Fatalf("weak or missing share token: %#v", created)
	}
	var stored string
	if err = st.DB().QueryRow(`SELECT token_hash FROM share_tokens WHERE id=?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == created.Token {
		t.Fatal("plaintext share token was persisted")
	}
	if _, err = m.Validate(ctx, created.Token); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	_, err = st.DB().Exec(`UPDATE share_tokens SET expires_at=? WHERE id=?`, now.Add(-time.Minute).Format(time.RFC3339Nano), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Validate(ctx, created.Token); err == nil {
		t.Fatal("expired token accepted")
	}
	_, err = st.DB().Exec(`UPDATE share_tokens SET expires_at=? WHERE id=?`, expiry.Format(time.RFC3339Nano), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Revoke(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Validate(ctx, created.Token); err == nil {
		t.Fatal("revoked token accepted")
	}
}

func TestShareTokenRejectsPastExpiry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := auth.NewShareManager(st.DB())
	past := time.Now().Add(-time.Minute)
	if _, err = m.Create(ctx, "invalid", &past); err == nil {
		t.Fatal("past expiry accepted")
	}
}
