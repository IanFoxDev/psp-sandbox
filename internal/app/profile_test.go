package app_test

import (
	"strings"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
)

func TestStripeProfileReplacesNativeAPI(t *testing.T) {
	s := sandboxtest.New(t, func(c *config.Config) { c.Profile = config.ProfileStripe })

	r := s.Do("POST", "/v1/payments", map[string]any{"amount": 1000, "currency": "EUR"},
		"Authorization", "Bearer sk_test_123")
	if r.Status != 404 || !strings.Contains(string(r.Body), "invalid_request_error") {
		t.Errorf("native create in the stripe profile: %d %s", r.Status, r.Body)
	}
	if r.Header.Get("Request-Id") == "" {
		t.Error("no Request-Id")
	}
	if r := s.Do("GET", "/_sandbox/scenarios", nil); r.Status != 200 {
		t.Errorf("control API: %d", r.Status)
	}
	if r := s.Do("GET", "/healthz", nil); r.Status != 200 {
		t.Errorf("healthz: %d", r.Status)
	}
}

func TestNativeProfileIsTheDefault(t *testing.T) {
	s := sandboxtest.New(t)
	r := s.Do("POST", "/v1/payments", map[string]any{"amount": 1000, "currency": "EUR"})
	if r.Status != 201 || r.Header.Get("Request-Id") != "" {
		t.Errorf("native create: %d, Request-Id %q", r.Status, r.Header.Get("Request-Id"))
	}
}
