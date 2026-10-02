package stripe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
)

type stripeBox struct {
	t *testing.T
	*sandboxtest.Sandbox
}

func newStripe(t *testing.T, change ...func(*config.Config)) *stripeBox {
	t.Helper()
	change = append([]func(*config.Config){func(c *config.Config) {
		c.Profile = config.ProfileStripe
		c.ProcessingDelay = 0
	}}, change...)
	return &stripeBox{t: t, Sandbox: sandboxtest.New(t, change...)}
}

type reply struct {
	status int
	header http.Header
	body   map[string]any
}

func (r reply) err() map[string]any {
	e, _ := r.body["error"].(map[string]any)
	return e
}

// form sends a request the way the Stripe SDKs do: form-encoded, bearer key.
// headers are name, value pairs.
func (s *stripeBox) form(method, path, body string, headers ...string) reply {
	s.t.Helper()
	r, err := s.formCtx(context.Background(), method, path, body, headers...)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	return r
}

func (s *stripeBox) formCtx(ctx context.Context, method, path, body string, headers ...string) (reply, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.Server.URL+path, strings.NewReader(body))
	if err != nil {
		return reply{}, err
	}
	req.Header.Set("Authorization", "Bearer sk_test_123")
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := s.Server.Client().Do(req)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := reply{status: resp.StatusCode, header: resp.Header}
	if err := json.Unmarshal(raw, &out.body); err != nil {
		s.t.Fatalf("%s %s: %q is not JSON", method, path, raw)
	}
	return out, nil
}

func (s *stripeBox) create(body string, headers ...string) reply {
	s.t.Helper()
	return s.form("POST", "/v1/payment_intents", body, headers...)
}

const visa = "amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa"

func TestCreateAndConfirmAnswersWithTheOutcome(t *testing.T) {
	s := newStripe(t)
	r := s.create(visa + "&metadata[order_id]=42&metadata[reference]=order-42&description=Order+42")
	b := r.body
	if r.status != 200 || b["object"] != "payment_intent" || b["status"] != "succeeded" ||
		b["amount_received"] != 1000.0 || b["currency"] != "eur" || b["payment_method"] != "pm_card_visa" ||
		b["description"] != "Order 42" || b["last_payment_error"] != nil {
		t.Fatalf("%d %v", r.status, b)
	}
	id := b["id"].(string)
	if !strings.HasPrefix(id, "pi_") || b["latest_charge"] != "ch_"+strings.TrimPrefix(id, "pi_") {
		t.Errorf("id %s, latest_charge %v", id, b["latest_charge"])
	}
	if md := b["metadata"].(map[string]any); md["order_id"] != "42" {
		t.Errorf("metadata %v", md)
	}
	if p, _ := s.App.Engine.Payment(id); p.Reference != "order-42" {
		t.Errorf("reference for rules and reset: %q", p.Reference)
	}

	ch := s.form("GET", "/v1/charges/"+b["latest_charge"].(string), "")
	if ch.status != 200 || ch.body["status"] != "succeeded" || ch.body["captured"] != true ||
		ch.body["amount_captured"] != 1000.0 || ch.body["payment_intent"] != id || ch.body["paid"] != true {
		t.Errorf("charge %d %v", ch.status, ch.body)
	}
	got := s.form("GET", "/v1/payment_intents/"+id+"?expand[]=latest_charge", "")
	if c, ok := got.body["latest_charge"].(map[string]any); !ok || c["id"] != ch.body["id"] {
		t.Errorf("expanded latest_charge %v", got.body["latest_charge"])
	}
}

