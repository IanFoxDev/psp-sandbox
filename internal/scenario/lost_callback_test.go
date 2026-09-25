package scenario_test

import (
	"testing"
	"time"

	"github.com/ghuser/psp-sandbox/internal/callback"
	"github.com/ghuser/psp-sandbox/internal/sandboxtest"
	"github.com/ghuser/psp-sandbox/internal/scenario"
)

// The application must poll GET to learn the outcome.
func TestLostCallback(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(nil, scenario.Header, "lost_callback")["id"].(string)

	sb.WaitStatus(id, "captured")
	sb.Receiver.Quiet(0, 100*time.Millisecond)

	dels := sb.App.Dispatcher.Deliveries(id)
	if len(dels) != 1 || dels[0].Status != callback.StatusDropped || dels[0].EventType != "payment.captured" {
		t.Fatalf("deliveries = %+v", dels)
	}
}
