package scenario_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

func authenticate(t *testing.T, sb *sandboxtest.Sandbox, id, result string) sandboxtest.Response {
	t.Helper()
	return sb.Do(http.MethodPost, "/_sandbox/payments/"+id+"/authenticate", map[string]any{"result": result})
}

// waitAction creates a 3DS payment and waits until it asks for the customer.
func waitAction(t *testing.T, sb *sandboxtest.Sandbox, spec string) string {
	t.Helper()
	p := sb.CreatePayment(map[string]any{"return_url": "https://shop.test/return"}, scenario.Header, spec)
	id := p["id"].(string)
	got := sb.WaitStatus(id, "requires_action")
	if got["action_url"] != "http://sandbox.test/_sandbox/ui/3ds/"+id {
		t.Fatalf("action_url = %v", got["action_url"])
	}
	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.action_required" || cb.Data["action_url"] == nil || !cb.Signed {
		t.Fatalf("first callback = %s", cb.Body)
	}
	return id
}

func TestThreeDSecureSucceeds(t *testing.T) {
	sb := sandboxtest.New(t)
	id := waitAction(t, sb, "three_d_secure")
	sb.Receiver.Quiet(1, 50*time.Millisecond)

	r := authenticate(t, sb, id, "success")
	p := r.JSON(t)
	if r.Status != http.StatusOK || p["status"] != "captured" || p["action_url"] != nil {
		t.Fatalf("authenticate: %d %v", r.Status, p)
	}
	if cb := sb.Receiver.Wait(2)[1]; cb.Type != "payment.captured" {
		t.Fatalf("second callback = %s", cb.Body)
	}
	if r := authenticate(t, sb, id, "success"); r.Status != http.StatusConflict {
		t.Fatalf("second authenticate: %d %s", r.Status, r.Body)
	}
}

func TestThreeDSecureFailedAuthentication(t *testing.T) {
	sb := sandboxtest.New(t)
	id := waitAction(t, sb, "three_d_secure")
	p := authenticate(t, sb, id, "failure").JSON(t)
	if p["status"] != "failed" || p["failure_reason"] != "authentication_failed" {
		t.Fatalf("after failure: %v", p)
	}
	if cb := sb.Receiver.Wait(2)[1]; cb.Type != "payment.failed" {
		t.Fatalf("second callback = %s", cb.Body)
	}
}

func TestThreeDSecureDeclinedAfterAuthentication(t *testing.T) {
	sb := sandboxtest.New(t)
	id := waitAction(t, sb, "three_d_secure; outcome=declined")
	if p := authenticate(t, sb, id, "success").JSON(t); p["status"] != "failed" || p["failure_reason"] != "generic_decline" {
		t.Fatalf("after authentication: %v", p)
	}
}

func TestThreeDSecureManualCapture(t *testing.T) {
	sb := sandboxtest.New(t)
	p := sb.CreatePayment(map[string]any{"capture": "manual"}, scenario.Header, "three_d_secure")
	id := p["id"].(string)
	sb.WaitStatus(id, "requires_action")
	if p := authenticate(t, sb, id, "success").JSON(t); p["status"] != "authorized" {
		t.Fatalf("manual capture after authentication: %v", p)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	sb := sandboxtest.New(t)
	plain := sb.CreatePayment(nil)
	sb.WaitStatus(plain["id"].(string), "captured")
	if r := authenticate(t, sb, plain["id"].(string), "success"); r.Status != http.StatusConflict ||
		!strings.Contains(string(r.Body), "requires action") {
		t.Errorf("not waiting: %d %s", r.Status, r.Body)
	}
	if r := authenticate(t, sb, "pay_missing", "success"); r.Status != http.StatusNotFound {
		t.Errorf("unknown: %d", r.Status)
	}
	if r := authenticate(t, sb, plain["id"].(string), "maybe"); r.Status != http.StatusBadRequest {
		t.Errorf("bad result: %d", r.Status)
	}
	r := sb.Do(http.MethodPost, "/v1/payments", map[string]any{"amount": 1, "currency": "EUR", "return_url": "/back"})
	if r.Status != http.StatusBadRequest {
		t.Errorf("relative return_url: %d %s", r.Status, r.Body)
	}
}
