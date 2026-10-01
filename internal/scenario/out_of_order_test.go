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

// Authorize, capture and refund within the window: the application hears about
// the refund first and about the authorization last.
func TestOutOfOrder(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = 0
	})
	clk := sb.App.Clock.(*clock.Manual)
	id := sb.CreatePayment(map[string]any{"capture": "manual"}, scenario.Header, "out_of_order; window=1m")["id"].(string)

	if resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/capture", nil); resp.Status != http.StatusOK {
		t.Fatalf("capture: %d %s", resp.Status, resp.Body)
	}
	if resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/refunds", map[string]any{"amount": 300}); resp.Status != http.StatusCreated {
		t.Fatalf("refund: %d %s", resp.Status, resp.Body)
	}
	sb.WaitStatus(id, "partially_refunded")
	sb.Receiver.Quiet(0, 50*time.Millisecond)

	clk.Advance(time.Minute)
	cbs := sb.Receiver.Wait(3)
	for i, want := range []string{"refund.succeeded", "payment.captured", "payment.authorized"} {
		if cbs[i].Type != want || !cbs[i].Signed {
			t.Fatalf("callback %d = %s, want %s", i, cbs[i].Type, want)
		}
	}
}

func TestOutOfOrderRealClock(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(nil, scenario.Header, "out_of_order; window=1s")["id"].(string)
	sb.WaitStatus(id, "captured")
	resp := sb.Do(http.MethodPost, "/_sandbox/payments/"+id+"/events", map[string]any{"type": "chargeback.opened"})
	if resp.Status != http.StatusCreated {
		t.Fatalf("force: %d %s", resp.Status, resp.Body)
	}
	cbs := sb.Receiver.Wait(2)
	if cbs[0].Type != "chargeback.opened" || cbs[1].Type != "payment.captured" {
		t.Fatalf("callbacks %s, %s", cbs[0].Type, cbs[1].Type)
	}
}

func TestOutOfOrderZeroWindow(t *testing.T) {
	sb := sandboxtest.New(t)
	resp := sb.Do(http.MethodPost, "/v1/payments", map[string]any{"amount": 1, "currency": "EUR"},
		scenario.Header, "out_of_order; window=0s")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("window=0s: %d %s", resp.Status, resp.Body)
	}
}
