package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/nacl/secretbox"
)

type SecretBox struct{ key [32]byte }

func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != 32 {
		return nil, errors.New("secretbox key must be exactly 32 bytes")
	}
	b := &SecretBox{}
	copy(b.key[:], key)
	return b, nil
}

// LoadOrCreateSecretBox uses HOMEDEX_SECRET (base64-encoded 32 bytes) when set,
// otherwise it creates a mode-0600 key in the data directory.
func LoadOrCreateSecretBox(dataDir string) (*SecretBox, error) {
	if value := strings.TrimSpace(os.Getenv("HOMEDEX_SECRET")); value != "" {
		key, err := base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			key, err = base64.StdEncoding.DecodeString(value)
		}
		if err != nil {
			return nil, fmt.Errorf("decode HOMEDEX_SECRET: %w", err)
		}
		return NewSecretBox(key)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "instance.key")
	key, err := os.ReadFile(path)
	if err == nil {
		return NewSecretBox(key)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return NewSecretBox(existing)
		}
		return nil, err
	}
	if _, err = f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return NewSecretBox(key)
}

func (b *SecretBox) Seal(plaintext []byte) ([]byte, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return secretbox.Seal(nonce[:], plaintext, &nonce, &b.key), nil
}

func (b *SecretBox) Open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 24 {
		return nil, errors.New("encrypted config is truncated")
	}
	var nonce [24]byte
	copy(nonce[:], ciphertext[:24])
	plain, ok := secretbox.Open(nil, ciphertext[24:], &nonce, &b.key)
	if !ok {
		return nil, errors.New("encrypted config authentication failed")
	}
	return plain, nil
}
