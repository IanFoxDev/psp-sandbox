package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile != ProfileNative {
		t.Errorf("Profile = %q", c.Profile)
	}
	if c.Addr != ":8090" {
		t.Errorf("Addr = %q", c.Addr)
	}
	if len(c.RetrySchedule) != 6 || c.RetrySchedule[5] != time.Hour {
		t.Errorf("RetrySchedule = %v", c.RetrySchedule)
	}
}

func TestInvalidClock(t *testing.T) {
	t.Setenv("PSP_CLOCK", "fast")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error for PSP_CLOCK=fast")
	}
}

func TestInvalidCallbackURL(t *testing.T) {
	for _, v := range []string{"not-a-url", "app/api/psp/callback", "ftp://app/cb"} {
		t.Setenv("PSP_CALLBACK_URL", v)
		if _, err := FromEnv(); err == nil {
			t.Errorf("PSP_CALLBACK_URL=%q accepted", v)
		}
	}
	t.Setenv("PSP_CALLBACK_URL", "http://app:8000/api/psp/callback")
	if _, err := FromEnv(); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
}

func TestRetrySchedule(t *testing.T) {
	t.Setenv("PSP_RETRY_SCHEDULE", "0s, 1s ,10s")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{0, time.Second, 10 * time.Second}
	for i, d := range want {
		if c.RetrySchedule[i] != d {
			t.Errorf("RetrySchedule[%d] = %v, want %v", i, c.RetrySchedule[i], d)
		}
	}
}

func TestProfile(t *testing.T) {
	t.Setenv("PSP_PROFILE", "stripe")
	c, err := FromEnv()
	if err != nil || c.Profile != ProfileStripe || c.ProcessingDelay != 0 {
		t.Fatalf("stripe: %q %v %v", c.Profile, c.ProcessingDelay, err)
	}
	t.Setenv("PSP_PROCESSING_DELAY", "1s")
	if c, _ := FromEnv(); c.ProcessingDelay != time.Second {
		t.Fatalf("explicit delay in the stripe profile: %v", c.ProcessingDelay)
	}
	t.Setenv("PSP_PROFILE", "adyen")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "PSP_PROFILE") {
		t.Fatalf("adyen: %v", err)
	}
}
