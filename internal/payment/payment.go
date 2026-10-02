// Package payment holds the domain model: Payment, Refund, their statuses and
// the allowed transitions between them. It knows nothing about HTTP or scenarios.
package payment

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"
)

// Status of a payment. See docs/api.md for the diagram.
type Status string

// Payment statuses.
const (
	// Unconfirmed is a payment that exists but has not been sent for
	// processing. The native API never produces it: there, create confirms.
	Unconfirmed       Status = "unconfirmed"
	Pending           Status = "pending"
	Authorized        Status = "authorized"
	Captured          Status = "captured"
	PartiallyRefunded Status = "partially_refunded"
	Refunded          Status = "refunded"
	Failed            Status = "failed"
	Canceled          Status = "canceled"
	Disputed          Status = "disputed"
	ChargebackLost    Status = "chargeback_lost"
	ChargebackWon     Status = "chargeback_won"
)

// transitions lists, for each status, the statuses it may move to.
// A status that is not a key here is final.
var transitions = map[Status][]Status{
	Unconfirmed:       {Pending, Canceled},
	Pending:           {Authorized, Captured, Failed, Canceled},
	Authorized:        {Captured, Canceled},
	Captured:          {PartiallyRefunded, Refunded, Disputed},
	PartiallyRefunded: {PartiallyRefunded, Refunded, Disputed},
	Disputed:          {ChargebackLost, ChargebackWon},
}

// CanBecome reports whether a payment in status s may move to status to.
func (s Status) CanBecome(to Status) bool {
	for _, next := range transitions[s] {
		if next == to {
			return true
		}
	}
	return false
}

// Final reports whether no transition leaves s.
func (s Status) Final() bool {
	return len(transitions[s]) == 0
}

// CaptureMode says whether a payment is captured right after authorization.
type CaptureMode string

// Capture modes.
const (
	CaptureAuto   CaptureMode = "auto"
	CaptureManual CaptureMode = "manual"
)

var (
	// ErrInvalidState means the operation is not allowed in the current status.
	ErrInvalidState = errors.New("invalid state")
	// ErrInvalidAmount means the amount is not positive or larger than allowed.
	ErrInvalidAmount = errors.New("invalid amount")
	// ErrAmountExceedsCaptured means a refund is larger than what is left to refund.
	ErrAmountExceedsCaptured = errors.New("amount exceeds captured")
)

