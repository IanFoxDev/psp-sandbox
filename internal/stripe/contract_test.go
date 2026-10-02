package stripe_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
)

// contract sends requests and checks every answer against the Stripe spec:
// a 200 against the operation's response schema, anything else against the
// error schema.
type contract struct {
	t    *testing.T
	box  *stripeBox
	spec *stripeSpec
	// checked counts documents per schema, for the summary.
	checked map[string]int
}

// call sends one request. tmpl is the spec's path template, path the real one.
func (c *contract) call(method, tmpl, path, body string, headers ...string) map[string]any {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.box.Server.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sk_test_123")
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.box.Server.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	s, name := c.spec.component("error"), "error"
	if resp.StatusCode == 200 {
		s, name = c.spec.responseSchema(c.t, method, tmpl), method+" "+tmpl
	}
	c.expect(fmt.Sprintf("%s %s -> %d", method, path, resp.StatusCode), raw, s, name)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (c *contract) expect(what string, doc []byte, s *schema, name string) {
	c.t.Helper()
	c.checked[name]++
	if problems := c.spec.check(doc, s); len(problems) > 0 {
		c.t.Errorf("%s does not match the Stripe schema:\n  %s\n  body: %s", what, strings.Join(problems, "\n  "), doc)
	}
}

// TestContract runs every operation of the profile through the states and
// scenarios that change the shape of the answers, then checks every event,
// and its data.object against the schema of that object.
func TestContract(t *testing.T) {
	sp := loadStripeSpec(t)
	box := newStripe(t, func(c *config.Config) { c.ManualClock = true })
	c := &contract{t: t, box: box, spec: sp, checked: map[string]int{}}
	const (
		intents = "/v1/payment_intents"
		intent  = "/v1/payment_intents/{intent}"
	)
	id := func(m map[string]any) string { return m["id"].(string) }

	// Paid at once, with everything the create call takes.
	paid := c.call("POST", intents, intents, visa+"&description=Order+1&metadata[order_id]=1&expand[]=latest_charge")
	c.call("GET", intent, intents+"/"+id(paid), "")
	c.call("GET", intent, intents+"/"+id(paid)+"?expand[]=latest_charge", "")
	c.call("GET", "/v1/charges/{charge}", "/v1/charges/"+paid["latest_charge"].(map[string]any)["id"].(string), "")

	// Two steps, an update, a decline, a retry.
	open := c.call("POST", intents, intents, "amount=2000&currency=usd")
	c.call("POST", intent, intents+"/"+id(open), "metadata[note]=x&description=Two+steps")
	c.call("POST", intent+"/confirm", intents+"/"+id(open)+"/confirm", "payment_method=pm_card_visa_chargeDeclinedInsufficientFunds")
	c.call("POST", intent+"/confirm", intents+"/"+id(open)+"/confirm", "payment_method=pm_card_chargeDeclinedExpiredCard")
	retried := c.call("POST", intent+"/confirm", intents+"/"+id(open)+"/confirm", "payment_method=pm_card_visa&expand[]=latest_charge")
	c.call("GET", "/v1/charges/{charge}", "/v1/charges/"+retried["latest_charge"].(map[string]any)["id"].(string), "")
	c.call("GET", "/v1/charges/{charge}", "/v1/charges/ch_"+strings.TrimPrefix(id(open), "pi_"), "")

	// Manual capture, partial capture, cancellations.
	held := c.call("POST", intents, intents, visa+"&capture_method=manual")
	c.call("POST", intent+"/capture", intents+"/"+id(held)+"/capture", "amount_to_capture=400")
	c.call("GET", "/v1/charges/{charge}", "/v1/charges/"+held["latest_charge"].(string), "")
	released := c.call("POST", intents, intents, visa+"&capture_method=manual")
	c.call("POST", intent+"/cancel", intents+"/"+id(released)+"/cancel", "cancellation_reason=requested_by_customer")
	c.call("GET", "/v1/charges/{charge}", "/v1/charges/"+released["latest_charge"].(string), "")
	abandoned := c.call("POST", intents, intents, "amount=500&currency=eur")
	c.call("POST", intent+"/cancel", intents+"/"+id(abandoned)+"/cancel", "cancellation_reason=abandoned")

	// Refunds, partial and full.
	part := c.call("POST", "/v1/refunds", "/v1/refunds", "payment_intent="+id(paid)+"&amount=300&reason=duplicate&metadata[ticket]=1")
	c.call("POST", "/v1/refunds", "/v1/refunds", "payment_intent="+id(paid))
	c.call("GET", "/v1/refunds/{refund}", "/v1/refunds/"+id(part), "")
	c.call("GET", "/v1/charges/{charge}", "/v1/charges/"+paid["latest_charge"].(map[string]any)["id"].(string), "")

	// A chargeback, opened and lost.
	disputed := c.call("POST", intents, intents, "amount=1000&currency=eur&confirm=true&payment_method=pm_card_createDispute")
	box.Do("POST", "/_sandbox/clock/advance", map[string]any{"seconds": 1})
	c.call("GET", intent, intents+"/"+id(disputed)+"?expand[]=latest_charge", "")
	box.Do("POST", "/_sandbox/clock/advance", map[string]any{"seconds": 31 * 24 * 3600})

	// Lists.
	c.call("GET", intents, intents+"?limit=3", "")
	c.call("GET", "/v1/events", "/v1/events?limit=2&type=charge.*", "")

	// Errors: 400, 401, 402, 404, idempotency.
	c.call("POST", intents, intents, "currency=eur")
	c.call("POST", intents, intents, visa, "Authorization", "Bearer sk_live_123")
	c.call("POST", intents, intents, "amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclined")
	c.call("GET", intent, intents+"/pi_missing", "")
	c.call("POST", "/v1/refunds", "/v1/refunds", "payment_intent="+id(paid))
	c.call("POST", intents, intents, visa, "Idempotency-Key", "contract-1")
	c.call("POST", intents, intents, visa+"&amount=5", "Idempotency-Key", "contract-1")

	// Every event, and the object inside it.
	var events []map[string]any
	after := ""
	for {
		q := "/v1/events?limit=100"
		if after != "" {
			q += "&starting_after=" + after
		}
		page := c.call("GET", "/v1/events", q, "")
		for _, e := range page["data"].([]any) {
			events = append(events, e.(map[string]any))
		}
		if page["has_more"] != true {
			break
		}
		after = id(events[len(events)-1])
	}
	seen := map[string]bool{}
	for _, e := range events {
		raw, _ := json.Marshal(e)
		c.expect("event "+id(e), raw, sp.component("event"), "event")
		obj := e["data"].(map[string]any)["object"].(map[string]any)
		rawObj, _ := json.Marshal(obj)
		kind := obj["object"].(string)
		c.expect(fmt.Sprintf("%s %s data.object", e["type"], id(e)), rawObj, sp.component(kind), kind)
		seen[e["type"].(string)] = true
		c.call("GET", "/v1/events/{id}", "/v1/events/"+id(e), "")
	}
	for _, typ := range []string{
		"payment_intent.created", "payment_intent.succeeded", "payment_intent.payment_failed",
		"payment_intent.canceled", "payment_intent.amount_capturable_updated",
		"charge.succeeded", "charge.failed", "charge.captured", "charge.refunded",
		"refund.created", "charge.dispute.created", "charge.dispute.closed",
	} {
		if !seen[typ] {
			t.Errorf("the run produced no %s event, so its shape is unchecked", typ)
		}
	}
	t.Logf("checked against stripe/openapi %s: %v", stripeSpecCommit[:7], c.checked)
}
