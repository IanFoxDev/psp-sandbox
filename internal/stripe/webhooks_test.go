package stripe_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
)

// hooks returns the webhooks of one payment intent, as types, in the order
// they arrived. It fails on any webhook whose Stripe-Signature does not
// verify.
func hooks(t *testing.T, all []sandboxtest.Callback, intent string) []string {
	t.Helper()
	var out []string
	for _, c := range all {
		if !c.Signed {
			t.Errorf("%s %s: signature does not verify: %v", c.ID, c.Type, c.Header)
		}
		obj, _ := c.Data["object"].(map[string]any)
		if obj["id"] == intent || obj["payment_intent"] == intent {
			out = append(out, c.Type)
		}
	}
	return out
}

func waitHooks(t *testing.T, s *stripeBox, intent string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(sandboxtest.Wait)
	for {
		got := hooks(t, s.Receiver.All(), intent)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d webhooks of %s, got %v", n, intent, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func intentOf(r reply) string {
	if id, ok := r.body["id"].(string); ok {
		return id
	}
	return r.err()["payment_intent"].(map[string]any)["id"].(string)
}

func TestWebhooksMatchTheEventsAPI(t *testing.T) {
	s := newStripe(t)
	id := intentOf(s.create(visa))
	got := waitHooks(t, s, id, 3)
	want := []string{"payment_intent.created", "charge.succeeded", "payment_intent.succeeded"}
	if !slices.Equal(got, want) {
		t.Fatalf("webhooks %v, want %v", got, want)
	}
	for _, c := range s.Receiver.All() {
		if c.Header.Get("Stripe-Signature") == "" || c.Header.Get("webhook-signature") != "" {
			t.Errorf("%s: want only Stripe-Signature, got %v", c.ID, c.Header)
		}
		ev := s.form("GET", "/v1/events/"+c.ID, "")
		var sent map[string]any
		if err := json.Unmarshal(c.Body, &sent); err != nil {
			t.Fatal(err)
		}
		if ev.status != 200 || !jsonEqual(t, sent, ev.body) {
			t.Errorf("%s: webhook body differs from GET /v1/events:\n%s\n%v", c.ID, c.Body, ev.body)
		}
	}
}

func jsonEqual(t *testing.T, a, b map[string]any) bool {
	t.Helper()
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestWebhookSignatureUsesWallClock(t *testing.T) {
	s := newStripe(t, func(c *config.Config) { c.ManualClock = true })
	s.Do("POST", "/_sandbox/clock/advance", map[string]any{"seconds": 48 * 3600})
	waitHooks(t, s, intentOf(s.create(visa)), 3)
	for _, c := range s.Receiver.All() {
		if d := time.Since(time.Unix(c.Timestamp, 0)); d < -time.Minute || d > time.Minute {
			t.Errorf("%s: t=%d is %v away from now; SDKs reject more than 300s", c.ID, c.Timestamp, d)
		}
	}
}

func TestScenarioWebhooks(t *testing.T) {
	cases := []struct {
		scenario string
		body     string
		want     []string
	}{
		{"happy_path", visa, []string{"payment_intent.created", "charge.succeeded", "payment_intent.succeeded"}},
		{"declined", visa, []string{"payment_intent.created", "charge.failed", "payment_intent.payment_failed"}},
		{"duplicate_callback; times=3", visa, []string{"payment_intent.created",
			"charge.succeeded", "charge.succeeded", "charge.succeeded",
			"payment_intent.succeeded", "payment_intent.succeeded", "payment_intent.succeeded"}},
		{"out_of_order; window=200ms", visa, []string{"payment_intent.created", "payment_intent.succeeded", "charge.succeeded"}},
		{"delayed_callback; delay=200ms", visa, []string{"payment_intent.created", "charge.succeeded", "payment_intent.succeeded"}},
	}
	for _, c := range cases {
		t.Run(strings.Fields(c.scenario)[0], func(t *testing.T) {
			s := newStripe(t)
			started := time.Now()
			id := intentOf(s.create(c.body + "&metadata[sandbox_scenario]=" + url.QueryEscape(c.scenario)))
			got := waitHooks(t, s, id, len(c.want))
			if !slices.Equal(got, c.want) {
				t.Errorf("webhooks %v, want %v", got, c.want)
			}
			if c.scenario == "delayed_callback; delay=200ms" {
				last := s.Receiver.All()[len(c.want)-1]
				if d := last.Received.Sub(started); d < 200*time.Millisecond {
					t.Errorf("delayed webhook after %v", d)
				}
			}
		})
	}
}

func TestLostCallbackSendsNothingAfterCreate(t *testing.T) {
	s := newStripe(t)
	id := intentOf(s.create(visa + "&metadata[sandbox_scenario]=lost_callback"))
	s.Receiver.Quiet(1, 300*time.Millisecond)
	if got := hooks(t, s.Receiver.All(), id); !slices.Equal(got, []string{"payment_intent.created"}) {
		t.Errorf("webhooks %v", got)
	}
	if r := s.form("GET", "/v1/payment_intents/"+id, ""); r.body["status"] != "succeeded" {
		t.Errorf("polling still tells the result: %v", r.body["status"])
	}
}

func TestCallbackBeforeResponse(t *testing.T) {
	s := newStripe(t)
	r := s.create(visa + "&metadata[sandbox_scenario]=callback_before_response")
	answered := time.Now()
	id := intentOf(r)
	for _, c := range s.Receiver.All() {
		if c.Type == "charge.succeeded" {
			if !c.Received.Before(answered) {
				t.Errorf("charge.succeeded arrived after the confirm answer")
			}
			return
		}
	}
	t.Errorf("no charge.succeeded before the answer: %v", hooks(t, s.Receiver.All(), id))
}

func TestTimeoutThenSuccessStillSendsWebhooks(t *testing.T) {
	s := newStripe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := s.formCtx(ctx, "POST", "/v1/payment_intents", visa, "X-Sandbox-Scenario", "timeout_then_success; delay=2s")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a client timeout, got %v", err)
	}
	got := s.Receiver.Wait(3)
	if got[2].Type != "payment_intent.succeeded" {
		t.Errorf("webhooks after the timeout: %v", got)
	}
}

func TestServerErrorThenSuccessWebhooks(t *testing.T) {
	s := newStripe(t)
	h := []string{"X-Sandbox-Scenario", "server_error_then_success", "Idempotency-Key", "k-1"}
	if r := s.create(visa, h...); r.status != 503 {
		t.Fatalf("first: %d", r.status)
	}
	id := intentOf(s.create(visa, h...))
	s.Receiver.Quiet(3, 200*time.Millisecond)
	if got := hooks(t, s.Receiver.All(), id); len(got) != 3 {
		t.Errorf("a refused call sends nothing: %v", got)
	}
}

func TestChargebackWebhooks(t *testing.T) {
	s := newStripe(t, func(c *config.Config) { c.ManualClock = true })
	id := intentOf(s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_createDispute"))
	waitHooks(t, s, id, 3)
	s.Do("POST", "/_sandbox/clock/advance", map[string]any{"seconds": 1})
	got := waitHooks(t, s, id, 4)
	if got[3] != "charge.dispute.created" {
		t.Errorf("webhooks %v", got)
	}
}

func TestCallbackURLFromMetadata(t *testing.T) {
	s := newStripe(t)
	other := sandboxtest.NewReceiver(t)
	id := intentOf(s.create(visa + "&metadata[sandbox_callback_url]=" + url.QueryEscape(other.URL())))
	other.Wait(3)
	s.Receiver.Quiet(0, 100*time.Millisecond)
	if got := hooks(t, other.All(), id); len(got) != 3 {
		t.Errorf("other receiver: %v", got)
	}
}

func TestReplayResendsTheStripeEvent(t *testing.T) {
	s := newStripe(t)
	id := intentOf(s.create(visa))
	waitHooks(t, s, id, 3)
	r := s.Do("GET", "/_sandbox/payments/"+id+"/deliveries", nil)
	var deliveries struct {
		Data []struct {
			ID        string `json:"id"`
			EventType string `json:"event_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(r.Body, &deliveries); err != nil || len(deliveries.Data) != 3 {
		t.Fatalf("deliveries: %s %v", r.Body, err)
	}
	last := deliveries.Data[2]
	if last.EventType != "payment_intent.succeeded" {
		t.Errorf("delivery type %s", last.EventType)
	}
	if r := s.Do("POST", "/_sandbox/deliveries/"+last.ID+"/replay", nil); r.Status/100 != 2 {
		t.Fatalf("replay: %d %s", r.Status, r.Body)
	}
	got := waitHooks(t, s, id, 4)
	if got[3] != "payment_intent.succeeded" {
		t.Errorf("replayed: %v", got)
	}
}
