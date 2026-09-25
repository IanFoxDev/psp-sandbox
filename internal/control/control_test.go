package control_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/api"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

func TestScenarios(t *testing.T) {
	sb := sandboxtest.New(t)
	resp := sb.Do(http.MethodGet, "/_sandbox/scenarios", nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	byName := map[string]map[string]any{}
	for _, d := range resp.JSON(t)["data"].([]any) {
		def := d.(map[string]any)
		byName[def["name"].(string)] = def
	}
	for _, name := range []string{"happy_path", "declined", "duplicate_callback", "callback_before_response",
		"timeout_then_success", "lost_callback", "delayed_callback"} {
		if byName[name] == nil {
			t.Errorf("catalog has no %s", name)
		}
	}
	if params := byName["happy_path"]["params"].([]any); len(params) != 0 {
		t.Errorf("happy_path params = %v", params)
	}
	times := byName["duplicate_callback"]["params"].([]any)[0].(map[string]any)
	if times["name"] != "times" || times["type"] != "int" || times["default"] != "2" {
		t.Errorf("duplicate_callback times = %v", times)
	}
}

func TestDeliveriesAndEvents(t *testing.T) {
	sb := sandboxtest.New(t)
	sb.Receiver.Respond(func(sandboxtest.Callback) int { return http.StatusTeapot })
	id := sb.CreatePayment(nil)["id"].(string)
	sb.Receiver.Wait(3) // three attempts in the test retry schedule

	dels := waitDeliveries(t, sb, id, "failed")
	d := dels[0].(map[string]any)
	attempts := d["attempts"].([]any)
	if len(attempts) != 3 || d["event_type"] != "payment.captured" || d["url"] != sb.Receiver.URL() {
		t.Fatalf("delivery = %v", d)
	}
	first := attempts[0].(map[string]any)
	if first["status_code"] != 418.0 || first["request_headers"].(map[string]any)["Webhook-Signature"] == nil {
		t.Fatalf("attempt = %v", first)
	}
	if d["body"].(map[string]any)["type"] != "payment.captured" {
		t.Fatalf("body = %v", d["body"])
	}

	events := sb.Do(http.MethodGet, "/_sandbox/payments/"+id+"/events", nil).JSON(t)["data"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["id"] != d["event_id"] {
		t.Fatalf("events = %v", events)
	}

	for _, path := range []string{"/_sandbox/payments/pay_missing/deliveries", "/_sandbox/payments/pay_missing/events"} {
		if resp := sb.Do(http.MethodGet, path, nil); resp.Status != http.StatusNotFound {
			t.Errorf("%s: %d", path, resp.Status)
		}
	}
}

// A test can make the application miss a callback and then deliver it again,
// for example after fixing the handler.
func TestReplay(t *testing.T) {
	sb := sandboxtest.New(t)
	sb.Receiver.Respond(func(sandboxtest.Callback) int { return http.StatusInternalServerError })
	id := sb.CreatePayment(nil)["id"].(string)
	dels := waitDeliveries(t, sb, id, "failed")
	sb.Receiver.Respond(nil)
	before := len(sb.Receiver.All())

	resp := sb.Do(http.MethodPost, "/_sandbox/deliveries/"+dels[0].(map[string]any)["id"].(string)+"/replay", nil)
	if resp.Status != http.StatusAccepted {
		t.Fatalf("replay: %d %s", resp.Status, resp.Body)
	}
	replay := resp.JSON(t)
	if replay["replay_of"] != dels[0].(map[string]any)["id"] {
		t.Fatalf("replay = %v", replay)
	}
	cb := sb.Receiver.Wait(before + 1)[before]
	if cb.ID != replay["event_id"] || !cb.Signed {
		t.Fatalf("replayed callback = %s", cb.Body)
	}

	if resp := sb.Do(http.MethodPost, "/_sandbox/deliveries/dlv_missing/replay", nil); resp.Status != http.StatusNotFound {
		t.Fatalf("unknown delivery: %d", resp.Status)
	}
}

func TestForceChargeback(t *testing.T) {
	sb := sandboxtest.New(t)
	id := sb.CreatePayment(nil)["id"].(string)
	sb.WaitStatus(id, "captured")
	sb.Receiver.Wait(1)

	resp := sb.Do(http.MethodPost, "/_sandbox/payments/"+id+"/events", map[string]any{"type": "chargeback.opened"})
	if resp.Status != http.StatusCreated {
		t.Fatalf("force: %d %s", resp.Status, resp.Body)
	}
	out := resp.JSON(t)
	if out["payment"].(map[string]any)["status"] != "disputed" || out["event"].(map[string]any)["type"] != "chargeback.opened" {
		t.Fatalf("force = %v", out)
	}
	if cb := sb.Receiver.Wait(2)[1]; cb.Type != "chargeback.opened" || cb.Data["status"] != "disputed" {
		t.Fatalf("callback = %s", cb.Body)
	}

	resp = sb.Do(http.MethodPost, "/_sandbox/payments/"+id+"/events", map[string]any{"type": "chargeback.closed", "outcome": "won"})
	if resp.Status != http.StatusCreated {
		t.Fatalf("close: %d %s", resp.Status, resp.Body)
	}
	if cb := sb.Receiver.Wait(3)[2]; cb.Type != "chargeback.closed" || cb.Data["status"] != "chargeback_won" {
		t.Fatalf("callback = %s", cb.Body)
	}
}

func TestForceErrors(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.ProcessingDelay = time.Hour })
	id := sb.CreatePayment(nil)["id"].(string)
	cases := []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"type": "chargeback.opened"}, http.StatusConflict, "invalid_state"},
		{map[string]any{"type": "refund.succeeded"}, http.StatusBadRequest, "invalid_request"},
		{map[string]any{"type": "chargeback.closed", "outcome": "draw"}, http.StatusBadRequest, "invalid_request"},
	}
	for _, c := range cases {
		resp := sb.Do(http.MethodPost, "/_sandbox/payments/"+id+"/events", c.body)
		if resp.Status != c.status || errorCode(t, resp) != c.code {
			t.Errorf("%v: %d %s", c.body, resp.Status, resp.Body)
		}
	}
	if resp := sb.Do(http.MethodPost, "/_sandbox/payments/pay_missing/events", map[string]any{"type": "payment.captured"}); resp.Status != http.StatusNotFound {
		t.Errorf("unknown payment: %d", resp.Status)
	}
}

