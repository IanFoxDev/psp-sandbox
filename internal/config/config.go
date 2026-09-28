// Package config reads sandbox settings from the environment.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is the sandbox configuration. See docs/api.md for the variables.
type Config struct {
	Addr            string
	APIKey          string
	CallbackURL     string
	WebhookSecret   string
	ScenariosFile   string
	DefaultScenario string
	ProcessingDelay time.Duration
	RetrySchedule   []time.Duration
	ManualClock     bool
	Seed            string
	LogFormat       string
}

// FromEnv reads PSP_* variables, applying defaults for unset ones.
func FromEnv() (Config, error) {
	c := Config{
		Addr:            env("PSP_ADDR", ":8090"),
		APIKey:          os.Getenv("PSP_API_KEY"),
		CallbackURL:     os.Getenv("PSP_CALLBACK_URL"),
		WebhookSecret:   os.Getenv("PSP_WEBHOOK_SECRET"),
		ScenariosFile:   os.Getenv("PSP_SCENARIOS_FILE"),
		DefaultScenario: env("PSP_DEFAULT_SCENARIO", "happy_path"),
		Seed:            os.Getenv("PSP_SEED"),
		LogFormat:       env("PSP_LOG_FORMAT", "text"),
	}

	if c.CallbackURL != "" {
		u, err := url.Parse(c.CallbackURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, fmt.Errorf("PSP_CALLBACK_URL: want an absolute http or https URL, got %q", c.CallbackURL)
		}
	}

	var err error
	if c.ProcessingDelay, err = time.ParseDuration(env("PSP_PROCESSING_DELAY", "200ms")); err != nil {
		return c, fmt.Errorf("PSP_PROCESSING_DELAY: %w", err)
	}
	if c.RetrySchedule, err = parseDurations(env("PSP_RETRY_SCHEDULE", "0s,5s,30s,2m,10m,1h")); err != nil {
		return c, fmt.Errorf("PSP_RETRY_SCHEDULE: %w", err)
	}

	switch clock := env("PSP_CLOCK", "real"); clock {
	case "real":
	case "manual":
		c.ManualClock = true
	default:
		return c, fmt.Errorf("PSP_CLOCK: want real or manual, got %q", clock)
	}

	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, fmt.Errorf("PSP_LOG_FORMAT: want text or json, got %q", c.LogFormat)
	}

	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func parseDurations(s string) ([]time.Duration, error) {
	parts := strings.Split(s, ",")
	out := make([]time.Duration, 0, len(parts))
	for _, p := range parts {
		d, err := time.ParseDuration(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
