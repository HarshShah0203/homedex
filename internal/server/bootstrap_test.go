package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/store"
)

func TestBootstrapAdminStoresTheSameHashAsSetupAndNeverReplacesIt(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err = BootstrapAdmin(ctx, st, "short"); err == nil || !strings.Contains(err.Error(), "at least 12 characters") {
		t.Fatalf("weak password: error=%v", err)
	}
	outcome, err := BootstrapAdmin(ctx, st, "first bootstrap passphrase")
	if err != nil || outcome != AdminCreated {
		t.Fatalf("first bootstrap outcome=%v error=%v", outcome, err)
	}
	var hash string
	if err = st.DB().QueryRow(`SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") || !auth.VerifyPassword(hash, "first bootstrap passphrase") {
		t.Fatalf("stored hash is not the setup argon2id hash: %q", hash)
	}

	outcome, err = BootstrapAdmin(ctx, st, "second bootstrap passphrase")
	if err != nil || outcome != AdminAlreadyExists {
		t.Fatalf("second bootstrap outcome=%v error=%v", outcome, err)
	}
	var after string
	if err = st.DB().QueryRow(`SELECT value FROM settings WHERE key='admin_password_hash'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != hash {
		t.Fatal("a second bootstrap replaced the admin password")
	}
}
