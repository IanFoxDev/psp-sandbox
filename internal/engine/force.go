package engine

import (
	"errors"
	"fmt"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// ErrUnsupportedEvent means the event type cannot be forced.
var ErrUnsupportedEvent = errors.New("unsupported event")

// ForceRequest asks for an event that the scenario did not plan.
type ForceRequest struct {
	Type payment.EventType
	// Reason is the decline reason for payment.failed. Default do_not_honor.
	Reason string
	// Outcome is lost or won for chargeback.closed. Default lost.
	Outcome string
}

// Force moves the payment to the status that the event stands for and sends
// the event through the scenario's delivery plan, as if the provider did it.
// The usual status rules apply: chargeback.opened needs a captured payment.
func (e *Engine) Force(paymentID string, req ForceRequest) (payment.Payment, payment.Event, error) {
	to, reason, err := forcedStatus(req)
	if err != nil {
		return payment.Payment{}, payment.Event{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ev, _, err := e.transitionLocked(paymentID, to, reason)
	if err != nil {
		return p, ev, err
	}
	e.log.Info("event forced", "payment", paymentID, "event", ev.ID, "type", ev.Type)
	return p, ev, nil
}

func forcedStatus(req ForceRequest) (payment.Status, string, error) {
	switch req.Type {
	case payment.EventPaymentAuthorized:
		return payment.Authorized, "", nil
	case payment.EventPaymentCaptured:
		return payment.Captured, "", nil
	case payment.EventPaymentFailed:
		reason := req.Reason
		if reason == "" {
			reason = "do_not_honor"
		}
		return payment.Failed, reason, nil
	case payment.EventPaymentCanceled:
		return payment.Canceled, "", nil
	case payment.EventChargebackOpened:
		return payment.Disputed, "", nil
	case payment.EventChargebackClosed:
		switch req.Outcome {
		case "", "lost":
			return payment.ChargebackLost, "", nil
		case "won":
			return payment.ChargebackWon, "", nil
		}
		return "", "", fmt.Errorf("%w: outcome must be lost or won, got %q", ErrUnsupportedEvent, req.Outcome)
	}
	return "", "", fmt.Errorf("%w: %q cannot be forced; supported: payment.authorized, payment.captured, "+
		"payment.failed, payment.canceled, chargeback.opened, chargeback.closed", ErrUnsupportedEvent, req.Type)
}