func TestDeclinedCardAnswers402AndCanBeRetried(t *testing.T) {
	s := newStripe(t)
	r := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclinedInsufficientFunds")
	e := r.err()
	if r.status != 402 || e["type"] != "card_error" || e["code"] != "card_declined" ||
		e["decline_code"] != "insufficient_funds" {
		t.Fatalf("%d %v", r.status, r.body)
	}
	pi := e["payment_intent"].(map[string]any)
	id := pi["id"].(string)
	if pi["status"] != "requires_payment_method" || e["charge"] != pi["latest_charge"] {
		t.Errorf("payment_intent %v, charge %v", pi, e["charge"])
	}
	if lpe := pi["last_payment_error"].(map[string]any); lpe["decline_code"] != "insufficient_funds" {
		t.Errorf("last_payment_error %v", lpe)
	}

	again := s.form("POST", "/v1/payment_intents/"+id+"/confirm", "payment_method=pm_card_visa")
	first := "ch_" + strings.TrimPrefix(id, "pi_")
	if again.status != 200 || again.body["status"] != "succeeded" || again.body["latest_charge"] != first+"_2" ||
		again.body["last_payment_error"] != nil {
		t.Fatalf("retry: %d %v", again.status, again.body)
	}
	old := s.form("GET", "/v1/charges/"+first, "")
	if old.body["status"] != "failed" || old.body["failure_code"] != "card_declined" || old.body["paid"] != false {
		t.Errorf("first charge: %v", old.body)
	}
	if r := s.form("GET", "/v1/charges/"+first+"_3", ""); r.status != 404 {
		t.Errorf("charge of an attempt that did not happen: %d", r.status)
	}
}

func TestTestCards(t *testing.T) {
	s := newStripe(t)
	cases := map[string][2]string{
		"pm_card_visa_chargeDeclined":           {"card_declined", "generic_decline"},
		"pm_card_visa_chargeDeclinedLostCard":   {"card_declined", "lost_card"},
		"pm_card_visa_chargeDeclinedStolenCard": {"card_declined", "stolen_card"},
		"pm_card_chargeDeclinedExpiredCard":     {"expired_card", ""},
		"pm_card_chargeDeclinedIncorrectCvc":    {"incorrect_cvc", ""},
		"pm_card_chargeDeclinedProcessingError": {"processing_error", ""},
	}
	for pm, want := range cases {
		r := s.create("amount=1000&currency=eur&confirm=true&payment_method=" + pm)
		e := r.err()
		got := [2]string{}
		got[0], _ = e["code"].(string)
		got[1], _ = e["decline_code"].(string)
		if r.status != 402 || got != want {
			t.Errorf("%s: %d %v", pm, r.status, e)
		}
	}
	for _, pm := range []string{"pm_card_mastercard", "pm_card_visa_debit"} {
		if r := s.create("amount=1000&currency=eur&confirm=true&payment_method=" + pm); r.body["status"] != "succeeded" {
			t.Errorf("%s: %v", pm, r.body)
		}
	}
	r := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_1Nxyz")
	if r.status != 404 || r.err()["code"] != "resource_missing" || r.err()["param"] != "payment_method" {
		t.Errorf("unknown payment method: %d %v", r.status, r.body)
	}
}

func TestScenarioFromMetadataAndHeader(t *testing.T) {
	s := newStripe(t)
	r := s.create(visa + "&metadata[sandbox_scenario]=" + url.QueryEscape("declined; reason=expired_card"))
	if r.status != 402 || r.err()["code"] != "expired_card" {
		t.Errorf("metadata scenario: %d %v", r.status, r.body)
	}
	r = s.create(visa, "X-Sandbox-Scenario", "declined; reason=lost_card")
	if r.status != 402 || r.err()["decline_code"] != "lost_card" {
		t.Errorf("header scenario: %d %v", r.status, r.body)
	}
	// A scenario wins over the test card.
	r = s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclined&metadata[sandbox_scenario]=happy_path")
	if r.status != 200 {
		t.Errorf("scenario over card: %d %v", r.status, r.body)
	}
	r = s.create(visa + "&metadata[sandbox_scenario]=no_such_thing")
	if r.status != 400 || r.err()["param"] != "metadata[sandbox_scenario]" {
		t.Errorf("unknown scenario: %d %v", r.status, r.body)
	}
	r = s.create(visa + "&metadata[sandbox_scenario]=" + url.QueryEscape("declined; reason=bored"))
	if r.status != 400 {
		t.Errorf("invalid parameter: %d %v", r.status, r.body)
	}
	if n := len(s.form("GET", "/v1/payment_intents?limit=100", "").body["data"].([]any)); n != 3 {
		t.Errorf("%d payment intents, refused creates must store nothing", n)
	}
}