// Payment is a card payment as the provider sees it. Amounts are in minor units.
type Payment struct {
	ID             string            `json:"id"`
	Status         Status            `json:"status"`
	Amount         int64             `json:"amount"`
	CapturedAmount int64             `json:"captured_amount"`
	RefundedAmount int64             `json:"refunded_amount"`
	Currency       string            `json:"currency"`
	Reference      string            `json:"reference,omitempty"`
	Capture        CaptureMode       `json:"capture"`
	Scenario       string            `json:"scenario"`
	FailureReason  string            `json:"failure_reason,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Metadata       map[string]string `json:"metadata,omitempty"`

	// CallbackURL is where events for this payment are delivered.
	CallbackURL string `json:"-"`
	// RefundPending is the sum of refunds that are accepted but not settled yet.
	RefundPending int64 `json:"-"`
	// PaymentMethod is what the payer used, when the API takes one.
	PaymentMethod string `json:"-"`
	// Attempt counts confirmations: 0 while unconfirmed, then 1, and one more
	// for each retry after a failure.
	Attempt int `json:"-"`
	// ConfirmedAt is when the current attempt started.
	ConfirmedAt time.Time `json:"-"`
	// PastFailures holds the decline reasons of earlier attempts, oldest first.
	PastFailures []string `json:"-"`
	// DisputedAt is when a chargeback was opened.
	DisputedAt time.Time `json:"-"`
	// Description and CancellationReason are kept for APIs that have them.
	Description        string `json:"-"`
	CancellationReason string `json:"-"`
}

// Clone returns a copy that shares no memory with p.
func (p Payment) Clone() Payment {
	p.Metadata = maps.Clone(p.Metadata)
	p.PastFailures = slices.Clone(p.PastFailures)
	return p
}

// Become moves the payment to status to, or returns ErrInvalidState.
func (p *Payment) Become(to Status, at time.Time) error {
	if !p.Status.CanBecome(to) {
		return fmt.Errorf("%w: payment is %s and cannot become %s", ErrInvalidState, p.Status, to)
	}
	p.Status = to
	p.UpdatedAt = at
	return nil
}

// Confirm sends an unconfirmed payment for processing, or starts a new attempt
// of a failed one. Failed stays final for scenarios and for the native API:
// only an explicit confirmation, with a new payment method, reopens it.
func (p *Payment) Confirm(at time.Time) error {
	switch p.Status {
	case Unconfirmed:
		if err := p.Become(Pending, at); err != nil {
			return err
		}
	case Failed:
		p.Status = Pending
		p.UpdatedAt = at
		p.PastFailures = append(p.PastFailures, p.FailureReason)
		p.FailureReason = ""
	default:
		return fmt.Errorf("%w: payment is %s, only unconfirmed or failed payments can be confirmed", ErrInvalidState, p.Status)
	}
	p.Attempt++
	p.ConfirmedAt = at
	return nil
}

// Abandon cancels a payment that is unconfirmed or failed, which an API with
// retries after failure allows. The native API never calls it: there a failed
// payment stays failed.
func (p *Payment) Abandon(at time.Time) error {
	switch p.Status {
	case Unconfirmed:
		return p.Become(Canceled, at)
	case Failed:
		p.Status = Canceled
		p.UpdatedAt = at
		return nil
	}
	return fmt.Errorf("%w: payment is %s, only unconfirmed or failed payments can be abandoned", ErrInvalidState, p.Status)
}

// Authorize moves a pending payment to authorized.
func (p *Payment) Authorize(at time.Time) error {
	return p.Become(Authorized, at)
}

// Fail moves a pending payment to failed with a decline reason.
func (p *Payment) Fail(reason string, at time.Time) error {
	if err := p.Become(Failed, at); err != nil {
		return err
	}
	p.FailureReason = reason
	return nil
}

// CaptureAmount captures amount (the full amount if zero) of a pending or
// authorized payment.
func (p *Payment) CaptureAmount(amount int64, at time.Time) error {
	if amount == 0 {
		amount = p.Amount
	}
	if amount < 0 || amount > p.Amount {
		return fmt.Errorf("%w: capture amount must be between 1 and %d", ErrInvalidAmount, p.Amount)
	}
	if err := p.Become(Captured, at); err != nil {
		return err
	}
	p.CapturedAmount = amount
	return nil
}

// Cancel voids a pending or authorized payment.
func (p *Payment) Cancel(at time.Time) error {
	return p.Become(Canceled, at)
}

// Refundable is the amount that can still be refunded.
func (p *Payment) Refundable() int64 {
	return p.CapturedAmount - p.RefundedAmount - p.RefundPending
}

// ReserveRefund accepts a refund of amount and holds it until it settles.
func (p *Payment) ReserveRefund(amount int64) error {
	if p.Status != Captured && p.Status != PartiallyRefunded {
		return fmt.Errorf("%w: payment is %s, refunds need captured or partially_refunded", ErrInvalidState, p.Status)
	}
	if amount <= 0 {
		return fmt.Errorf("%w: refund amount must be positive", ErrInvalidAmount)
	}
	if amount > p.Refundable() {
		return fmt.Errorf("%w: %d requested, %d left to refund", ErrAmountExceedsCaptured, amount, p.Refundable())
	}
	p.RefundPending += amount
	return nil
}

// SettleRefund books a reserved refund and moves the payment to
// partially_refunded or refunded.
func (p *Payment) SettleRefund(amount int64, at time.Time) error {
	to := PartiallyRefunded
	if p.RefundedAmount+amount == p.CapturedAmount {
		to = Refunded
	}
	if err := p.Become(to, at); err != nil {
		return err
	}
	p.RefundPending -= amount
	p.RefundedAmount += amount
	return nil
}

// ReleaseRefund drops a reserved refund that failed.
func (p *Payment) ReleaseRefund(amount int64) {
	p.RefundPending -= amount
}

var currencyCode = regexp.MustCompile(`^[A-Z][A-Z0-9]{2,4}$`)

// ValidCurrency reports whether s is a currency code the API accepts: 3 to 5
// uppercase letters or digits, starting with a letter (EUR, JPY, USDT).
func ValidCurrency(s string) bool {
	return currencyCode.MatchString(s)
}
