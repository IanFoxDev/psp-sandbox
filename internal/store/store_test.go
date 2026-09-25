package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

var now = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func TestPaymentsAreCopies(t *testing.T) {
	s := New()
	p := captured("pay_1", 1000)
	p.Metadata = map[string]string{"k": "v"}
	s.AddPayment(p)
	p.Metadata["k"] = "changed"

	got, err := s.Payment("pay_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["k"] != "v" {
		t.Fatal("store shares metadata with the caller")
	}
}

func TestUpdatePaymentKeepsStateOnError(t *testing.T) {
	s := New()
	s.AddPayment(payment.Payment{ID: "pay_1", Status: payment.Pending, Amount: 1000})
	_, err := s.UpdatePayment("pay_1", func(p *payment.Payment) error {
		p.Amount = 1
		return errors.New("no")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	got, _ := s.Payment("pay_1")
	if got.Amount != 1000 {
		t.Fatalf("amount = %d, want 1000", got.Amount)
	}
	if _, err := s.UpdatePayment("pay_x", func(*payment.Payment) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: err = %v", err)
	}
}

func TestPaymentsByReference(t *testing.T) {
	s := New()
	for i, ref := range []string{"a", "b", "a"} {
		s.AddPayment(payment.Payment{ID: string(rune('1' + i)), Reference: ref})
	}
	got := s.Payments("a")
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("got %+v", got)
	}
	if len(s.Payments("")) != 3 {
		t.Fatal("empty reference should return all payments")
	}
}

// Concurrent refunds must never refund more than was captured.
func TestConcurrentRefundsDoNotOverRefund(t *testing.T) {
	s := New()
	s.AddPayment(captured("pay_1", 1000))

	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.AddRefund("pay_1", func(p *payment.Payment) (payment.Refund, error) {
				if err := p.ReserveRefund(100); err != nil {
					return payment.Refund{}, err
				}
				return payment.Refund{ID: string(rune('a' + i)), PaymentID: p.ID, Amount: 100, Status: payment.RefundPending}, nil
			})
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, payment.ErrAmountExceedsCaptured) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if accepted != 10 {
		t.Fatalf("accepted %d refunds of 100 on a 1000 payment, want 10", accepted)
	}
	p, _ := s.Payment("pay_1")
	if p.RefundPending != 1000 {
		t.Fatalf("RefundPending = %d", p.RefundPending)
	}
}

func TestUpdateRefundChangesBoth(t *testing.T) {
	s := New()
	s.AddPayment(captured("pay_1", 1000))
	_, r, err := s.AddRefund("pay_1", func(p *payment.Payment) (payment.Refund, error) {
		return payment.Refund{ID: "ref_1", PaymentID: p.ID, Amount: 400, Status: payment.RefundPending}, p.ReserveRefund(400)
	})
	if err != nil {
		t.Fatal(err)
	}
	p, r, err := s.UpdateRefund(r.ID, func(p *payment.Payment, r *payment.Refund) error {
		r.Status = payment.RefundSucceeded
		return p.SettleRefund(r.Amount, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != payment.PartiallyRefunded || p.RefundedAmount != 400 || r.Status != payment.RefundSucceeded {
		t.Fatalf("payment %+v refund %+v", p, r)
	}
}

func TestEvents(t *testing.T) {
	s := New()
	s.AddEvent(payment.Event{ID: "evt_1", PaymentID: "pay_1"})
	s.AddEvent(payment.Event{ID: "evt_2", PaymentID: "pay_1"})
	s.AddEvent(payment.Event{ID: "evt_3", PaymentID: "pay_2"})
	got := s.Events("pay_1")
	if len(got) != 2 || got[1].ID != "evt_2" {
		t.Fatalf("got %+v", got)
	}
}

func TestReset(t *testing.T) {
	s := New()
	s.AddPayment(captured("pay_1", 1000))
	s.AddEvent(payment.Event{ID: "evt_1", PaymentID: "pay_1"})
	_, _ = s.BeginIdempotent("k", "f")
	s.Reset()
	if _, err := s.Payment("pay_1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("payment survived Reset")
	}
	if len(s.Events("pay_1")) != 0 {
		t.Fatal("events survived Reset")
	}
	if r, err := s.BeginIdempotent("k", "other"); r != nil || err != nil {
		t.Fatal("idempotency key survived Reset")
	}
}

func TestIdempotency(t *testing.T) {
	s := New()
	if r, err := s.BeginIdempotent("k", "f1"); r != nil || err != nil {
		t.Fatalf("first use: %v %v", r, err)
	}
	if _, err := s.BeginIdempotent("k", "f1"); !errors.Is(err, ErrIdempotencyInProgress) {
		t.Fatalf("while running: err = %v", err)
	}
	s.FinishIdempotent("k", Response{Status: 201, Body: []byte("{}")})
	r, err := s.BeginIdempotent("k", "f1")
	if err != nil || r == nil || r.Status != 201 {
		t.Fatalf("replay: %v %v", r, err)
	}
	if _, err := s.BeginIdempotent("k", "f2"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("other body: err = %v", err)
	}
}

func TestIdempotencyAbort(t *testing.T) {
	s := New()
	_, _ = s.BeginIdempotent("k", "f1")
	s.AbortIdempotent("k")
	if r, err := s.BeginIdempotent("k", "f2"); r != nil || err != nil {
		t.Fatalf("after abort: %v %v", r, err)
	}
}

// Many clients retrying at once with one key: exactly one runs the request.
func TestIdempotencyConcurrentClaims(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	var mu sync.Mutex
	owners := 0
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.BeginIdempotent("k", "f")
			if r == nil && err == nil {
				mu.Lock()
				owners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if owners != 1 {
		t.Fatalf("%d goroutines own the key, want 1", owners)
	}
}

func captured(id string, amount int64) payment.Payment {
	return payment.Payment{ID: id, Status: payment.Captured, Amount: amount, CapturedAmount: amount}
}
