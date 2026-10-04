package stripe_test

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
)

const session = "mode=payment&success_url=" + "https%3A%2F%2Fshop.test%2Fdone%3Fs%3D%7BCHECKOUT_SESSION_ID%7D" +
	"&cancel_url=https%3A%2F%2Fshop.test%2Fcart&client_reference_id=order-42" +
	"&line_items[0][price_data][currency]=eur&line_items[0][price_data][unit_amount]=700" +
	"&line_items[0][price_data][product_data][name]=Tea&line_items[0][quantity]=2" +
	"&line_items[1][price_data][currency]=eur&line_items[1][price_data][unit_amount]=600" +
	"&line_items[1][price_data][product_data][name]=Cup&line_items[1][quantity]=1"

func (s *stripeBox) openSession(extra string) map[string]any {
	s.t.Helper()
	r := s.form("POST", "/v1/checkout/sessions", session+extra)
	if r.status != 200 {
		s.t.Fatalf("create session: %d %v", r.status, r.body)
	}
	return r.body
}

func (s *stripeBox) pay(id, pm string, headers ...string) map[string]any {
	s.t.Helper()
	r := s.Do(http.MethodPost, "/_sandbox/checkout/"+id+"/pay", map[string]any{"payment_method": pm}, headers...)
	if r.Status != http.StatusOK {
		s.t.Fatalf("pay %s with %s: %d %s", id, pm, r.Status, r.Body)
	}
	return r.JSON(s.t)
}

func TestCreateSession(t *testing.T) {
	s := newStripe(t)
	b := s.openSession("&metadata[cart]=7")
	id := b["id"].(string)
	if !strings.HasPrefix(id, "cs_test_") || b["object"] != "checkout.session" || b["status"] != "open" ||
		b["payment_status"] != "unpaid" || b["amount_total"] != 2000.0 || b["currency"] != "eur" ||
		b["payment_intent"] != nil || b["url"] != "http://sandbox.test/_sandbox/ui/checkout/"+id ||
		b["client_reference_id"] != "order-42" || b["metadata"].(map[string]any)["cart"] != "7" {
		t.Fatalf("session %v", b)
	}
	if exp := int64(b["expires_at"].(float64)) - int64(b["created"].(float64)); exp != 24*3600 {
		t.Errorf("expires after %ds, want 24h", exp)
	}
	items := s.form("GET", "/v1/checkout/sessions/"+id+"/line_items", "").body["data"].([]any)
	first := items[0].(map[string]any)
	if len(items) != 2 || first["description"] != "Tea" || first["quantity"] != 2.0 || first["amount_total"] != 1400.0 {
		t.Errorf("line items %v", items)
	}
	got := s.form("GET", "/v1/checkout/sessions/"+id+"?expand[]=line_items", "").body
	if _, ok := got["line_items"].(map[string]any); !ok {
		t.Errorf("expand line_items: %v", got["line_items"])
	}
}

func TestCreateSessionValidation(t *testing.T) {
	s := newStripe(t)
	item := "&line_items[0][price_data][currency]=eur&line_items[0][price_data][unit_amount]=100" +
		"&line_items[0][price_data][product_data][name]=Tea&line_items[0][quantity]=1"
	ok := "mode=payment&success_url=https%3A%2F%2Fshop.test%2Fok" + item
	now := time.Now().Unix()
	cases := map[string]string{
		strings.Replace(ok, "mode=payment", "mode=subscription", 1): "mode",
		"mode=payment&success_url=https%3A%2F%2Fshop.test%2Fok":     "line_items",
		"mode=payment" + item:    "success_url",
		ok + "&cancel_url=/cart": "cancel_url",
		ok + "&line_items[1][price]=price_123&line_items[1][quantity]=1":            "line_items[1][price]",
		strings.Replace(ok, "[quantity]=1", "[quantity]=0", 1):                      "line_items[0][quantity]",
		strings.Replace(ok, "[product_data][name]=Tea", "[product_data][name]=", 1): "line_items[0][price_data][product_data][name]",
		ok + "&line_items[1][price_data][currency]=usd&line_items[1][price_data][unit_amount]=1&line_items[1][price_data][product_data][name]=X&line_items[1][quantity]=1": "line_items[1][price_data][currency]",
		ok + "&payment_intent_data[capture_method]=manual":       "payment_intent_data[capture_method]",
		ok + "&expires_at=" + strconv.FormatInt(now+600, 10):     "expires_at",
		ok + "&expires_at=" + strconv.FormatInt(now+25*3600, 10): "expires_at",
		ok + "&metadata[sandbox_scenario]=nope":                  "metadata[sandbox_scenario]",
	}
	for body, param := range cases {
		if r := s.form("POST", "/v1/checkout/sessions", body); r.status != 400 || r.err()["param"] != param {
			t.Errorf("%s: %d %v", body, r.status, r.body)
		}
	}
	if r := s.form("POST", "/v1/checkout/sessions", ok+"&expires_at="+strconv.FormatInt(now+3600, 10)); r.status != 200 {
		t.Errorf("expires_at in an hour: %d %v", r.status, r.body)
	}
}

