package stripe_test

import (
	"slices"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
)

// events lists the event types of one payment intent, oldest first.
func (s *stripeBox) events(query, intent string) []map[string]any {
	s.t.Helper()
	r := s.form("GET", "/v1/events?limit=100"+query, "")
	if r.status != 200 || r.body["object"] != "list" {
		s.t.Fatalf("events: %d %v", r.status, r.body)
	}
	var out []map[string]any
	for _, e := range r.body["data"].([]any) {
		ev := e.(map[string]any)
		obj := ev["data"].(map[string]any)["object"].(map[string]any)
		if obj["id"] == intent || obj["payment_intent"] == intent {
			out = append(out, ev)
		}
	}
	slices.Reverse(out)
	return out
}

func types(evs []map[string]any) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e["type"].(string)
	}
	return out
}

func TestEventsOfAPayment(t *testing.T) {
	s := newStripe(t)
	id, _ := s.paid("")
	evs := s.events("", id)
	want := []string{"payment_intent.created", "charge.succeeded", "payment_intent.succeeded"}
	if got := types(evs); !slices.Equal(got, want) {
		t.Fatalf("types %v, want %v", got, want)
	}
	for _, e := range evs {
		if e["object"] != "event" || e["api_version"] != "2026-09-30.endive" || e["livemode"] != false ||
			e["pending_webhooks"] != 1.0 {
			t.Errorf("event fields: %v", e)
		}
		got := s.form("GET", "/v1/events/"+e["id"].(string), "")
		if got.status != 200 || got.body["type"] != e["type"] {
			t.Errorf("retrieve %s: %d %v", e["id"], got.status, got.body)
		}
	}
	if evs[2]["id"] != evs[1]["id"].(string)+"_2" {
		t.Errorf("one change gives evt_X and evt_X_2: %s %s", evs[1]["id"], evs[2]["id"])
	}
	succeeded := evs[2]["data"].(map[string]any)["object"].(map[string]any)
	if succeeded["status"] != "succeeded" || succeeded["object"] != "payment_intent" {
		t.Errorf("snapshot: %v", succeeded)
	}
	for _, missing := range []string{"evt_nope", evs[1]["id"].(string) + "_3", evs[0]["id"].(string) + "_2"} {
		if r := s.form("GET", "/v1/events/"+missing, ""); r.status != 404 {
			t.Errorf("%s: %d", missing, r.status)
		}
	}
}

func TestEventsOfCaptureDeclineAndRefund(t *testing.T) {
	s := newStripe(t)
	held := s.create(visa + "&capture_method=manual").body["id"].(string)
	s.form("POST", "/v1/payment_intents/"+held+"/capture", "")
	want := []string{"payment_intent.created", "charge.succeeded", "payment_intent.amount_capturable_updated",
		"charge.captured", "payment_intent.succeeded"}
	if got := types(s.events("", held)); !slices.Equal(got, want) {
		t.Errorf("manual capture: %v", got)
	}

	declined := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclined").err()["payment_intent"].(map[string]any)["id"].(string)
	evs := s.events("", declined)
	if got := types(evs); !slices.Equal(got, []string{"payment_intent.created", "charge.failed", "payment_intent.payment_failed"}) {
		t.Errorf("decline: %v", got)
	}
	if ch := evs[1]["data"].(map[string]any)["object"].(map[string]any); ch["status"] != "failed" || ch["failure_code"] != "card_declined" {
		t.Errorf("charge.failed object: %v", ch)
	}

	paid, _ := s.paid("")
	s.form("POST", "/v1/refunds", "payment_intent="+paid)
	got := types(s.events("", paid))
	if !slices.Equal(got[len(got)-2:], []string{"refund.created", "charge.refunded"}) {
		t.Errorf("refund: %v", got)
	}
}

func TestEventFiltersAndPages(t *testing.T) {
	s := newStripe(t)
	s.paid("")
	s.paid("")
	r := s.form("GET", "/v1/events?type=payment_intent.*", "")
	for _, e := range r.body["data"].([]any) {
		if typ := e.(map[string]any)["type"].(string); typ != "payment_intent.created" && typ != "payment_intent.succeeded" {
			t.Errorf("type filter let through %s", typ)
		}
	}
	if n := len(r.body["data"].([]any)); n != 4 {
		t.Errorf("payment_intent.* gave %d events", n)
	}
	r = s.form("GET", "/v1/events?types[]=charge.succeeded&types[]=payment_intent.created", "")
	if n := len(r.body["data"].([]any)); n != 4 {
		t.Errorf("types[] gave %d events", n)
	}
	page := s.form("GET", "/v1/events?limit=4", "")
	data := page.body["data"].([]any)
	last := data[3].(map[string]any)["id"].(string)
	next := s.form("GET", "/v1/events?limit=4&starting_after="+last, "")
	if page.body["has_more"] != true || next.body["has_more"] != false || len(next.body["data"].([]any)) != 2 {
		t.Errorf("pages: %v / %v", page.body["has_more"], next.body)
	}
}

func TestDisputeEvents(t *testing.T) {
	s := newStripe(t, func(c *config.Config) { c.ManualClock = true })
	r := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_createDispute")
	id := r.body["id"].(string)
	if r.body["status"] != "succeeded" {
		t.Fatalf("create: %v", r.body)
	}
	advance := func(seconds int) {
		if a := s.Do("POST", "/_sandbox/clock/advance", map[string]any{"seconds": seconds}); a.Status != 200 {
			t.Fatalf("advance: %d", a.Status)
		}
	}
	advance(1)
	evs := s.events("&type=charge.dispute.*", id)
	if len(evs) != 1 || evs[0]["type"] != "charge.dispute.created" {
		t.Fatalf("after 1s: %v", types(evs))
	}
	dp := evs[0]["data"].(map[string]any)["object"].(map[string]any)
	if dp["object"] != "dispute" || dp["status"] != "needs_response" || dp["amount"] != 1000.0 || dp["payment_intent"] != id {
		t.Errorf("dispute: %v", dp)
	}
	if pi := s.form("GET", "/v1/payment_intents/"+id+"?expand[]=latest_charge", ""); pi.body["status"] != "succeeded" ||
		pi.body["latest_charge"].(map[string]any)["disputed"] != true {
		t.Errorf("intent during dispute: %v", pi.body)
	}
	if r := s.form("POST", "/v1/refunds", "payment_intent="+id); r.err()["code"] != "charge_disputed" {
		t.Errorf("refund of a disputed charge: %v", r.body)
	}
	advance(31 * 24 * 3600)
	evs = s.events("&type=charge.dispute.*", id)
	if len(evs) != 2 || evs[1]["type"] != "charge.dispute.closed" ||
		evs[1]["data"].(map[string]any)["object"].(map[string]any)["status"] != "lost" {
		t.Errorf("after close: %v", evs)
	}
	if evs[0]["data"].(map[string]any)["object"].(map[string]any)["created"] != evs[1]["data"].(map[string]any)["object"].(map[string]any)["created"] {
		t.Error("the dispute keeps its created time")
	}
}
