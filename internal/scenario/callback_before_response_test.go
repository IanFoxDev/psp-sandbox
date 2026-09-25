package scenario_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

// The application gets the callback for a payment it has not stored yet,
// because the create call has not returned.
func TestCallbackBeforeResponse(t *testing.T) {
	sb := sandboxtest.New(t)

	resp := sb.Do(http.MethodPost, "/v1/payments", map[string]any{"amount": 1000, "currency": "EUR"},
		scenario.Header, "callback_before_response; lead=100ms")
	answered := time.Now()
	if resp.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.Status, resp.Body)
	}
	p := resp.JSON(t)
	if p["status"] != "pending" {
		t.Fatalf("create response says %v, want pending", p["status"])
	}

	cbs := sb.Receiver.All()
	if len(cbs) != 1 {
		t.Fatalf("%d callbacks before the create response, want 1", len(cbs))
	}
	if cbs[0].Data["id"] != p["id"] || cbs[0].Type != "payment.captured" {
		t.Fatalf("callback = %s", cbs[0].Body)
	}
	if lead := answered.Sub(cbs[0].Received); lead < 90*time.Millisecond {
		t.Fatalf("callback came %v before the response, want at least 100ms", lead)
	}
	if got := sb.Payment(p["id"].(string)); got["status"] != "captured" {
		t.Fatalf("status = %v", got["status"])
	}
}
