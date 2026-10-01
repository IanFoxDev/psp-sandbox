package scenario_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/api"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

var order = map[string]any{"amount": 1000, "currency": "EUR", "reference": "order-1"}

// The first call fails and creates nothing. A retry with the same key creates
// exactly one payment, and later retries get that payment back.
func TestServerErrorThenSuccess(t *testing.T) {
	sb := sandboxtest.New(t)
	call := func() sandboxtest.Response {
		return sb.Do(http.MethodPost, "/v1/payments", order,
			scenario.Header, "server_error_then_success", api.HeaderIdempotencyKey, "order-1")
	}

	first := call()
	if first.Status != http.StatusServiceUnavailable || first.JSON(t)["error"].(map[string]any)["code"] != "server_error" {
		t.Fatalf("first call: %d %s", first.Status, first.Body)
	}
	if n := paymentCount(t, sb); n != 0 {
		t.Fatalf("payments after the failed call: %d", n)
	}

	second := call()
	if second.Status != http.StatusCreated || second.Header.Get(api.HeaderReplayed) != "" {
		t.Fatalf("second call: %d %v %s", second.Status, second.Header, second.Body)
	}
	third := call()
	if third.Status != http.StatusCreated || third.Header.Get(api.HeaderReplayed) != "true" ||
		third.JSON(t)["id"] != second.JSON(t)["id"] {
		t.Fatalf("third call: %d %v %s", third.Status, third.Header, third.Body)
	}

	if n := paymentCount(t, sb); n != 1 {
		t.Fatalf("payments: %d, want 1", n)
	}
	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.captured" {
		t.Fatalf("callback = %s", cb.Body)
	}
	sb.Receiver.Quiet(1, 50*time.Millisecond)
}

func TestServerErrorThenSuccessSeveralFailures(t *testing.T) {
	sb := sandboxtest.New(t)
	header := "server_error_then_success; failures=3; status=502"
	for i := range 3 {
		resp := sb.Do(http.MethodPost, "/v1/payments", order, scenario.Header, header, api.HeaderIdempotencyKey, "k")
		if resp.Status != http.StatusBadGateway {
			t.Fatalf("call %d: %d %s", i+1, resp.Status, resp.Body)
		}
	}
	sb.CreatePayment(nil, scenario.Header, header, api.HeaderIdempotencyKey, "k")
}

// Without a key the sandbox cannot tell a retry by its key, so the same
// request counts as a retry and another request starts its own count.
func TestServerErrorThenSuccessWithoutKey(t *testing.T) {
	sb := sandboxtest.New(t)
	header := "server_error_then_success"
	if resp := sb.Do(http.MethodPost, "/v1/payments", order, scenario.Header, header); resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("first call: %d %s", resp.Status, resp.Body)
	}
	other := map[string]any{"amount": 1000, "currency": "EUR", "reference": "order-2"}
	if resp := sb.Do(http.MethodPost, "/v1/payments", other, scenario.Header, header); resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("another request: %d %s", resp.Status, resp.Body)
	}
	sb.CreatePayment(nil, scenario.Header, header)
}

func TestServerErrorThenSuccessReset(t *testing.T) {
	sb := sandboxtest.New(t)
	call := func() int {
		return sb.Do(http.MethodPost, "/v1/payments", order, scenario.Header, "server_error_then_success").Status
	}
	call()
	sb.Do(http.MethodPost, "/_sandbox/reset", nil)
	if status := call(); status != http.StatusServiceUnavailable {
		t.Fatalf("first call after reset: %d", status)
	}
}

func TestServerErrorThenSuccessBadStatus(t *testing.T) {
	sb := sandboxtest.New(t)
	resp := sb.Do(http.MethodPost, "/v1/payments", order, scenario.Header, "server_error_then_success; status=418")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("status=418: %d %s", resp.Status, resp.Body)
	}
}

func paymentCount(t *testing.T, sb *sandboxtest.Sandbox) int {
	t.Helper()
	return len(sb.Do(http.MethodGet, "/v1/payments", nil).JSON(t)["data"].([]any))
}
