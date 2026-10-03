package payment

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func TestTransitions(t *testing.T) {
	all := []Status{Unconfirmed, Pending, RequiresAction, Authorized, Captured, PartiallyRefunded, Refunded, Failed,
		Canceled, Disputed, ChargebackLost, ChargebackWon}
	allowed := map[[2]Status]bool{
		{Unconfirmed, Pending}:                 true,
		{Unconfirmed, Canceled}:                true,
		{Pending, RequiresAction}:              true,
		{RequiresAction, Authorized}:           true,
		{RequiresAction, Captured}:             true,
		{RequiresAction, Failed}:               true,
		{RequiresAction, Canceled}:             true,
		{Pending, Authorized}:                  true,
		{Pending, Captured}:                    true,
		{Pending, Failed}:                      true,
		{Pending, Canceled}:                    true,
		{Authorized, Captured}:                 true,
		{Authorized, Canceled}:                 true,
		{Captured, PartiallyRefunded}:          true,
		{Captured, Refunded}:                   true,
		{Captured, Disputed}:                   true,
		{PartiallyRefunded, PartiallyRefunded}: true,
		{PartiallyRefunded, Refunded}:          true,
		{PartiallyRefunded, Disputed}:          true,
		{Disputed, ChargebackLost}:             true,
		{Disputed, ChargebackWon}:              true,
	}
	for _, from := range all {
		for _, to := range all {
			if got, want := from.CanBecome(to), allowed[[2]Status{from, to}]; got != want {
				t.Errorf("%s -> %s: CanBecome = %v, want %v", from, to, got, want)
			}
		}
	}
	for _, s := range []Status{Refunded, Failed, Canceled, ChargebackLost, ChargebackWon} {
		if !s.Final() {
			t.Errorf("%s should be final", s)
		}
	}
}

func TestCaptureAfterAuthorize(t *testing.T) {
	p := newPayment()
	if err := p.Authorize(now); err != nil {
		t.Fatal(err)
	}
	if err := p.CaptureAmount(700, now); err != nil {
		t.Fatal(err)
	}
	if p.Status != Captured || p.CapturedAmount != 700 {
		t.Fatalf("got %s captured %d", p.Status, p.CapturedAmount)
	}
}

func TestCaptureDefaultsToFullAmount(t *testing.T) {
	p := newPayment()
	if err := p.CaptureAmount(0, now); err != nil {
		t.Fatal(err)
	}
	if p.CapturedAmount != 1000 {
		t.Fatalf("captured %d", p.CapturedAmount)
	}
}

func TestCaptureRejectsWrongAmount(t *testing.T) {
	for _, amount := range []int64{-1, 1001} {
		p := newPayment()
		if err := p.CaptureAmount(amount, now); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("amount %d: err = %v", amount, err)
		}
		if p.Status != Pending {
			t.Errorf("amount %d: status changed to %s", amount, p.Status)
		}
	}
}

func TestCannotCaptureFailedPayment(t *testing.T) {
	p := newPayment()
	if err := p.Fail("do_not_honor", now); err != nil {
		t.Fatal(err)
	}
	if err := p.CaptureAmount(0, now); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
	if p.FailureReason != "do_not_honor" {
		t.Fatalf("reason = %q", p.FailureReason)
	}
}

func TestRefunds(t *testing.T) {
	p := newPayment()
	if err := p.ReserveRefund(100); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("refund of pending payment: err = %v", err)
	}
	must(t, p.CaptureAmount(0, now))

	must(t, p.ReserveRefund(300))
	must(t, p.ReserveRefund(600))
	if err := p.ReserveRefund(101); !errors.Is(err, ErrAmountExceedsCaptured) {
		t.Fatalf("over-refund with pending refunds: err = %v", err)
	}
	if err := p.ReserveRefund(0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero refund: err = %v", err)
	}

	must(t, p.SettleRefund(300, now))
	if p.Status != PartiallyRefunded || p.RefundedAmount != 300 {
		t.Fatalf("after first refund: %s %d", p.Status, p.RefundedAmount)
	}

	p.ReleaseRefund(600)
	if p.Refundable() != 700 {
		t.Fatalf("Refundable = %d, want 700", p.Refundable())
	}

	must(t, p.ReserveRefund(700))
	must(t, p.SettleRefund(700, now))
	if p.Status != Refunded || p.RefundedAmount != 1000 || p.RefundPending != 0 {
		t.Fatalf("after full refund: %+v", p)
	}
}

func TestCloneDoesNotShareMetadata(t *testing.T) {
	p := newPayment()
	p.Metadata = map[string]string{"k": "v"}
	c := p.Clone()
	c.Metadata["k"] = "changed"
	if p.Metadata["k"] != "v" {
		t.Fatal("Clone shares the metadata map")
	}
}

func TestEventTypeFor(t *testing.T) {
	cases := map[Status]EventType{
		Captured:       EventPaymentCaptured,
		Failed:         EventPaymentFailed,
		Disputed:       EventChargebackOpened,
		ChargebackWon:  EventChargebackClosed,
		ChargebackLost: EventChargebackClosed,
	}
	for s, want := range cases {
		if got, ok := EventTypeFor(s); !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", s, got, ok, want)
		}
	}
	if _, ok := EventTypeFor(Refunded); ok {
		t.Error("refunded should not have a payment event")
	}
}

func newPayment() *Payment {
	return &Payment{ID: "pay_1", Status: Pending, Amount: 1000, Currency: "EUR", CreatedAt: now}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestConfirm(t *testing.T) {
	p := newPayment()
	p.Status = Unconfirmed
	if err := p.Confirm(now); err != nil {
		t.Fatal(err)
	}
	if p.Status != Pending || p.Attempt != 1 {
		t.Fatalf("got %s attempt %d", p.Status, p.Attempt)
	}
	if err := p.Fail("insufficient_funds", now); err != nil {
		t.Fatal(err)
	}
	if !p.Status.Final() {
		t.Fatal("failed should stay final for status changes")
	}
	if err := p.Confirm(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if p.Status != Pending || p.Attempt != 2 || p.FailureReason != "" || !p.UpdatedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("new attempt: %+v", p)
	}
	if err := p.CaptureAmount(0, now); err != nil {
		t.Fatal(err)
	}
	if err := p.Confirm(now); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("confirm captured: %v", err)
	}
}

func TestLeavingRequiresActionDropsTheActionURL(t *testing.T) {
	p := newPayment()
	must(t, p.Become(RequiresAction, now))
	p.ActionURL = "http://sandbox.test/_sandbox/ui/3ds/pay_1"
	must(t, p.CaptureAmount(0, now))
	if p.ActionURL != "" {
		t.Fatalf("action_url kept after capture: %q", p.ActionURL)
	}
}
