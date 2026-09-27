package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("invalid password accepted")
	}
}

func TestSecretBoxRoundTripAndTamper(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	b, err := NewSecretBox(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := b.Seal([]byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := b.Open(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != `{"token":"secret"}` {
		t.Fatalf("unexpected plaintext %s", plain)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err = b.Open(ciphertext); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestLoadOrCreateSecretBoxProtectsKeyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMEDEX_SECRET", "")
	if _, err := LoadOrCreateSecretBox(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "instance.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key mode=%o, want 600", info.Mode().Perm())
	}
}

// A platform that makes app data world-readable on every start (Runtipi runs
// chmod -R a+rwx) must not leave the key that decrypts source credentials open.
func TestLoadOrCreateSecretBoxRestrictsAnExistingKeyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOMEDEX_SECRET", "")
	first, err := LoadOrCreateSecretBox(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "instance.key")
	if err = os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateSecretBox(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.key != again.key {
		t.Fatal("reloading the key changed it")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode=%o after reload, want 600", info.Mode().Perm())
	}
}
