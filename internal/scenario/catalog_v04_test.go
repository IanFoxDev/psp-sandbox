package scenario_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

func TestInvalidSignature(t *testing.T) {
	for _, mode := range []string{"wrong_secret", "stale_timestamp", "missing"} {
		t.Run(mode, func(t *testing.T) {
			sb := sandboxtest.New(t)
			sb.CreatePayment(nil, scenario.Header, "invalid_signature; mode="+mode)
			cb := sb.Receiver.Wait(1)[0]
			// A stale signature is valid HMAC; receivers reject it for its
			// age, which this receiver does not check.
			if cb.Signed != (mode == "stale_timestamp") || cb.Type != "payment.captured" {
				t.Fatalf("callback %s signed=%v: %v", cb.Type, cb.Signed, cb.Header)
			}
			sig := cb.Header.Get("webhook-signature")
			switch mode {
			case "missing":
				if sig != "" {
					t.Errorf("signature %q", sig)
				}
			case "stale_timestamp":
				if age := time.Since(time.Unix(cb.Timestamp, 0)); age < 9*time.Minute || age > 11*time.Minute {
					t.Errorf("timestamp is %v old", age)
				}
			case "wrong_secret":
				if sig == "" {
					t.Error("no signature at all")
				}
			}
		})
	}
}

func TestAckIgnored(t *testing.T) {
	sb := sandboxtest.New(t) // three attempts: 0s, 50ms, 100ms
	p := sb.CreatePayment(nil, scenario.Header, "ack_ignored; times=2")
	got := sb.Receiver.Wait(3)
	if got[0].ID != got[1].ID || got[1].ID != got[2].ID {
		t.Fatalf("not the same event: %s %s %s", got[0].ID, got[1].ID, got[2].ID)
	}
	sb.Receiver.Quiet(3, 200*time.Millisecond)
	r := sb.Do(http.MethodGet, "/_sandbox/payments/"+p["id"].(string)+"/deliveries", nil).JSON(t)
	del := r["data"].([]any)[0].(map[string]any)
	attempts := del["attempts"].([]any)
	if del["status"] != "succeeded" || len(attempts) != 3 || attempts[0].(map[string]any)["status_code"] != 200.0 {
		t.Fatalf("delivery %v", del)
	}

	more := sandboxtest.New(t)
	q := more.CreatePayment(nil, scenario.Header, "ack_ignored; times=5")
	more.Receiver.Wait(3)
	deadline := time.Now().Add(sandboxtest.Wait)
	for {
		d := more.Do(http.MethodGet, "/_sandbox/payments/"+q["id"].(string)+"/deliveries", nil).JSON(t)["data"].([]any)[0].(map[string]any)
		if d["status"] == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("more ignored answers than attempts: delivery is %v", d["status"])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAmountMismatch(t *testing.T) {
	sb := sandboxtest.New(t)
	cases := map[string]float64{"amount_mismatch": 999, "amount_mismatch; delta=50": 1050, "amount_mismatch; delta=-5000": 1}
	for spec, want := range cases {
		p := sb.CreatePayment(nil, scenario.Header, spec)
		got := sb.WaitStatus(p["id"].(string), "captured")
		if got["captured_amount"] != want || got["amount"] != 1000.0 {
			t.Errorf("%s: captured %v of %v", spec, got["captured_amount"], got["amount"])
		}
	}
	for _, cb := range sb.Receiver.Wait(3) {
		if cb.Data["captured_amount"] == cb.Data["amount"] {
			t.Errorf("callback carries the requested amount: %s", cb.Body)
		}
	}
	if r := sb.Do(http.MethodPost, "/v1/payments", map[string]any{"amount": 1000, "currency": "EUR"},
		scenario.Header, "amount_mismatch; delta=0"); r.Status != http.StatusBadRequest {
		t.Errorf("delta=0: %d %s", r.Status, r.Body)
	}
	m := sb.CreatePayment(map[string]any{"capture": "manual"}, scenario.Header, "amount_mismatch")
	if got := sb.WaitStatus(m["id"].(string), "authorized"); got["captured_amount"] != 0.0 {
		t.Errorf("manual capture: %v", got)
	}
}

func TestStatusRegression(t *testing.T) {
	sb := sandboxtest.New(t)
	p := sb.CreatePayment(nil, scenario.Header, "status_regression; delay=100ms")
	got := sb.Receiver.Wait(2)
	if got[0].Type != "payment.captured" || got[1].Type != "payment.failed" || got[1].Data["status"] != "failed" || !got[1].Signed {
		t.Fatalf("callbacks %s, %s", got[0].Body, got[1].Body)
	}
	if now := sb.Payment(p["id"].(string)); now["status"] != "captured" {
		t.Errorf("the payment itself is %v, want captured", now["status"])
	}
}