func TestClockAdvance(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) {
		c.ManualClock = true
		c.ProcessingDelay = 30 * time.Second
	})
	before := sb.Do(http.MethodGet, "/_sandbox/clock", nil).JSON(t)
	if before["manual"] != true {
		t.Fatalf("clock = %v", before)
	}
	id := sb.CreatePayment(nil)["id"].(string)

	resp := sb.Do(http.MethodPost, "/_sandbox/clock/advance", map[string]any{"seconds": 30})
	if resp.Status != http.StatusOK {
		t.Fatalf("advance: %d %s", resp.Status, resp.Body)
	}
	start, _ := time.Parse(time.RFC3339Nano, before["now"].(string))
	now, _ := time.Parse(time.RFC3339Nano, resp.JSON(t)["now"].(string))
	if now.Sub(start) != 30*time.Second {
		t.Fatalf("clock moved %v, want 30s", now.Sub(start))
	}
	// Due status changes are applied before advance answers.
	if got := sb.Payment(id); got["status"] != "captured" {
		t.Fatalf("status right after advance = %v", got["status"])
	}
	sb.Receiver.Wait(1)

	if resp := sb.Do(http.MethodPost, "/_sandbox/clock/advance", map[string]any{"seconds": 0}); resp.Status != http.StatusBadRequest {
		t.Fatalf("zero seconds: %d", resp.Status)
	}
}

func TestClockAdvanceNeedsManualClock(t *testing.T) {
	sb := sandboxtest.New(t)
	resp := sb.Do(http.MethodPost, "/_sandbox/clock/advance", map[string]any{"seconds": 1})
	if resp.Status != http.StatusConflict || errorCode(t, resp) != "clock_not_manual" {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
}

func TestReset(t *testing.T) {
	sb := sandboxtest.New(t)
	sb.Receiver.Respond(func(sandboxtest.Callback) int { return http.StatusInternalServerError })
	body := map[string]any{"amount": 1000, "currency": "EUR", "reference": "r-1"}
	first := sb.Do(http.MethodPost, "/v1/payments", body, api.HeaderIdempotencyKey, "k-1", scenario.Header, "delayed_callback; delay=200ms")
	id := first.JSON(t)["id"].(string)

	if resp := sb.Do(http.MethodPost, "/_sandbox/reset", nil); resp.Status != http.StatusNoContent {
		t.Fatalf("reset: %d", resp.Status)
	}
	if resp := sb.Do(http.MethodGet, "/v1/payments/"+id, nil); resp.Status != http.StatusNotFound {
		t.Fatalf("payment after reset: %d", resp.Status)
	}
	// The delayed callback of the dropped payment never arrives.
	sb.Receiver.Quiet(0, 300*time.Millisecond)

	again := sb.Do(http.MethodPost, "/v1/payments", body, api.HeaderIdempotencyKey, "k-1")
	if again.Status != http.StatusCreated || again.Header.Get(api.HeaderReplayed) != "" {
		t.Fatalf("idempotency key survived reset: %d %s", again.Status, again.Body)
	}
}

func waitDeliveries(t *testing.T, sb *sandboxtest.Sandbox, paymentID, status string) []any {
	t.Helper()
	deadline := time.Now().Add(sandboxtest.Wait)
	for {
		dels := sb.Do(http.MethodGet, "/_sandbox/payments/"+paymentID+"/deliveries", nil).JSON(t)["data"].([]any)
		if len(dels) > 0 && dels[0].(map[string]any)["status"] == status {
			return dels
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries of %s never reached %s: %v", paymentID, status, dels)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func errorCode(t *testing.T, r sandboxtest.Response) string {
	t.Helper()
	e, _ := r.JSON(t)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