func TestPaySessionCompletesIt(t *testing.T) {
	s := newStripe(t)
	id := s.openSession("&customer_email=a%40shop.test&payment_intent_data[metadata][order]=42")["id"].(string)
	b := s.pay(id, "pm_card_visa")
	pi, _ := b["payment_intent"].(map[string]any)
	if b["status"] != "complete" || b["payment_status"] != "paid" || b["url"] != nil || pi["status"] != "succeeded" ||
		pi["amount"] != 2000.0 || pi["metadata"].(map[string]any)["order"] != "42" ||
		b["customer_details"].(map[string]any)["email"] != "a@shop.test" {
		t.Fatalf("after pay: %v", b)
	}
	intent := pi["id"].(string)
	want := []string{"payment_intent.created", "charge.succeeded", "payment_intent.succeeded", "checkout.session.completed"}
	if got := waitHooks(t, s, intent, 4); !slices.Equal(got, want) {
		t.Errorf("webhooks %v, want %v", got, want)
	}
	for _, action := range []string{"confirm", "cancel"} {
		if r := s.form("POST", "/v1/payment_intents/"+intent+"/"+action, ""); r.status != 400 ||
			!strings.Contains(r.err()["message"].(string), "Checkout Session") {
			t.Errorf("%s the session's PaymentIntent: %d %v", action, r.status, r.body)
		}
	}
	if r := s.Do(http.MethodPost, "/_sandbox/checkout/"+id+"/pay", map[string]any{"payment_method": "pm_card_visa"}); r.Status != http.StatusConflict {
		t.Errorf("pay a complete session: %d %s", r.Status, r.Body)
	}
	if list := s.form("GET", "/v1/checkout/sessions?payment_intent="+intent, "").body["data"].([]any); len(list) != 1 {
		t.Errorf("list by payment_intent: %v", list)
	}
}

func TestPaySessionAfterDeclineAndWith3DS(t *testing.T) {
	s := newStripe(t)
	id := s.openSession("")["id"].(string)
	b := s.pay(id, "pm_card_visa_chargeDeclined")
	pi := b["payment_intent"].(map[string]any)
	if b["status"] != "open" || pi["status"] != "requires_payment_method" {
		t.Fatalf("after decline: %v", b)
	}
	b = s.pay(id, "pm_card_threeDSecure2Required")
	if b["status"] != "open" || b["payment_intent"].(map[string]any)["status"] != "requires_action" ||
		b["payment_intent"].(map[string]any)["id"] != pi["id"] {
		t.Fatalf("3DS on the same PaymentIntent: %v", b)
	}
	s.authenticate(pi["id"].(string), "success")
	if got := s.form("GET", "/v1/checkout/sessions/"+id, "").body; got["status"] != "complete" {
		t.Errorf("after authentication: %v", got)
	}
	if n := len(s.form("GET", "/v1/payment_intents", "").body["data"].([]any)); n != 1 {
		t.Errorf("%d PaymentIntents for one session", n)
	}
}

func TestExpireSession(t *testing.T) {
	s := newStripe(t)
	id := s.openSession("")["id"].(string)
	intent := s.pay(id, "pm_card_visa_chargeDeclined")["payment_intent"].(map[string]any)["id"].(string)
	r := s.form("POST", "/v1/checkout/sessions/"+id+"/expire", "")
	if r.status != 200 || r.body["status"] != "expired" || r.body["url"] != nil {
		t.Fatalf("expire: %d %v", r.status, r.body)
	}
	pi := s.form("GET", "/v1/payment_intents/"+intent, "").body
	if pi["status"] != "canceled" || pi["cancellation_reason"] != "expired" {
		t.Errorf("PaymentIntent of an expired session: %v", pi)
	}
	want := []string{"payment_intent.created", "charge.failed", "payment_intent.payment_failed",
		"checkout.session.expired", "payment_intent.canceled"}
	if got := waitHooks(t, s, intent, len(want)); !slices.Equal(got, want) {
		t.Errorf("webhooks %v, want %v", got, want)
	}
	if r := s.form("POST", "/v1/checkout/sessions/"+id+"/expire", ""); r.status != 400 {
		t.Errorf("expire again: %d", r.status)
	}
	if r := s.form("POST", "/v1/checkout/sessions/cs_test_nope/expire", ""); r.status != 404 {
		t.Errorf("unknown: %d", r.status)
	}
}

