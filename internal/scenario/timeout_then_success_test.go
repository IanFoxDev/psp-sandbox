package scenario_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ghuser/psp-sandbox/internal/api"
	"github.com/ghuser/psp-sandbox/internal/sandboxtest"
	"github.com/ghuser/psp-sandbox/internal/scenario"
)

func TestTimeoutThenSuccessHold(t *testing.T) {
	sb := sandboxtest.New(t)
	body := map[string]any{"amount": 1000, "currency": "EUR", "reference": "slow-1"}
	headers := []string{scenario.Header, "timeout_then_success; delay=2s", api.HeaderIdempotencyKey, "slow-1"}

	if _, err := sb.DoWithTimeout(100*time.Millisecond, http.MethodPost, "/v1/payments", body, headers...); err == nil {
		t.Fatal("the create call answered within the client timeout")
	}

	// The client never learned the payment id, yet the payment went through.
	cb := sb.Receiver.Wait(1)[0]
	if cb.Type != "payment.captured" {
		t.Fatalf("callback = %s", cb.Body)
	}

	retry := sb.Do(http.MethodPost, "/v1/payments", body, headers...)
	if retry.Status != http.StatusCreated || retry.Header.Get(api.HeaderReplayed) != "true" {
		t.Fatalf("retry: %d %s", retry.Status, retry.Body)
	}
	if id := retry.JSON(t)["id"]; id != cb.Data["id"] {
		t.Fatalf("retry returned %v, callback was for %v", id, cb.Data["id"])
	}
	if n := len(sb.Do(http.MethodGet, "/v1/payments?reference=slow-1", nil).JSON(t)["data"].([]any)); n != 1 {
		t.Fatalf("%d payments, want 1", n)
	}
}

func TestTimeoutThenSuccessReset(t *testing.T) {
	sb := sandboxtest.New(t)
	body := map[string]any{"amount": 1000, "currency": "EUR", "reference": "reset-1"}
	headers := []string{scenario.Header, "timeout_then_success; mode=reset", api.HeaderIdempotencyKey, "reset-1"}

	if resp, err := sb.DoWithTimeout(2*time.Second, http.MethodPost, "/v1/payments", body, headers...); err == nil {
		t.Fatalf("got a response %d, want a closed connection", resp.Status)
	}

	cb := sb.Receiver.Wait(1)[0]
	retry := sb.Do(http.MethodPost, "/v1/payments", body, headers...)
	if retry.Status != http.StatusCreated || retry.JSON(t)["id"] != cb.Data["id"] {
		t.Fatalf("retry: %d %s, callback for %v", retry.Status, retry.Body, cb.Data["id"])
	}
}
