// Package config reads sandbox settings from the environment.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// Profiles, the values of PSP_PROFILE.
const (
	ProfileNative = "native"
	ProfileStripe = "stripe"
)

// Config is the sandbox configuration. See docs/api.md for the variables.
type Config struct {
	// Profile is the provider API the sandbox speaks: native or stripe.
	Profile string
	Addr    string
	// PublicURL is where a browser reaches the sandbox, for links to the
	// pages where a customer acts. No trailing slash.
	PublicURL       string
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
		Profile:         env("PSP_PROFILE", ProfileNative),
		Addr:            env("PSP_ADDR", ":8090"),
		APIKey:          os.Getenv("PSP_API_KEY"),
		CallbackURL:     os.Getenv("PSP_CALLBACK_URL"),
		WebhookSecret:   os.Getenv("PSP_WEBHOOK_SECRET"),
		ScenariosFile:   os.Getenv("PSP_SCENARIOS_FILE"),
		DefaultScenario: env("PSP_DEFAULT_SCENARIO", "happy_path"),
		Seed:            os.Getenv("PSP_SEED"),
		LogFormat:       env("PSP_LOG_FORMAT", "text"),
	}

	c.PublicURL = strings.TrimRight(os.Getenv("PSP_PUBLIC_URL"), "/")
	if c.PublicURL == "" {
		c.PublicURL = defaultPublicURL(c.Addr)
	} else if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return c, fmt.Errorf("PSP_PUBLIC_URL: want an absolute http or https URL, got %q", c.PublicURL)
	}

	if c.CallbackURL != "" {
		u, err := url.Parse(c.CallbackURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, fmt.Errorf("PSP_CALLBACK_URL: want an absolute http or https URL, got %q", c.CallbackURL)
		}
	}

	var err error
	// Stripe answers a card payment with its outcome, so the stripe profile
	// settles at once unless asked otherwise.
	delay := "200ms"
	if c.Profile == ProfileStripe {
		delay = "0s"
	}
	if c.ProcessingDelay, err = time.ParseDuration(env("PSP_PROCESSING_DELAY", delay)); err != nil {
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

	if c.Profile != ProfileNative && c.Profile != ProfileStripe {
		return c, fmt.Errorf("PSP_PROFILE: want %s or %s, got %q", ProfileNative, ProfileStripe, c.Profile)
	}

	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, fmt.Errorf("PSP_LOG_FORMAT: want text or json, got %q", c.LogFormat)
	}

	return c, nil
}

// defaultPublicURL is localhost on the port of addr: right when tests and
// the browser run on the host that publishes the port.
func defaultPublicURL(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" {
		port = "8090"
	}
	return "http://localhost:" + port
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
