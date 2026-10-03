package stripe_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

const threeDS = "amount=1000&currency=eur&confirm=true&payment_method=pm_card_threeDSecure2Required"

func (s *stripeBox) authenticate(id, result string) {
	s.t.Helper()
	if r := s.Do(http.MethodPost, "/_sandbox/payments/"+id+"/authenticate", map[string]any{"result": result}); r.Status != http.StatusOK {
		s.t.Fatalf("authenticate %s: %d %s", id, r.Status, r.Body)
	}
}

func TestThreeDSecureConfirmAnswersRequiresAction(t *testing.T) {
	s := newStripe(t)
	r := s.create(threeDS + "&return_url=" + url.QueryEscape("https://shop.test/return"))
	b := r.body
	if r.status != 200 || b["status"] != "requires_action" || b["latest_charge"] != nil {
		t.Fatalf("%d %v", r.status, b)
	}
	id := b["id"].(string)
	na, _ := b["next_action"].(map[string]any)
	redirect, _ := na["redirect_to_url"].(map[string]any)
	if na["type"] != "redirect_to_url" || redirect["url"] != "http://sandbox.test/_sandbox/ui/3ds/"+id ||
		redirect["return_url"] != "https://shop.test/return" {
		t.Fatalf("next_action %v", na)
	}

	s.authenticate(id, "success")
	got := s.form("GET", "/v1/payment_intents/"+id, "")
	if got.body["status"] != "succeeded" || got.body["next_action"] != nil || got.body["latest_charge"] == nil {
		t.Fatalf("after authentication: %v", got.body)
	}
	want := []string{"payment_intent.created", "payment_intent.requires_action", "charge.succeeded", "payment_intent.succeeded"}
	if hooks := waitHooks(t, s, id, 4); !slices.Equal(hooks, want) {
		t.Errorf("webhooks %v, want %v", hooks, want)
	}
}

func TestThreeDSecureWithoutReturnURL(t *testing.T) {
	s := newStripe(t)
	b := s.create(threeDS).body
	redirect := b["next_action"].(map[string]any)["redirect_to_url"].(map[string]any)
	if redirect["return_url"] != nil || redirect["url"] == nil {
		t.Errorf("redirect_to_url %v", redirect)
	}
}

func TestThreeDSecureFailedAuthentication(t *testing.T) {
	s := newStripe(t)
	id := s.create(threeDS).body["id"].(string)
	s.authenticate(id, "failure")
	b := s.form("GET", "/v1/payment_intents/"+id, "").body
	lpe, _ := b["last_payment_error"].(map[string]any)
	if b["status"] != "requires_payment_method" || lpe["code"] != "payment_intent_authentication_failure" ||
		lpe["type"] != "invalid_request_error" || b["latest_charge"] != nil {
		t.Fatalf("after failed authentication: %v", b)
	}
	want := []string{"payment_intent.created", "payment_intent.requires_action", "payment_intent.payment_failed"}
	if hooks := waitHooks(t, s, id, 3); !slices.Equal(hooks, want) {
		t.Errorf("webhooks %v, want %v (no charge for a failed authentication)", hooks, want)
	}
	// The customer tries another card.
	if r := s.form("POST", "/v1/payment_intents/"+id+"/confirm", "payment_method=pm_card_visa"); r.body["status"] != "succeeded" {
		t.Errorf("retry: %d %v", r.status, r.body)
	}
}

func TestThreeDSecureDeclinedAfterAuthentication(t *testing.T) {
	s := newStripe(t)
	id := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_threeDSecureRequiredChargeDeclined").body["id"].(string)
	s.authenticate(id, "success")
	lpe, _ := s.form("GET", "/v1/payment_intents/"+id, "").body["last_payment_error"].(map[string]any)
	if lpe["code"] != "card_declined" || lpe["type"] != "card_error" {
		t.Errorf("last_payment_error %v", lpe)
	}
}

func TestThreeDSecureOtherCardAndCancel(t *testing.T) {
	s := newStripe(t)
	id := s.create("amount=1000&currency=eur&confirm=true&payment_method=pm_card_authenticationRequired").body["id"].(string)
	r := s.form("POST", "/v1/payment_intents/"+id+"/cancel", "cancellation_reason=abandoned")
	if r.status != 200 || r.body["status"] != "canceled" || r.body["next_action"] != nil {
		t.Errorf("cancel while requires_action: %d %v", r.status, r.body)
	}
}

func TestThreeDSecurePageReturnsWithStripeParams(t *testing.T) {
	s := newStripe(t)
	id := s.create(threeDS + "&return_url=" + url.QueryEscape("https://shop.test/return?order=42")).body["id"].(string)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.Server.URL+"/_sandbox/ui/3ds/"+id+"?result=success", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *s.Server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || err != nil {
		t.Fatalf("submit: %d %v", resp.StatusCode, err)
	}
	q := loc.Query()
	if loc.Host != "shop.test" || q.Get("order") != "42" || q.Get("payment_intent") != id ||
		q.Get("payment_intent_client_secret") != id+"_secret_sandbox" || q.Get("redirect_status") != "succeeded" {
		t.Errorf("return to %s", loc)
	}
}

func TestReturnURLMustBeAbsolute(t *testing.T) {
	s := newStripe(t)
	if r := s.create(threeDS + "&return_url=/back"); r.status != 400 || r.err()["param"] != "return_url" {
		t.Errorf("relative return_url: %d %v", r.status, r.body)
	}
}