func TestSessionExpiresOnTheClock(t *testing.T) {
	s := newStripe(t, func(c *config.Config) { c.ManualClock = true })
	id := s.openSession("")["id"].(string)
	s.Do(http.MethodPost, "/_sandbox/clock/advance", map[string]any{"seconds": 24*3600 - 1})
	if got := s.form("GET", "/v1/checkout/sessions/"+id, "").body; got["status"] != "open" {
		t.Fatalf("a second early: %v", got["status"])
	}
	s.Do(http.MethodPost, "/_sandbox/clock/advance", map[string]any{"seconds": 1})
	if got := s.form("GET", "/v1/checkout/sessions/"+id, "").body; got["status"] != "expired" {
		t.Fatalf("at expires_at: %v", got["status"])
	}
	cb := s.Receiver.Wait(1)[0]
	if cb.Type != "checkout.session.expired" || !cb.Signed || cb.Data["object"].(map[string]any)["id"] != id {
		t.Errorf("webhook %s %s", cb.Type, cb.Body)
	}
}

func TestSessionScenarios(t *testing.T) {
	cases := map[string][]string{
		"duplicate_callback; times=2": {"payment_intent.created", "charge.succeeded", "charge.succeeded",
			"payment_intent.succeeded", "payment_intent.succeeded", "checkout.session.completed", "checkout.session.completed"},
		"out_of_order; window=200ms": {"payment_intent.created", "checkout.session.completed", "payment_intent.succeeded", "charge.succeeded"},
	}
	for spec, want := range cases {
		t.Run(strings.Fields(spec)[0], func(t *testing.T) {
			s := newStripe(t)
			id := s.openSession("&payment_intent_data[metadata][sandbox_scenario]=" + url.QueryEscape(spec))["id"].(string)
			intent := s.pay(id, "pm_card_visa")["payment_intent"].(map[string]any)["id"].(string)
			if got := waitHooks(t, s, intent, len(want)); !slices.Equal(got, want) {
				t.Errorf("webhooks %v, want %v", got, want)
			}
		})
	}
}

func TestResetByPrefixDropsSessions(t *testing.T) {
	s := newStripe(t)
	keep := s.openSession("")["id"].(string)
	drop := s.form("POST", "/v1/checkout/sessions", strings.Replace(session, "order-42", "test-1-order", 1)).body["id"].(string)
	if r := s.Do(http.MethodPost, "/_sandbox/reset", map[string]any{"reference_prefix": "test-1-"}); r.Status != http.StatusNoContent {
		t.Fatalf("reset: %d", r.Status)
	}
	if r := s.form("GET", "/v1/checkout/sessions/"+drop, ""); r.status != 404 {
		t.Errorf("dropped session: %d", r.status)
	}
	if r := s.form("GET", "/v1/checkout/sessions/"+keep, ""); r.status != 200 {
		t.Errorf("other session: %d", r.status)
	}
}

// browser submits a form and does not follow the redirect.
func (s *stripeBox) browser(method, path string, form url.Values) (int, string, string) {
	s.t.Helper()
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.Server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *s.Server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		body.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, resp.Header.Get("Location"), body.String()
}

func TestCheckoutPage(t *testing.T) {
	s := newStripe(t)
	id := s.openSession("")["id"].(string)
	page := "/_sandbox/ui/checkout/" + id

	status, _, html := s.browser(http.MethodGet, page, nil)
	for _, want := range []string{"Tea", "Cup", "2000", "pm_card_threeDSecure2Required", `id="pay"`, "https://shop.test/cart"} {
		if status != 200 || !strings.Contains(html, want) {
			t.Errorf("page: %d, no %q", status, want)
		}
	}
	if status, _, html := s.browser(http.MethodPost, page, url.Values{"payment_method": {"pm_card_visa_chargeDeclined"}}); status != http.StatusPaymentRequired ||
		!strings.Contains(html, "declined") || !strings.Contains(html, `id="pay"`) {
		t.Errorf("declined: %d", status)
	}
	status, location, _ := s.browser(http.MethodPost, page, url.Values{"payment_method": {"pm_card_threeDSecure2Required"}})
	if status != http.StatusSeeOther || !strings.Contains(location, "/_sandbox/ui/3ds/pi_") {
		t.Fatalf("3DS: %d %q", status, location)
	}
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	threeDSPath := u.Path
	status, back, _ := s.browser(http.MethodPost, threeDSPath, url.Values{"result": {"success"}})
	if status != http.StatusSeeOther || !strings.HasPrefix(back, "http://sandbox.test"+page+"?") || !strings.Contains(back, "redirect_status=succeeded") {
		t.Fatalf("back from 3DS: %d %q", status, back)
	}
	status, done, _ := s.browser(http.MethodGet, page, nil)
	if status != http.StatusSeeOther || done != "https://shop.test/done?s="+id {
		t.Errorf("complete session page: %d %q", status, done)
	}
	if status, _, _ := s.browser(http.MethodGet, "/_sandbox/ui/checkout/cs_test_nope", nil); status != 404 {
		t.Errorf("unknown session page: %d", status)
	}
}
