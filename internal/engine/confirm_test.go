package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/ids"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/signing"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

const delay = 200 * time.Millisecond

func newEngine(t *testing.T) (*Engine, *clock.Manual) {
	t.Helper()
	clk := clock.NewManual(time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC))
	signer, err := signing.New(signing.NewRandomSecret())
	if err != nil {
		t.Fatal(err)
	}
	gen := ids.New("test")
	d := callback.New(callback.Options{Clock: clk, Signer: signer, IDs: gen})
	t.Cleanup(d.Close)
	e, err := New(Config{ProcessingDelay: delay, DefaultScenario: scenario.Spec{Name: "happy_path"}},
		Deps{Clock: clk, Store: store.New(), Dispatcher: d, Catalog: scenario.Builtin(), IDs: gen})
	if err != nil {
		t.Fatal(err)
	}
	return e, clk
}

func spec(name string) *scenario.Spec {
	return &scenario.Spec{Name: name}
}

func status(t *testing.T, e *Engine, id string) payment.Payment {
	t.Helper()
	p, err := e.Payment(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func eventTypes(t *testing.T, e *Engine, id string) []payment.EventType {
	t.Helper()
	evs, err := e.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	var out []payment.EventType
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

func TestPrepareStoresUnconfirmedWithoutScenario(t *testing.T) {
	e, clk := newEngine(t)
	p, err := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != payment.Unconfirmed || p.Attempt != 0 || p.Scenario != "" {
		t.Fatalf("got %s attempt %d scenario %q", p.Status, p.Attempt, p.Scenario)
	}
	if n := clk.Pending(); n != 0 {
		t.Fatalf("%d timers scheduled before confirm", n)
	}
	clk.Advance(time.Hour)
	if got := status(t, e, p.ID); got.Status != payment.Unconfirmed {
		t.Fatalf("status = %s after an hour", got.Status)
	}
	if got := eventTypes(t, e, p.ID); len(got) != 1 || got[0] != payment.EventPaymentCreated {
		t.Fatalf("events = %v", got)
	}
}

func TestConfirmRunsScenarioAndSettles(t *testing.T) {
	e, clk := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	c, err := e.Confirm(p.ID, ConfirmRequest{PaymentMethod: "pm_card_visa"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Payment.Status != payment.Pending || c.Payment.Attempt != 1 || c.Payment.Scenario != "happy_path" {
		t.Fatalf("got %s attempt %d scenario %q", c.Payment.Status, c.Payment.Attempt, c.Payment.Scenario)
	}
	if closed(c.Settled) {
		t.Fatal("settled before the first step")
	}
	clk.Advance(delay)
	<-c.Settled
	got := status(t, e, p.ID)
	if got.Status != payment.Captured || got.PaymentMethod != "pm_card_visa" {
		t.Fatalf("got %s with %q", got.Status, got.PaymentMethod)
	}
}

func TestConfirmAfterFailureStartsNewAttempt(t *testing.T) {
	e, clk := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	if _, err := e.Confirm(p.ID, ConfirmRequest{Scenario: spec("declined"), PaymentMethod: "pm_card_visa_chargeDeclined"}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(delay)
	if got := status(t, e, p.ID); got.Status != payment.Failed || got.FailureReason == "" {
		t.Fatalf("first attempt: %s %q", got.Status, got.FailureReason)
	}

	c, err := e.Confirm(p.ID, ConfirmRequest{PaymentMethod: "pm_card_visa"})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(delay)
	<-c.Settled
	got := status(t, e, p.ID)
	if got.Status != payment.Captured || got.Attempt != 2 || got.FailureReason != "" ||
		got.PaymentMethod != "pm_card_visa" || got.Scenario != "happy_path" {
		t.Fatalf("second attempt: %+v", got)
	}
}

func TestStepsOfEarlierAttemptAreSkipped(t *testing.T) {
	e, clk := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	// The first attempt plans a capture after the delay, but fails before it.
	if _, err := e.Confirm(p.ID, ConfirmRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Force(p.ID, ForceRequest{Type: payment.EventPaymentFailed}); err != nil {
		t.Fatal(err)
	}
	// The second attempt starts half way and captures one delay later.
	clk.Advance(delay / 2)
	if _, err := e.Confirm(p.ID, ConfirmRequest{}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(delay / 2)
	if got := status(t, e, p.ID); got.Status != payment.Pending {
		t.Fatalf("leftover step of attempt 1 applied: %s", got.Status)
	}
	clk.Advance(delay / 2)
	if got := status(t, e, p.ID); got.Status != payment.Captured || got.Attempt != 2 {
		t.Fatalf("attempt 2: %s attempt %d", got.Status, got.Attempt)
	}
}

func TestConfirmOnlyUnconfirmedOrFailed(t *testing.T) {
	e, clk := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	if _, err := e.Confirm(p.ID, ConfirmRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Confirm(p.ID, ConfirmRequest{}); !errors.Is(err, payment.ErrInvalidState) {
		t.Fatalf("confirm while pending: %v", err)
	}
	clk.Advance(delay)
	if _, err := e.Confirm(p.ID, ConfirmRequest{}); !errors.Is(err, payment.ErrInvalidState) {
		t.Fatalf("confirm after capture: %v", err)
	}
	if _, err := e.Confirm("pay_missing", ConfirmRequest{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("confirm unknown: %v", err)
	}
	q, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	if _, err := e.Confirm(q.ID, ConfirmRequest{Scenario: spec("no_such")}); !errors.Is(err, ErrInvalidScenario) {
		t.Fatalf("unknown scenario: %v", err)
	}
	if got := status(t, e, q.ID); got.Status != payment.Unconfirmed {
		t.Fatalf("after unknown scenario: %s", got.Status)
	}
}

func TestRefusedConfirmLeavesPaymentUnconfirmed(t *testing.T) {
	e, clk := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	req := ConfirmRequest{Scenario: spec("server_error_then_success"), RetryKey: "key-1"}
	var refused *RefusedError
	if _, err := e.Confirm(p.ID, req); !errors.As(err, &refused) {
		t.Fatalf("first confirm: %v", err)
	}
	if got := status(t, e, p.ID); got.Status != payment.Unconfirmed || got.Attempt != 0 {
		t.Fatalf("after refusal: %s attempt %d", got.Status, got.Attempt)
	}
	c, err := e.Confirm(p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(delay)
	<-c.Settled
	if got := status(t, e, p.ID); got.Status != payment.Captured || got.Attempt != 1 {
		t.Fatalf("after retry: %s attempt %d", got.Status, got.Attempt)
	}
}

func TestCancelUnconfirmed(t *testing.T) {
	e, _ := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	got, err := e.Cancel(p.ID)
	if err != nil || got.Status != payment.Canceled {
		t.Fatalf("got %s, %v", got.Status, err)
	}
}

func TestCreateSettlesAfterFirstStep(t *testing.T) {
	e, clk := newEngine(t)
	c, err := e.Create(CreateRequest{Amount: 1000, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Payment.Attempt != 1 || closed(c.Settled) {
		t.Fatalf("attempt %d, settled %v", c.Payment.Attempt, closed(c.Settled))
	}
	clk.Advance(delay)
	<-c.Settled
	if got := eventTypes(t, e, c.Payment.ID); len(got) != 1 || got[0] != payment.EventPaymentCaptured {
		t.Fatalf("events = %v, native create sends no payment.created", got)
	}
}

func TestPrepareAndConfirmRefusedStoresNothing(t *testing.T) {
	e, clk := newEngine(t)
	req := CreateRequest{Amount: 1000, Currency: "EUR"}
	conf := ConfirmRequest{Scenario: spec("server_error_then_success"), RetryKey: "key-1"}
	var refused *RefusedError
	if _, err := e.PrepareAndConfirm(req, conf); !errors.As(err, &refused) {
		t.Fatalf("first call: %v", err)
	}
	if n := len(e.Payments("")); n != 0 {
		t.Fatalf("%d payments after a refused call", n)
	}
	c, err := e.PrepareAndConfirm(req, conf)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(delay)
	<-c.Settled
	if got := eventTypes(t, e, c.Payment.ID); len(got) != 2 || got[0] != payment.EventPaymentCreated {
		t.Fatalf("events = %v", got)
	}
}

func TestAbandonOnlyUnconfirmedOrFailed(t *testing.T) {
	e, clk := newEngine(t)
	p, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	if got, err := e.Abandon(p.ID); err != nil || got.Status != payment.Canceled {
		t.Fatalf("unconfirmed: %s %v", got.Status, err)
	}
	q, _ := e.Prepare(CreateRequest{Amount: 1000, Currency: "EUR"})
	if _, err := e.Confirm(q.ID, ConfirmRequest{Scenario: spec("declined")}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(delay)
	if got, err := e.Abandon(q.ID); err != nil || got.Status != payment.Canceled || got.FailureReason == "" {
		t.Fatalf("failed: %+v %v", got, err)
	}
	c, _ := e.Create(CreateRequest{Amount: 1000, Currency: "EUR"})
	clk.Advance(delay)
	if _, err := e.Abandon(c.Payment.ID); !errors.Is(err, payment.ErrInvalidState) {
		t.Fatalf("captured: %v", err)
	}
}
