package scenario_test

import (
	"net/http"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

func TestDeclined(t *testing.T) {
	sb := sandboxtest.New(t)
	p := sb.CreatePayment(nil, scenario.Header, "declined; reason=expired_card")
	if p["status"] != "pending" || p["scenario"] != "declined" {
		t.Fatalf("create response = %v", p)
	}

	cb := sb.Receiver.Wait(1)[0]
	if cb.Type != "payment.failed" || cb.Data["failure_reason"] != "expired_card" || !cb.Signed {
		t.Fatalf("callback = %s", cb.Body)
	}
	got := sb.WaitStatus(p["id"].(string), "failed")
	if got["captured_amount"] != 0.0 {
		t.Fatalf("declined payment has captured_amount %v", got["captured_amount"])
	}

	resp := sb.Do(http.MethodPost, "/v1/payments/"+p["id"].(string)+"/capture", nil)
	if resp.Status != http.StatusConflict {
		t.Fatalf("capture of a declined payment: %d %s", resp.Status, resp.Body)
	}
}

func TestDeclinedDefaultReason(t *testing.T) {
	sb := sandboxtest.New(t)
	sb.CreatePayment(nil, scenario.Header, "declined")
	if cb := sb.Receiver.Wait(1)[0]; cb.Data["failure_reason"] != "insufficient_funds" {
		t.Fatalf("callback = %s", cb.Body)
	}
}

func TestDeclinedUnknownReason(t *testing.T) {
	sb := sandboxtest.New(t)
	resp := sb.Do(http.MethodPost, "/v1/payments", map[string]any{"amount": 1, "currency": "EUR"},
		scenario.Header, "declined; reason=bad_luck")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("unknown reason: %d %s", resp.Status, resp.Body)
	}
}
