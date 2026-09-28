package api_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/api"
	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

// Create a payment, see it change status, receive a signed callback.
func TestHappyPathEndToEnd(t *testing.T) {
	sb := sandboxtest.New(t)

	p := sb.CreatePayment(map[string]any{"reference": "order-42", "metadata": map[string]string{"customer_id": "c_1"}})
	if p["status"] != "pending" || p["scenario"] != "happy_path" || p["amount"] != 1000.0 {
		t.Fatalf("create response = %v", p)
	}
	id := p["id"].(string)

	cb := sb.Receiver.Wait(1)[0]
	if !cb.Signed {
		t.Fatalf("callback signature does not verify: %v", cb.Header)
	}
	if cb.Type != "payment.captured" || cb.Data["id"] != id || cb.Data["status"] != "captured" || cb.Data["captured_amount"] != 1000.0 {
		t.Fatalf("callback = %s", cb.Body)
	}
	if d := time.Since(time.Unix(cb.Timestamp, 0)); d < -time.Minute || d > time.Minute {
		t.Fatalf("webhook-timestamp is %v away from now", d)
	}

	got := sb.Payment(id)
	if got["status"] != "captured" || got["reference"] != "order-42" {
		t.Fatalf("payment = %v", got)
	}
	if got["metadata"].(map[string]any)["customer_id"] != "c_1" {
		t.Fatalf("metadata = %v", got["metadata"])
	}

	sb.Receiver.Quiet(1, 50*time.Millisecond)
	dels := sb.App.Dispatcher.Deliveries(id)
	if len(dels) != 1 || dels[0].Status != "succeeded" || dels[0].EventID != cb.ID {
		t.Fatalf("deliveries = %+v", dels)
	}
}

func TestManualCapture(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(map[string]any{"capture": "manual"})["id"].(string)

	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.authorized" {
		t.Fatalf("first callback = %s", cb.Type)
	}
	sb.WaitStatus(id, "authorized")

	for _, amount := range []int{0, -1} {
		resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/capture", map[string]any{"amount": amount})
		if resp.Status != http.StatusBadRequest || errorCode(t, resp) != "invalid_request" {
			t.Fatalf("capture %d: %d %s", amount, resp.Status, resp.Body)
		}
	}

	resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/capture", map[string]any{"amount": 700})
	if resp.Status != http.StatusOK || resp.JSON(t)["captured_amount"] != 700.0 {
		t.Fatalf("capture: %d %s", resp.Status, resp.Body)
	}
	if cb := sb.Receiver.Wait(2)[1]; cb.Type != "payment.captured" || cb.Data["captured_amount"] != 700.0 {
		t.Fatalf("second callback = %s", cb.Body)
	}

	resp = sb.Do(http.MethodPost, "/v1/payments/"+id+"/capture", nil)
	if resp.Status != http.StatusConflict || errorCode(t, resp) != "invalid_state" {
		t.Fatalf("second capture: %d %s", resp.Status, resp.Body)
	}
}

func TestCancelBeforeProcessing(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.ProcessingDelay = 100 * time.Millisecond })
	id := sb.CreatePayment(nil)["id"].(string)

	resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/cancel", nil)
	if resp.Status != http.StatusOK || resp.JSON(t)["status"] != "canceled" {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.canceled" {
		t.Fatalf("callback = %s", cb.Type)
	}
	// The scheduled capture must not happen after the cancel.
	sb.Receiver.Quiet(1, 200*time.Millisecond)
	if got := sb.Payment(id); got["status"] != "canceled" {
		t.Fatalf("status = %v", got["status"])
	}
}

func TestRefunds(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(nil)["id"].(string)
	sb.WaitStatus(id, "captured")

	resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/refunds", map[string]any{"amount": 300, "reference": "refund-1"})
	if resp.Status != http.StatusCreated {
		t.Fatalf("refund: %d %s", resp.Status, resp.Body)
	}
	ref := resp.JSON(t)
	if ref["status"] != "pending" || ref["payment_id"] != id || ref["amount"] != 300.0 {
		t.Fatalf("refund = %v", ref)
	}

	resp = sb.Do(http.MethodPost, "/v1/payments/"+id+"/refunds", map[string]any{"amount": 701})
	if resp.Status != http.StatusUnprocessableEntity || errorCode(t, resp) != "amount_exceeds_captured" {
		t.Fatalf("over-refund: %d %s", resp.Status, resp.Body)
	}

	cbs := sb.Receiver.Wait(2)
	if cbs[1].Type != "refund.succeeded" || cbs[1].Data["id"] != ref["id"] || cbs[1].Data["status"] != "succeeded" {
		t.Fatalf("refund callback = %s", cbs[1].Body)
	}
	p := sb.WaitStatus(id, "partially_refunded")
	if p["refunded_amount"] != 300.0 {
		t.Fatalf("payment = %v", p)
	}
}

func TestRefundOfPendingPayment(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.ProcessingDelay = time.Hour })
	id := sb.CreatePayment(nil)["id"].(string)
	resp := sb.Do(http.MethodPost, "/v1/payments/"+id+"/refunds", map[string]any{"amount": 100})
	if resp.Status != http.StatusConflict || errorCode(t, resp) != "invalid_state" {
		t.Fatalf("refund of pending: %d %s", resp.Status, resp.Body)
	}
}

