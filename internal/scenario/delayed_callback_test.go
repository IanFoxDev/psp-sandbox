package scenario_test

import (
	"testing"
	"time"

	"github.com/ghuser/psp-sandbox/internal/clock"
	"github.com/ghuser/psp-sandbox/internal/config"
	"github.com/ghuser/psp-sandbox/internal/sandboxtest"
	"github.com/ghuser/psp-sandbox/internal/scenario"
)

// The payment is captured, but the application hears about it only later.
// With the manual clock the test does not wait the delay out.
func TestDelayedCallback(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = 0
	})
	clk := sb.App.Clock.(*clock.Manual)
	id := sb.CreatePayment(nil, scenario.Header, "delayed_callback; delay=10m")["id"].(string)

	sb.WaitStatus(id, "captured")
	waitTimers(t, clk)
	clk.Advance(9 * time.Minute)
	sb.Receiver.Quiet(0, 50*time.Millisecond)

	clk.Advance(time.Minute)
	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.captured" || cb.Data["id"] != id {
		t.Fatalf("callback = %s", cb.Body)
	}
}

func TestDelayedCallbackRealClock(t *testing.T) {
	sb := sandboxtest.New(t)
	start := time.Now()
	sb.CreatePayment(nil, scenario.Header, "delayed_callback; delay=150ms")
	cb := sb.Receiver.Wait(1)[0]
	if d := cb.Received.Sub(start); d < 150*time.Millisecond {
		t.Fatalf("callback after %v, want at least 150ms", d)
	}
}

func waitTimers(t *testing.T, m *clock.Manual) {
	t.Helper()
	deadline := time.Now().Add(sandboxtest.Wait)
	for m.Pending() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nothing was scheduled on the manual clock")
		}
		time.Sleep(time.Millisecond)
	}
}
