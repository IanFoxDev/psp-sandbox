package stripe

import (
	"strconv"
	"strings"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// object is a Stripe object as JSON. Nullable fields Stripe always sends are
// set to nil rather than left out.
type object = map[string]any

func unix(t time.Time) int64 { return t.Unix() }

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func metadataOf(p payment.Payment) map[string]string {
	if p.Metadata == nil {
		return map[string]string{}
	}
	return p.Metadata
}

// intentStatus maps the domain status to the PaymentIntent status (ADR 0005).
func intentStatus(p payment.Payment) string {
	switch p.Status {
	case payment.Unconfirmed:
		if p.PaymentMethod == "" {
			return "requires_payment_method"
		}
		return "requires_confirmation"
	case payment.Pending:
		return "processing"
	case payment.Authorized:
		return "requires_capture"
	case payment.Failed:
		return "requires_payment_method"
	case payment.Canceled:
		return "canceled"
	}
	// captured, refunded, disputed and the chargeback outcomes.
	return "succeeded"
}

func captureMethod(p payment.Payment) string {
	if p.Capture == payment.CaptureManual {
		return "manual"
	}
	return "automatic"
}

// suffix is the random part of an id: "pi_ABC" -> "ABC".
func suffix(id string) string {
	_, s, _ := strings.Cut(id, "_")
	return s
}

// chargeID names the charge of one attempt: ch_ABC for the first attempt of
// pi_ABC, ch_ABC_2 for the second.
func chargeID(p payment.Payment, attempt int) string {
	id := "ch_" + suffix(p.ID)
	if attempt > 1 {
		id += "_" + strconv.Itoa(attempt)
	}
	return id
}

// parseChargeID returns the payment and attempt a charge id stands for.
func parseChargeID(id string) (paymentID string, attempt int, ok bool) {
	rest, ok := strings.CutPrefix(id, "ch_")
	if !ok || rest == "" {
		return "", 0, false
	}
	attempt = 1
	if base, n, found := strings.Cut(rest, "_"); found {
		var err error
		if attempt, err = strconv.Atoi(n); err != nil || attempt < 2 {
			return "", 0, false
		}
		rest = base
	}
	return "pi_" + rest, attempt, true
}

func latestCharge(p payment.Payment) string {
	if p.Attempt == 0 {
		return ""
	}
	return chargeID(p, p.Attempt)
}

// lastError is the last_payment_error of a failed attempt, also the body of
// the 402 answer.
func lastError(p payment.Payment, reason string) Error {
	d := declineFor(reason)
	return Error{Type: TypeCard, Code: d.code, DeclineCode: d.declineCode, Message: d.message,
		Charge: latestCharge(p)}
}

// renderIntent returns the PaymentIntent. With expandCharge, latest_charge
// is the charge object instead of its id.
func renderIntent(p payment.Payment, expandCharge bool) object {
	var lastErr any
	if p.Status == payment.Failed {
		lastErr = lastError(p, p.FailureReason)
	}
	latest := nullable(latestCharge(p))
	if expandCharge && p.Attempt > 0 {
		latest = renderCharge(p, p.Attempt)
	}
	var canceledAt any
	if p.Status == payment.Canceled {
		canceledAt = unix(p.UpdatedAt)
	}
	amountCapturable := int64(0)
	if p.Status == payment.Authorized {
		amountCapturable = p.Amount
	}
	return object{
		"id":                   p.ID,
		"object":               "payment_intent",
		"amount":               p.Amount,
		"amount_capturable":    amountCapturable,
		"amount_received":      p.CapturedAmount,
		"canceled_at":          canceledAt,
		"cancellation_reason":  nullable(p.CancellationReason),
		"capture_method":       captureMethod(p),
		"client_secret":        p.ID + "_secret_sandbox",
		"confirmation_method":  "automatic",
		"created":              unix(p.CreatedAt),
		"currency":             strings.ToLower(p.Currency),
		"customer":             nil,
		"description":          nullable(p.Description),
		"last_payment_error":   lastErr,
		"latest_charge":        latest,
		"livemode":             false,
		"metadata":             metadataOf(p),
		"next_action":          nil,
		"payment_method":       nullable(p.PaymentMethod),
		"payment_method_types": []string{"card"},
		"processing":           nil,
		"receipt_email":        nil,
		"setup_future_usage":   nil,
		"shipping":             nil,
		"status":               intentStatus(p),
	}
}

// renderCharge returns the charge of one attempt of the payment. Earlier
// attempts all failed; the current one follows the payment.
func renderCharge(p payment.Payment, attempt int) object {
	status, reason := "succeeded", ""
	captured, amountCaptured, amountRefunded := false, int64(0), p.RefundedAmount
	created := p.ConfirmedAt
	switch {
	case attempt < p.Attempt:
		status, reason, amountRefunded = "failed", p.PastFailures[attempt-1], 0
		created = p.CreatedAt
	case p.Status == payment.Pending:
		status = "pending"
	case p.Status == payment.Failed, p.Status == payment.Canceled && p.FailureReason != "":
		status, reason = "failed", p.FailureReason
	case p.Status == payment.Canceled:
		// An authorization that was let go: Stripe reports it as refunded.
		amountRefunded = p.Amount
	case p.Status == payment.Authorized:
	default:
		captured, amountCaptured = true, p.CapturedAmount
	}
	var failureCode, failureMessage any
	if status == "failed" {
		d := declineFor(reason)
		failureCode, failureMessage = d.code, d.message
	}
	disputed := p.Status == payment.Disputed || p.Status == payment.ChargebackLost || p.Status == payment.ChargebackWon
	// Fully refunded: everything captured came back, or a released
	// authorization.
	refunded := status == "succeeded" && amountRefunded > 0 &&
		(captured && amountRefunded == amountCaptured || !captured && amountRefunded == p.Amount)
	return object{
		"id":              chargeID(p, attempt),
		"object":          "charge",
		"amount":          p.Amount,
		"amount_captured": amountCaptured,
		"amount_refunded": amountRefunded,
		"billing_details": object{"address": nil, "email": nil, "name": nil, "phone": nil, "tax_id": nil},
		"captured":        captured,
		"created":         unix(created),
		"currency":        strings.ToLower(p.Currency),
		"description":     nullable(p.Description),
		"disputed":        disputed && attempt == p.Attempt,
		"failure_code":    failureCode,
		"failure_message": failureMessage,
		"livemode":        false,
		"metadata":        metadataOf(p),
		"paid":            status == "succeeded",
		"payment_intent":  p.ID,
		"payment_method":  nullable(p.PaymentMethod),
		"refunded":        refunded,
		"status":          status,
	}
}