func TestConfirmInTwoSteps(t *testing.T) {
	s := newStripe(t)
	r := s.create("amount=1000&currency=eur")
	id := r.body["id"].(string)
	if r.body["status"] != "requires_payment_method" || r.body["latest_charge"] != nil {
		t.Fatalf("created: %v", r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id+"/confirm", ""); r.status != 400 || r.err()["param"] != "payment_method" {
		t.Errorf("confirm without a payment method: %d %v", r.status, r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id, "payment_method=pm_card_visa&amount=1500"); r.body["status"] != "requires_confirmation" || r.body["amount"] != 1500.0 {
		t.Errorf("update: %v", r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id+"/confirm", ""); r.body["status"] != "succeeded" || r.body["amount_received"] != 1500.0 {
		t.Errorf("confirm: %v", r.body)
	}
	r = s.form("POST", "/v1/payment_intents/"+id+"/confirm", "")
	if r.status != 400 || r.err()["code"] != "payment_intent_unexpected_state" ||
		!strings.Contains(r.err()["message"].(string), "status of succeeded") {
		t.Errorf("second confirm: %d %v", r.status, r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id, "amount=2000"); r.status != 400 {
		t.Errorf("amount after success: %d %v", r.status, r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/pi_nope/confirm", ""); r.status != 404 || r.err()["code"] != "resource_missing" {
		t.Errorf("unknown intent: %d %v", r.status, r.body)
	}
}

func TestUpdateMetadata(t *testing.T) {
	s := newStripe(t)
	id := s.create("amount=1000&currency=eur&metadata[a]=1&metadata[b]=2").body["id"].(string)
	r := s.form("POST", "/v1/payment_intents/"+id, "metadata[a]=&metadata[c]=3&metadata[reference]=r-1")
	md := r.body["metadata"].(map[string]any)
	if len(md) != 3 || md["b"] != "2" || md["c"] != "3" || md["a"] != nil {
		t.Errorf("merged metadata %v", md)
	}
	if p, _ := s.App.Engine.Payment(id); p.Reference != "r-1" {
		t.Errorf("reference %q", p.Reference)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id, "metadata="); len(r.body["metadata"].(map[string]any)) != 0 {
		t.Errorf("metadata= should clear: %v", r.body["metadata"])
	}
}

func TestManualCapture(t *testing.T) {
	s := newStripe(t)
	r := s.create(visa + "&capture_method=manual")
	id := r.body["id"].(string)
	if r.body["status"] != "requires_capture" || r.body["amount_capturable"] != 1000.0 || r.body["capture_method"] != "manual" {
		t.Fatalf("authorized: %v", r.body)
	}
	if ch := s.form("GET", "/v1/charges/"+r.body["latest_charge"].(string), ""); ch.body["captured"] != false || ch.body["status"] != "succeeded" {
		t.Errorf("authorized charge: %v", ch.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id+"/capture", "amount_to_capture=5000"); r.status != 400 || r.err()["code"] != "amount_too_large" {
		t.Errorf("capture too much: %d %v", r.status, r.body)
	}
	r = s.form("POST", "/v1/payment_intents/"+id+"/capture", "amount_to_capture=600")
	if r.status != 200 || r.body["status"] != "succeeded" || r.body["amount_received"] != 600.0 || r.body["amount_capturable"] != 0.0 {
		t.Errorf("capture: %d %v", r.status, r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+id+"/capture", ""); r.status != 400 || r.err()["code"] != "payment_intent_unexpected_state" {
		t.Errorf("second capture: %d %v", r.status, r.body)
	}
}

func TestCancel(t *testing.T) {
	s := newStripe(t)
	open := s.create("amount=1000&currency=eur").body["id"].(string)
	r := s.form("POST", "/v1/payment_intents/"+open+"/cancel", "cancellation_reason=abandoned")
	if r.body["status"] != "canceled" || r.body["cancellation_reason"] != "abandoned" || r.body["canceled_at"] == nil {
		t.Errorf("unconfirmed: %v", r.body)
	}
	failed := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclined").err()["payment_intent"].(map[string]any)["id"].(string)
	if r := s.form("POST", "/v1/payment_intents/"+failed+"/cancel", ""); r.body["status"] != "canceled" {
		t.Errorf("failed: %v", r.body)
	}
	held := s.create(visa + "&capture_method=manual").body["id"].(string)
	r = s.form("POST", "/v1/payment_intents/"+held+"/cancel", "")
	if r.body["status"] != "canceled" {
		t.Errorf("authorized: %v", r.body)
	}
	if ch := s.form("GET", "/v1/charges/"+r.body["latest_charge"].(string), ""); ch.body["refunded"] != true || ch.body["amount_refunded"] != 1000.0 {
		t.Errorf("released authorization: %v", ch.body)
	}
	done := s.create(visa).body["id"].(string)
	if r := s.form("POST", "/v1/payment_intents/"+done+"/cancel", ""); r.status != 400 || r.err()["code"] != "payment_intent_unexpected_state" {
		t.Errorf("succeeded: %d %v", r.status, r.body)
	}
	if r := s.form("POST", "/v1/payment_intents/"+open+"/cancel", "cancellation_reason=bored"); r.status != 400 {
		t.Errorf("bad reason: %d", r.status)
	}
}

func TestCreateValidation(t *testing.T) {
	s := newStripe(t)
	cases := map[string]string{
		"currency=eur":                                  "amount",
		"amount=1000":                                   "currency",
		"amount=0&currency=eur":                         "amount",
		"amount=ten&currency=eur":                       "amount",
		"amount=1000&currency=euro1":                    "currency",
		"amount=1000&currency=eur&confirm=yes":          "confirm",
		"amount=1000&currency=eur&capture_method=later": "capture_method",
		"amount=1000&currency=eur&metadata[sandbox_callback_url]=not-a-url":                                "metadata[sandbox_callback_url]",
		"amount=1000&currency=eur&confirm=true":                                                            "payment_method",
		"amount=1000&currency=eur&metadata[sandbox_scenario]=no_such_thing":                                "metadata[sandbox_scenario]",
		"amount=1000&currency=eur&metadata[sandbox_scenario]=" + url.QueryEscape("declined; reason=bored"): "metadata[sandbox_scenario]",
	}
	for body, param := range cases {
		r := s.create(body)
		if r.status != 400 || r.err()["param"] != param || r.err()["type"] != "invalid_request_error" {
			t.Errorf("%s: %d %v", body, r.status, r.body)
		}
	}
}

func TestIdempotentCreate(t *testing.T) {
	s := newStripe(t)
	first := s.create(visa, "Idempotency-Key", "order-1")
	again := s.create(visa, "Idempotency-Key", "order-1")
	if again.body["id"] != first.body["id"] || again.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay: %v / %v", first.body["id"], again.body["id"])
	}
	declined := "amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa_chargeDeclined"
	d1 := s.create(declined, "Idempotency-Key", "order-2")
	d2 := s.create(declined, "Idempotency-Key", "order-2")
	if d2.status != 402 || d2.err()["charge"] != d1.err()["charge"] {
		t.Errorf("declined replay: %d %v", d2.status, d2.body)
	}
	if r := s.create(visa+"&amount=2000", "Idempotency-Key", "order-1"); r.status != 400 || r.err()["type"] != "idempotency_error" {
		t.Errorf("other params: %d %v", r.status, r.body)
	}
}

func TestServerErrorThenSuccessWithSDKRetry(t *testing.T) {
	s := newStripe(t)
	scen := "X-Sandbox-Scenario"
	first := s.create(visa, scen, "server_error_then_success", "Idempotency-Key", "k-1")
	if first.status != 503 || first.err()["type"] != "api_error" || first.header.Get("Stripe-Should-Retry") != "true" {
		t.Fatalf("first: %d %v %v", first.status, first.body, first.header)
	}
	retry := s.create(visa, scen, "server_error_then_success", "Idempotency-Key", "k-1")
	if retry.status != 200 || retry.body["status"] != "succeeded" {
		t.Fatalf("retry: %d %v", retry.status, retry.body)
	}
	if n := len(s.form("GET", "/v1/payment_intents", "").body["data"].([]any)); n != 1 {
		t.Errorf("%d payment intents after a refused call and its retry, want 1", n)
	}

	// Confirm of an existing intent: the refusal leaves it as it was.
	id := s.create("amount=1000&currency=eur&payment_method=pm_card_visa").body["id"].(string)
	path := "/v1/payment_intents/" + id + "/confirm"
	if r := s.form("POST", path, "", scen, "server_error_then_success"); r.status != 503 {
		t.Fatalf("confirm: %d", r.status)
	}
	if r := s.form("GET", "/v1/payment_intents/"+id, ""); r.body["status"] != "requires_confirmation" {
		t.Errorf("after refusal: %v", r.body["status"])
	}
	if r := s.form("POST", path, "", scen, "server_error_then_success"); r.body["status"] != "succeeded" {
		t.Errorf("confirm retry without a key: %v", r.body)
	}
}

func TestTimeoutThenSuccessRetryGetsTheStoredAnswer(t *testing.T) {
	s := newStripe(t)
	headers := []string{"X-Sandbox-Scenario", "timeout_then_success; delay=2s", "Idempotency-Key", "slow-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.formCtx(ctx, "POST", "/v1/payment_intents", visa, headers...); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first call should time out on the client: %v", err)
	}
	r := s.create(visa, headers...)
	if r.status != 200 || r.body["status"] != "succeeded" || r.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("retry: %d %v", r.status, r.body)
	}
}

func TestList(t *testing.T) {
	s := newStripe(t)
	var ids []string
	for range 3 {
		ids = append(ids, s.create("amount=1000&currency=eur").body["id"].(string))
	}
	r := s.form("GET", "/v1/payment_intents?limit=2", "")
	data := r.body["data"].([]any)
	if r.body["object"] != "list" || r.body["has_more"] != true || len(data) != 2 ||
		data[0].(map[string]any)["id"] != ids[2] || r.body["url"] != "/v1/payment_intents" {
		t.Fatalf("page 1: %v", r.body)
	}
	r = s.form("GET", "/v1/payment_intents?limit=2&starting_after="+ids[1], "")
	data = r.body["data"].([]any)
	if r.body["has_more"] != false || len(data) != 1 || data[0].(map[string]any)["id"] != ids[0] {
		t.Errorf("page 2: %v", r.body)
	}
	if r := s.form("GET", "/v1/payment_intents?limit=101", ""); r.status != 400 {
		t.Errorf("limit 101: %d", r.status)
	}
}

func TestManualClockAnswersWhileProcessing(t *testing.T) {
	s := newStripe(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = time.Second
	})
	r := s.create(visa)
	if r.status != 200 || r.body["status"] != "processing" {
		t.Fatalf("with a manual clock: %d %v", r.status, r.body)
	}
	if adv := s.Do("POST", "/_sandbox/clock/advance", map[string]any{"seconds": 1}); adv.Status != 200 {
		t.Fatalf("advance: %d", adv.Status)
	}
	if got := s.form("GET", "/v1/payment_intents/"+r.body["id"].(string), ""); got.body["status"] != "succeeded" {
		t.Errorf("after advance: %v", got.body["status"])
	}
}
