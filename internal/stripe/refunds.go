package stripe

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

var refundReasons = []string{"duplicate", "fraudulent", "requested_by_customer"}

// renderRefund returns the refund; p is its payment, for the charge id.
func renderRefund(r payment.Refund, p payment.Payment) object {
	md := r.Metadata
	if md == nil {
		md = map[string]string{}
	}
	out := object{
		"id":                  r.ID,
		"object":              "refund",
		"amount":              r.Amount,
		"balance_transaction": nil,
		"charge":              chargeID(p, p.Attempt),
		"created":             unix(r.CreatedAt),
		"currency":            strings.ToLower(r.Currency),
		"metadata":            md,
		"payment_intent":      p.ID,
		"reason":              nullable(r.Reason),
		"receipt_number":      nil,
		"status":              string(r.Status),
	}
	// Stripe sends failure_reason only on a failed refund; it is not nullable.
	if r.Status == payment.RefundFailed {
		out["failure_reason"] = "unknown"
		if strings.Contains(r.FailureReason, "disputed") || strings.Contains(r.FailureReason, "chargeback") {
			out["failure_reason"] = "charge_for_pending_refund_disputed"
		}
	}
	return out
}

func (a *API) registerRefunds(mux *http.ServeMux) {
	mux.Handle("POST /v1/refunds", a.chain(a.idempotent(http.HandlerFunc(a.createRefund))))
	mux.Handle("GET /v1/refunds/{refund}", a.chain(http.HandlerFunc(a.getRefund)))
}

func (a *API) createRefund(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/refunds"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	intentID, hasIntent, err := p.String("payment_intent")
	if err != nil {
		writeParamError(w, err)
		return
	}
	chargeParam, hasCharge, err := p.String("charge")
	if err != nil {
		writeParamError(w, err)
		return
	}
	amount, hasAmount, err := p.Int("amount")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if hasAmount && amount <= 0 {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_integer", "amount",
			"This value must be greater than or equal to 1.")
		return
	}
	reason, _, err := p.String("reason")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if reason != "" && !slices.Contains(refundReasons, reason) {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "reason",
			"Invalid reason: must be one of "+strings.Join(refundReasons, ", "))
		return
	}
	md, _, err := p.Map("metadata")
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	var pi payment.Payment
	switch {
	case hasIntent == hasCharge:
		invalidRequest(w, http.StatusBadRequest, "parameter_missing", "payment_intent",
			"Pass exactly one of payment_intent or charge.")
		return
	case hasIntent:
		if pi, err = a.engine.Payment(intentID); err != nil {
			noSuchIntent(w, intentID)
			return
		}
	default:
		paymentID, attempt, ok := parseChargeID(chargeParam)
		if ok {
			pi, err = a.engine.Payment(paymentID)
		}
		if !ok || err != nil || attempt > pi.Attempt {
			invalidRequest(w, http.StatusNotFound, "resource_missing", "charge", "No such charge: '"+chargeParam+"'")
			return
		}
		if attempt < pi.Attempt {
			invalidRequest(w, http.StatusBadRequest, "charge_not_refundable", "charge",
				"Charge "+chargeParam+" failed and has nothing to refund.")
			return
		}
	}

	charge := chargeID(pi, pi.Attempt)
	switch pi.Status {
	case payment.Captured, payment.PartiallyRefunded:
	case payment.Refunded:
		invalidRequest(w, http.StatusBadRequest, "charge_already_refunded", "",
			"Charge "+charge+" has already been refunded.")
		return
	case payment.Disputed, payment.ChargebackLost, payment.ChargebackWon:
		invalidRequest(w, http.StatusBadRequest, "charge_disputed", "",
			"Charge "+charge+" has been charged back; refunds are not possible.")
		return
	default:
		invalidRequest(w, http.StatusBadRequest, "charge_not_refundable", "",
			"This PaymentIntent has a status of "+intentStatus(pi)+" and has no captured charge to refund.")
		return
	}
	if !hasAmount {
		amount = pi.Refundable()
	}
	if amount > pi.Refundable() {
		invalidRequest(w, http.StatusBadRequest, "amount_too_large", "amount",
			"Refund amount ("+strconv.FormatInt(amount, 10)+") is greater than unrefunded amount on charge ("+
				strconv.FormatInt(pi.Refundable(), 10)+").")
		return
	}

	ref, settled, err := a.engine.StartRefund(pi.ID, engine.RefundRequest{Amount: amount, Reason: reason, Metadata: md})
	if err != nil {
		writeEngineError(w, err, pi.ID)
		return
	}
	if !a.opts.ManualClock {
		select {
		case <-settled:
		case <-r.Context().Done():
			return
		}
		if ref, err = a.engine.RefundByID(ref.ID); err != nil {
			writeEngineError(w, err, pi.ID)
			return
		}
	}
	cur, err := a.engine.Payment(pi.ID)
	if err != nil {
		writeEngineError(w, err, pi.ID)
		return
	}
	respond(w, r, http.StatusOK, renderRefund(ref, cur), pi.ID)
}

func (a *API) getRefund(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("refund")
	ref, err := a.engine.RefundByID(id)
	var pi payment.Payment
	if err == nil {
		pi, err = a.engine.Payment(ref.PaymentID)
	}
	if err != nil {
		invalidRequest(w, http.StatusNotFound, "resource_missing", "id", "No such refund: '"+id+"'")
		return
	}
	writeJSON(w, http.StatusOK, renderRefund(ref, pi))
}
