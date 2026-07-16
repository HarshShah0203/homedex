package main

import (
	"testing"
	"time"
)

func TestEnvDays(t *testing.T) {
	t.Setenv("TEST_RETENTION", "")
	got, err := envDays("TEST_RETENTION", 30)
	if err != nil || got != 30*24*time.Hour {
		t.Fatalf("default retention=%s error=%v", got, err)
	}
	t.Setenv("TEST_RETENTION", "7")
	got, err = envDays("TEST_RETENTION", 30)
	if err != nil || got != 7*24*time.Hour {
		t.Fatalf("configured retention=%s error=%v", got, err)
	}
	for _, invalid := range []string{"0", "-1", "1.5", "forever"} {
		t.Setenv("TEST_RETENTION", invalid)
		if _, err = envDays("TEST_RETENTION", 30); err == nil {
			t.Fatalf("accepted invalid retention %q", invalid)
		}
	}
}
