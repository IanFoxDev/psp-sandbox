package ui_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

func get(t *testing.T, sb *sandboxtest.Sandbox, path string, want int) string {
	t.Helper()
	resp := sb.Do(http.MethodGet, path, nil)
	if resp.Status != want {
		t.Fatalf("GET %s: %d %s", path, resp.Status, resp.Body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET %s: content type %q", path, ct)
	}
	return string(resp.Body)
}

// post submits an empty form without following the redirect.
func post(t *testing.T, sb *sandboxtest.Sandbox, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, sb.Server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *sb.Server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

func contains(t *testing.T, page string, parts ...string) {
	t.Helper()
	for _, s := range parts {
		if !strings.Contains(page, s) {
			t.Errorf("page has no %q", s)
		}
	}
}

func TestList(t *testing.T) {
	sb := sandboxtest.New(t)
	contains(t, get(t, sb, "/_sandbox/", http.StatusOK), "No payments yet")

	a := sb.CreatePayment(map[string]any{"reference": "order-a"})
	b := sb.CreatePayment(map[string]any{"reference": "order-<b>"}, scenario.Header, "declined")
	sb.Receiver.Wait(2)
	sb.WaitStatus(b["id"].(string), "failed")

	page := get(t, sb, "/_sandbox/", http.StatusOK)
	contains(t, page, a["id"].(string), b["id"].(string), "declined", "st-failed", "2 payments",
		"order-&lt;b&gt;", "/_sandbox/ui/payments/"+a["id"].(string))
	if strings.Contains(page, "order-<b>") {
		t.Error("reference is not escaped")
	}
	if strings.Index(page, b["id"].(string)) > strings.Index(page, a["id"].(string)) {
		t.Error("newest payment is not first")
	}

	page = get(t, sb, "/_sandbox/?reference=order-a", http.StatusOK)
	contains(t, page, a["id"].(string), "1 payment<")
	if strings.Contains(page, b["id"].(string)) {
		t.Error("filter by reference shows other payments")
	}
}

func TestPaymentPage(t *testing.T) {
	sb := sandboxtest.New(t)
	p := sb.CreatePayment(map[string]any{"metadata": map[string]any{"order": "42"}}, scenario.Header, "duplicate_callback")
	sb.Receiver.Wait(2)
	id := p["id"].(string)

	page := get(t, sb, "/_sandbox/ui/payments/"+id, http.StatusOK)
	contains(t, page, id, "captured", "duplicate_callback", "metadata.order", "payment.captured",
		"copy 2", "Webhook-Signature", "Replay", "&#34;status&#34;: &#34;captured&#34;")

	get(t, sb, "/_sandbox/ui/payments/pay_NOPE", http.StatusNotFound)
}

func TestReplay(t *testing.T) {
	sb := sandboxtest.New(t)
	p := sb.CreatePayment(nil)
	sb.Receiver.Wait(1)
	id := p["id"].(string)
	dels := sb.Do(http.MethodGet, "/_sandbox/payments/"+id+"/deliveries", nil).JSON(t)["data"].([]any)
	dlv := dels[0].(map[string]any)["id"].(string)

	status, loc := post(t, sb, "/_sandbox/ui/deliveries/"+dlv+"/replay?refresh=2")
	if status != http.StatusSeeOther || loc != "/_sandbox/ui/payments/"+id+"?refresh=2" {
		t.Fatalf("replay: %d, Location %q", status, loc)
	}
	if cbs := sb.Receiver.Wait(2); cbs[1].ID != cbs[0].ID {
		t.Fatalf("replay sent event %s, want %s", cbs[1].ID, cbs[0].ID)
	}
	contains(t, get(t, sb, "/_sandbox/ui/payments/"+id, http.StatusOK), "replay of")

	if status, _ := post(t, sb, "/_sandbox/ui/deliveries/dlv_NOPE/replay"); status != http.StatusNotFound {
		t.Fatalf("replay of unknown delivery: %d", status)
	}
}

func TestResetAndRefresh(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.ManualClock = true })
	sb.CreatePayment(nil)

	contains(t, get(t, sb, "/_sandbox/?refresh=5", http.StatusOK),
		`<meta http-equiv="refresh" content="5">`, "(manual)", `action="/_sandbox/ui/reset?refresh=5"`)

	resp := sb.Do(http.MethodPost, "/_sandbox/ui/reset", nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("reset: %d", resp.Status)
	}
	contains(t, string(resp.Body), "No payments yet")
}
