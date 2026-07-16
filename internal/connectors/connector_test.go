package connectors

import (
	"encoding/json"
	"testing"
)

func TestDecodeConfig(t *testing.T) {
	type config struct {
		URL     string `json:"url"`
		Enabled bool   `json:"enabled"`
	}

	got, err := DecodeConfig[config](Config{
		"url":     json.RawMessage(`"https://proxy.example"`),
		"enabled": json.RawMessage(`true`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://proxy.example" || !got.Enabled {
		t.Fatalf("decoded config = %#v", got)
	}
}

func TestDecodeConfigReturnsJSONErrors(t *testing.T) {
	type config struct {
		URL  string `json:"url"`
		Port int    `json:"port"`
	}

	got, err := DecodeConfig[config](Config{
		"url":  json.RawMessage(`"https://proxy.example"`),
		"port": json.RawMessage(`"not-a-number"`),
	})
	if err == nil {
		t.Fatal("invalid typed config was accepted")
	}
	if got.URL != "https://proxy.example" {
		t.Fatalf("successfully decoded fields were not preserved: %#v", got)
	}
	if _, err := DecodeConfig[config](Config{"port": json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid raw JSON was accepted")
	}
}