func TestIdempotencyKey(t *testing.T) {
	sb := sandboxtest.New(t)
	body := map[string]any{"amount": 1000, "currency": "EUR", "reference": "order-7"}

	first := sb.Do(http.MethodPost, "/v1/payments", body, api.HeaderIdempotencyKey, "k-1")
	second := sb.Do(http.MethodPost, "/v1/payments", body, api.HeaderIdempotencyKey, "k-1")
	if first.Status != http.StatusCreated || second.Status != http.StatusCreated {
		t.Fatalf("statuses %d and %d", first.Status, second.Status)
	}
	if string(first.Body) != string(second.Body) || second.Header.Get(api.HeaderReplayed) != "true" {
		t.Fatalf("replay differs:\n%s\n%s", first.Body, second.Body)
	}

	body["amount"] = 2000
	conflict := sb.Do(http.MethodPost, "/v1/payments", body, api.HeaderIdempotencyKey, "k-1")
	if conflict.Status != http.StatusConflict || errorCode(t, conflict) != "idempotency_conflict" {
		t.Fatalf("different body: %d %s", conflict.Status, conflict.Body)
	}

	if n := len(sb.Do(http.MethodGet, "/v1/payments?reference=order-7", nil).JSON(t)["data"].([]any)); n != 1 {
		t.Fatalf("%d payments created, want 1", n)
	}
}

// A client that retries in a burst must not create two payments.
func TestIdempotencyKeyConcurrentRetries(t *testing.T) {
	sb := sandboxtest.New(t)
	body := map[string]any{"amount": 1000, "currency": "EUR", "reference": "burst"}

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := sb.Do(http.MethodPost, "/v1/payments", body, api.HeaderIdempotencyKey, "burst-1")
			if resp.Status != http.StatusCreated && resp.Status != http.StatusConflict {
				t.Errorf("status %d %s", resp.Status, resp.Body)
			}
		}()
	}
	wg.Wait()
	if n := len(sb.Do(http.MethodGet, "/v1/payments?reference=burst", nil).JSON(t)["data"].([]any)); n != 1 {
		t.Fatalf("%d payments created, want 1", n)
	}
}

func TestInvalidRequests(t *testing.T) {
	sb := sandboxtest.New(t)
	cases := []struct {
		name   string
		body   any
		header []string
	}{
		{"zero amount", map[string]any{"amount": 0, "currency": "EUR"}, nil},
		{"float amount", map[string]any{"amount": 10.5, "currency": "EUR"}, nil},
		{"lowercase currency", map[string]any{"amount": 1, "currency": "eur"}, nil},
		{"unknown field", map[string]any{"amount": 1, "currency": "EUR", "amout": 1}, nil},
		{"bad capture", map[string]any{"amount": 1, "currency": "EUR", "capture": "later"}, nil},
		{"bad callback url", map[string]any{"amount": 1, "currency": "EUR", "callback_url": "/cb"}, nil},
		{"unknown scenario", map[string]any{"amount": 1, "currency": "EUR"}, []string{scenario.Header, "nope"}},
		{"bad scenario param", map[string]any{"amount": 1, "currency": "EUR"}, []string{scenario.Header, "happy_path; x=1"}},
	}
	for _, c := range cases {
		resp := sb.Do(http.MethodPost, "/v1/payments", c.body, c.header...)
		if resp.Status != http.StatusBadRequest || errorCode(t, resp) != "invalid_request" {
			t.Errorf("%s: %d %s", c.name, resp.Status, resp.Body)
		}
	}
	if resp := sb.Do(http.MethodGet, "/v1/payments/pay_missing", nil); resp.Status != http.StatusNotFound || errorCode(t, resp) != "not_found" {
		t.Errorf("unknown payment: %d %s", resp.Status, resp.Body)
	}
}

func TestAPIKey(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.APIKey = "sk_test_1" })
	body := map[string]any{"amount": 1, "currency": "EUR"}

	if resp := sb.Do(http.MethodPost, "/v1/payments", body); resp.Status != http.StatusUnauthorized {
		t.Fatalf("without key: %d", resp.Status)
	}
	if resp := sb.Do(http.MethodPost, "/v1/payments", body, "Authorization", "Bearer wrong"); resp.Status != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", resp.Status)
	}
	if resp := sb.Do(http.MethodPost, "/v1/payments", body, "Authorization", "Bearer sk_test_1"); resp.Status != http.StatusCreated {
		t.Fatalf("right key: %d %s", resp.Status, resp.Body)
	}
}

func TestPerPaymentCallbackURL(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.CallbackURL = "" })
	other := sandboxtest.NewReceiver(t)
	sb.CreatePayment(map[string]any{"callback_url": other.URL()})
	if cb := other.Wait(1)[0]; cb.Type != "payment.captured" || !cb.Signed {
		t.Fatalf("callback = %s", cb.Body)
	}
}

// With a manual clock nothing happens until the test moves time forward.
func TestManualClock(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = time.Hour
	})
	id := sb.CreatePayment(nil)["id"].(string)

	sb.Receiver.Quiet(0, 50*time.Millisecond)
	if got := sb.Payment(id); got["status"] != "pending" {
		t.Fatalf("status before advance = %v", got["status"])
	}
	sb.App.Clock.(*clock.Manual).Advance(time.Hour)
	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.captured" {
		t.Fatalf("callback = %s", cb.Type)
	}
}

func errorCode(t *testing.T, r sandboxtest.Response) string {
	t.Helper()
	e, _ := r.JSON(t)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
