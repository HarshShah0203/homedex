package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/HarshShah0203/homedex/internal/auth"
)

func TestConnectorConfigsAreEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	box, err := auth.NewSecretBox(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	configs := NewConnectorConfigs(s, box)
	id, err := configs.Create(ctx, "traefik", "proxy", map[string]string{"token": "super-secret", "url": "https://proxy.local"})
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = s.DB().QueryRow(`SELECT config_encrypted FROM connectors WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("super-secret")) || bytes.Contains(raw, []byte("proxy.local")) {
		t.Fatal("plaintext connector config was stored")
	}
	var got map[string]string
	if err = configs.Load(ctx, id, &got); err != nil {
		t.Fatal(err)
	}
	if got["token"] != "super-secret" || got["url"] != "https://proxy.local" {
		t.Fatalf("unexpected decrypted config: %#v", got)
	}
}
