package stripe_test

import (
	"strings"
	"testing"
)

func (s *stripeBox) paid(extra string) (id, charge string) {
	s.t.Helper()
	r := s.create(visa + extra)
	if r.status != 200 {
		s.t.Fatalf("create: %d %v", r.status, r.body)
	}
	return r.body["id"].(string), r.body["latest_charge"].(string)
}

func TestFullRefund(t *testing.T) {
	s := newStripe(t)
	id, charge := s.paid("")
	r := s.form("POST", "/v1/refunds", "payment_intent="+id+"&reason=requested_by_customer&metadata[ticket]=7")
	b := r.body
	if r.status != 200 || b["object"] != "refund" || b["status"] != "succeeded" || b["amount"] != 1000.0 ||
		b["charge"] != charge || b["payment_intent"] != id || b["reason"] != "requested_by_customer" ||
		b["metadata"].(map[string]any)["ticket"] != "7" || !strings.HasPrefix(b["id"].(string), "re_") {
		t.Fatalf("%d %v", r.status, b)
	}
	if got := s.form("GET", "/v1/refunds/"+b["id"].(string), ""); got.body["status"] != "succeeded" || got.body["amount"] != 1000.0 {
		t.Errorf("retrieve: %v", got.body)
	}
	ch := s.form("GET", "/v1/charges/"+charge, "")
	if ch.body["refunded"] != true || ch.body["amount_refunded"] != 1000.0 {
		t.Errorf("charge after refund: %v", ch.body)
	}
	if pi := s.form("GET", "/v1/payment_intents/"+id, ""); pi.body["status"] != "succeeded" {
		t.Errorf("intent after refund: %v", pi.body["status"])
	}
	if r := s.form("POST", "/v1/refunds", "payment_intent="+id); r.status != 400 || r.err()["code"] != "charge_already_refunded" {
		t.Errorf("second refund: %d %v", r.status, r.body)
	}
}

func TestPartialRefunds(t *testing.T) {
	s := newStripe(t)
	id, charge := s.paid("")
	if r := s.form("POST", "/v1/refunds", "charge="+charge+"&amount=300"); r.body["amount"] != 300.0 {
		t.Fatalf("by charge: %v", r.body)
	}
	if r := s.form("POST", "/v1/refunds", "payment_intent="+id+"&amount=800"); r.status != 400 || r.err()["code"] != "amount_too_large" {
		t.Errorf("too much: %d %v", r.status, r.body)
	}
	if r := s.form("POST", "/v1/refunds", "payment_intent="+id); r.body["amount"] != 700.0 {
		t.Errorf("the rest by default: %v", r.body)
	}
	if ch := s.form("GET", "/v1/charges/"+charge, ""); ch.body["refunded"] != true || ch.body["amount_refunded"] != 1000.0 {
		t.Errorf("charge: %v", ch.body)
	}
}

func TestRefundRejections(t *testing.T) {
	s := newStripe(t)
	held := s.create(visa + "&capture_method=manual").body["id"].(string)
	declined := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclined").err()["payment_intent"].(map[string]any)
	retried := declined["id"].(string)
	failedCharge := declined["latest_charge"].(string)
	s.form("POST", "/v1/payment_intents/"+retried+"/confirm", "payment_method=pm_card_visa")
	id, _ := s.paid("")

	cases := []struct {
		body, code string
		status     int
	}{
		{"payment_intent=" + held, "charge_not_refundable", 400},
		{"charge=" + failedCharge, "charge_not_refundable", 400},
		{"payment_intent=pi_nope", "resource_missing", 404},
		{"charge=ch_nope", "resource_missing", 404},
		{"", "parameter_missing", 400},
		{"payment_intent=" + id + "&charge=ch_x", "parameter_missing", 400},
		{"payment_intent=" + id + "&amount=0", "parameter_invalid_integer", 400},
		{"payment_intent=" + id + "&reason=bored", "parameter_invalid_string", 400},
	}
	for _, c := range cases {
		r := s.form("POST", "/v1/refunds", c.body)
		if r.status != c.status || r.err()["code"] != c.code {
			t.Errorf("%q: %d %v", c.body, r.status, r.body)
		}
	}
	if r := s.form("GET", "/v1/refunds/re_nope", ""); r.status != 404 {
		t.Errorf("unknown refund: %d", r.status)
	}
}

func TestIdempotentRefund(t *testing.T) {
	s := newStripe(t)
	id, _ := s.paid("")
	first := s.form("POST", "/v1/refunds", "payment_intent="+id+"&amount=100", "Idempotency-Key", "refund-1")
	again := s.form("POST", "/v1/refunds", "payment_intent="+id+"&amount=100", "Idempotency-Key", "refund-1")
	if again.body["id"] != first.body["id"] || again.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay: %v / %v", first.body["id"], again.body["id"])
	}
	if ch := s.form("GET", "/v1/payment_intents/"+id+"?expand[]=latest_charge", ""); ch.body["latest_charge"].(map[string]any)["amount_refunded"] != 100.0 {
		t.Errorf("refunded twice: %v", ch.body["latest_charge"])
	}
}
