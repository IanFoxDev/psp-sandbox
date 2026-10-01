package scenario_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

// A day after capture the chargeback opens, three days later it is lost.
// The manual clock gets the test there without waiting.
func TestChargebackAfter(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = 0
	})
	clk := sb.App.Clock.(*clock.Manual)
	id := sb.CreatePayment(nil, scenario.Header, "chargeback_after; delay=24h; close_after=72h")["id"].(string)

	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.captured" {
		t.Fatalf("first callback = %s", cb.Body)
	}
	clk.Advance(23 * time.Hour)
	sb.Receiver.Quiet(1, 50*time.Millisecond)
	if p := sb.Payment(id); p["status"] != "captured" {
		t.Fatalf("status before the delay = %v", p["status"])
	}

	clk.Advance(time.Hour)
	if p := sb.Payment(id); p["status"] != "disputed" {
		t.Fatalf("status after the delay = %v", p["status"])
	}
	if cb := sb.Receiver.Wait(2)[1]; cb.Type != "chargeback.opened" || cb.Data["id"] != id || !cb.Signed {
		t.Fatalf("second callback = %s", cb.Body)
	}

	clk.Advance(72 * time.Hour)
	if p := sb.Payment(id); p["status"] != "chargeback_lost" {
		t.Fatalf("status after close_after = %v", p["status"])
	}
	if cb := sb.Receiver.Wait(3)[2]; cb.Type != "chargeback.closed" || cb.Data["status"] != "chargeback_lost" {
		t.Fatalf("third callback = %s", cb.Body)
	}
}

func TestChargebackAfterWon(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(nil, scenario.Header, "chargeback_after; delay=20ms; close_after=20ms; outcome=won")["id"].(string)

	cbs := sb.Receiver.Wait(3)
	for i, want := range []string{"payment.captured", "chargeback.opened", "chargeback.closed"} {
		if cbs[i].Type != want {
			t.Fatalf("callback %d = %s, want %s", i, cbs[i].Type, want)
		}
	}
	sb.WaitStatus(id, "chargeback_won")
}

// With manual capture the delays still count from creation. A payment that is
// only authorized when the chargeback falls due gets none.
func TestChargebackAfterManualCapture(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = 0
	})
	clk := sb.App.Clock.(*clock.Manual)
	fields := map[string]any{"capture": "manual"}
	header := "chargeback_after; delay=1h; close_after=1h"
	captured := sb.CreatePayment(fields, scenario.Header, header)["id"].(string)
	authorized := sb.CreatePayment(fields, scenario.Header, header)["id"].(string)
	sb.Receiver.Wait(2)

	resp := sb.Do(http.MethodPost, "/v1/payments/"+captured+"/capture", nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("capture: %d %s", resp.Status, resp.Body)
	}
	sb.Receiver.Wait(3)

	clk.Advance(2 * time.Hour)
	if p := sb.Payment(captured); p["status"] != "chargeback_lost" {
		t.Fatalf("captured payment ended as %v", p["status"])
	}
	if p := sb.Payment(authorized); p["status"] != "authorized" {
		t.Fatalf("authorized payment ended as %v", p["status"])
	}
	sb.Receiver.Quiet(5, 50*time.Millisecond)
}

func TestChargebackAfterUnknownOutcome(t *testing.T) {
	sb := sandboxtest.New(t)
	resp := sb.Do(http.MethodPost, "/v1/payments", map[string]any{"amount": 1, "currency": "EUR"},
		scenario.Header, "chargeback_after; outcome=draw")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("unknown outcome: %d %s", resp.Status, resp.Body)
	}
}
