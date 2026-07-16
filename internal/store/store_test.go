package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationAndPragmas(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000} {
		var got int
		if err := s.DB().QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("PRAGMA %s=%d, want %d", pragma, got, want)
		}
	}
	var mode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", mode)
	}
	var tables int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('hosts','services','ports','routes','certs','domains','changes','sessions')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 8 {
		t.Fatalf("created %d core tables, want 8", tables)
	}
	var networks int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='service_networks'`).Scan(&networks); err != nil {
		t.Fatal(err)
	}
	if networks != 1 {
		t.Fatal("service_networks migration was not applied")
	}
	var productTables int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('entity_notes','manual_expiries','notification_deliveries')`).Scan(&productTables); err != nil {
		t.Fatal(err)
	}
	if productTables != 3 {
		t.Fatalf("created %d product tables, want 3", productTables)
	}
}
