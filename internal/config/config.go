package config

import (
	"fmt"
	"log"
	"os"
	"time"
)

type Config struct {
	SlackBotToken    string
	SlackChannelID   string
	HyperDXBaseURL   string
	StormThreshold   int
	StormWindow      time.Duration
	ReminderInterval time.Duration
	ThreadMaxAge     time.Duration
	ListenAddr       string
}

func Load() (Config, error) {
	c := Config{
		SlackBotToken:    os.Getenv("SLACK_BOT_TOKEN"),
		SlackChannelID:   os.Getenv("SLACK_CHANNEL_ID"),
		HyperDXBaseURL:   os.Getenv("HYPERDX_BASE_URL"),
		StormThreshold:   envInt("STORM_THRESHOLD", 10),
		StormWindow:      envDur("STORM_WINDOW", 60*time.Second),
		ReminderInterval: envDur("REMINDER_INTERVAL", 30*time.Minute),
		ThreadMaxAge:     envDur("THREAD_MAX_AGE", 24*time.Hour),
		ListenAddr:       envStr("LISTEN_ADDR", ":8080"),
	}
	if c.SlackBotToken == "" || c.SlackChannelID == "" {
		return Config{}, fmt.Errorf("SLACK_BOT_TOKEN and SLACK_CHANNEL_ID are required")
	}
	return c, nil
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
		log.Printf("config: invalid %s=%q, using default", k, v)
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("config: invalid %s=%q, using default", k, v)
	}
	return def
}
