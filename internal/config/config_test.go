package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_CHANNEL_ID", "C123")
	t.Setenv("HYPERDX_BASE_URL", "https://hyperdx.example.com")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.StormThreshold != 10 || c.StormWindow != 60*time.Second ||
		c.ReminderInterval != 30*time.Minute || c.ThreadMaxAge != 24*time.Hour ||
		c.ListenAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when SLACK_BOT_TOKEN missing")
	}
}
