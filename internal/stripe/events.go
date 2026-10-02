package stripe

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// APIVersion is the one Stripe API version the profile follows (ADR 0005).
const APIVersion = "2026-09-30.endive"

// eventPart is one Stripe event made from a domain event.
type eventPart struct {
	typ string
	obj object
}

// stripeEvents renders a domain event as the Stripe events it stands for, in
// the order of ADR 0005. The first keeps the id of the domain event, the next
// ones get _2, _3. Rendering is a pure function of the event, so the Events
// API and the webhooks agree on ids and bodies.
func stripeEvents(ev payment.Event) []object {
	p := ev.Snapshot
	intent := func() object { return renderIntent(p, false) }
	charge := func() object { return renderCharge(p, p.Attempt) }
	var parts []eventPart
	switch ev.Type {
	case payment.EventPaymentCreated:
		parts = []eventPart{{"payment_intent.created", intent()}}
	case payment.EventPaymentAuthorized:
		parts = []eventPart{{"charge.succeeded", charge()}, {"payment_intent.amount_capturable_updated", intent()}}
	case payment.EventPaymentCaptured:
		first := eventPart{"charge.succeeded", charge()}
		if p.Capture == payment.CaptureManual {
			first.typ = "charge.captured"
		}
		parts = []eventPart{first, {"payment_intent.succeeded", intent()}}
	case payment.EventPaymentFailed:
		parts = []eventPart{{"charge.failed", charge()}, {"payment_intent.payment_failed", intent()}}
	case payment.EventPaymentCanceled:
		parts = []eventPart{{"payment_intent.canceled", intent()}}
	case payment.EventRefundSucceeded:
		r, _ := ev.Data.(payment.Refund)
		parts = []eventPart{{"refund.created", renderRefund(r, p)}, {"charge.refunded", charge()}}
	case payment.EventRefundFailed:
		r, _ := ev.Data.(payment.Refund)
		parts = []eventPart{{"refund.updated", renderRefund(r, p)}, {"refund.failed", renderRefund(r, p)}}
	case payment.EventChargebackOpened:
		parts = []eventPart{{"charge.dispute.created", renderDispute(p)}}
	case payment.EventChargebackClosed:
		parts = []eventPart{{"charge.dispute.closed", renderDispute(p)}}
	}
	pending := 0
	if p.CallbackURL != "" {
		pending = 1
	}
	out := make([]object, len(parts))
	for i, part := range parts {
		out[i] = object{
			"id":               partID(ev.ID, i),
			"object":           "event",
			"api_version":      APIVersion,
			"created":          unix(ev.CreatedAt),
			"data":             object{"object": part.obj},
			"livemode":         false,
			"pending_webhooks": pending,
			"request":          object{"id": nil, "idempotency_key": nil},
			"type":             part.typ,
		}
	}
	return out
}

func partID(eventID string, i int) string {
	if i == 0 {
		return eventID
	}
	return eventID + "_" + strconv.Itoa(i+1)
}

// parseEventID splits evt_ABC_2 into the domain event evt_ABC and index 1.
func parseEventID(id string) (string, int) {
	rest, ok := strings.CutPrefix(id, "evt_")
	if !ok {
		return id, 0
	}
	base, n, found := strings.Cut(rest, "_")
	if !found {
		return id, 0
	}
	i, err := strconv.Atoi(n)
	if err != nil || i < 2 {
		return id, -1
	}
	return "evt_" + base, i - 1
}

// renderDispute returns the dispute of the payment's current charge.
func renderDispute(p payment.Payment) object {
	status := "needs_response"
	switch p.Status {
	case payment.ChargebackLost:
		status = "lost"
	case payment.ChargebackWon:
		status = "won"
	}
	return object{
		"id":                         "dp_" + suffix(p.ID),
		"object":                     "dispute",
		"amount":                     p.CapturedAmount - p.RefundedAmount,
		"balance_transactions":       []any{},
		"charge":                     chargeID(p, p.Attempt),
		"created":                    unix(p.DisputedAt),
		"currency":                   strings.ToLower(p.Currency),
		"enhanced_eligibility_types": []string{},
		"evidence":                   object{"enhanced_evidence": object{}},
		"evidence_details": object{
			"due_by":               unix(p.DisputedAt.Add(7 * 24 * time.Hour)),
			"enhanced_eligibility": object{},
			"has_evidence":         false,
			"past_due":             false,
			"submission_count":     0,
		},
		"is_charge_refundable": false,
		"livemode":             false,
		"metadata":             map[string]string{},
		"payment_intent":       p.ID,
		"reason":               "fraudulent",
		"status":               status,
	}
}

func (a *API) registerEvents(mux *http.ServeMux) {
	mux.Handle("GET /v1/events", a.chain(http.HandlerFunc(a.listEvents)))
	mux.Handle("GET /v1/events/{id}", a.chain(http.HandlerFunc(a.getEvent)))
}

func (a *API) getEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	domainID, i := parseEventID(id)
	ev, err := a.engine.Event(domainID)
	var parts []object
	if err == nil {
		parts = stripeEvents(ev)
	}
	if i < 0 || i >= len(parts) {
		invalidRequest(w, http.StatusNotFound, "resource_missing", "id", "No such event: '"+id+"'")
		return
	}
	writeJSON(w, http.StatusOK, parts[i])
}

// matchType reports whether typ matches a filter such as payment_intent.* .
func matchType(filter, typ string) bool {
	if prefix, ok := strings.CutSuffix(filter, "*"); ok {
		return strings.HasPrefix(typ, prefix)
	}
	return filter == typ
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	const endpoint = "GET /v1/events"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	limit, after, ok := pageParams(w, p)
	if !ok {
		return
	}
	typ, _, err := p.String("type")
	if err != nil {
		writeParamError(w, err)
		return
	}
	types, _, err := p.List("types")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if typ != "" {
		types = append(types, typ)
	}
	a.warnUnread(endpoint, p)

	var all []object
	for _, ev := range a.engine.AllEvents() {
		for _, e := range stripeEvents(ev) {
			if len(types) == 0 || slices.ContainsFunc(types, func(f string) bool { return matchType(f, e["type"].(string)) }) {
				all = append(all, e)
			}
		}
	}
	slices.Reverse(all) // newest first
	page, hasMore, ok := paginate(w, all, after, limit, func(e object) string { return e["id"].(string) }, "event")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, object{"object": "list", "data": page, "has_more": hasMore, "url": "/v1/events"})
}

// Webhooks encodes a domain event as the Stripe events sent for it, for the
// callback dispatcher. Bodies are the same as GET /v1/events/{id} returns.
func Webhooks(ev payment.Event) ([]callback.Message, error) {
	parts := stripeEvents(ev)
	out := make([]callback.Message, 0, len(parts))
	for _, e := range parts {
		body, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		out = append(out, callback.Message{ID: e["id"].(string), Type: e["type"].(string), Body: body})
	}
	return out, nil
}
