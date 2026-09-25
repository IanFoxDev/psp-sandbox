package config

import (
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
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
