package stripe

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/httpx"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// Metadata keys the sandbox reads (ADR 0005).
const (
	MetaScenario    = "sandbox_scenario"
	MetaCallbackURL = "sandbox_callback_url"
	MetaReference   = "reference"
)

func (a *API) registerIntents(mux *http.ServeMux) {
	mux.Handle("POST /v1/payment_intents", a.chain(a.idempotent(http.HandlerFunc(a.createIntent))))
	mux.Handle("GET /v1/payment_intents", a.chain(http.HandlerFunc(a.listIntents)))
	mux.Handle("GET /v1/payment_intents/{intent}", a.chain(http.HandlerFunc(a.getIntent)))
	mux.Handle("POST /v1/payment_intents/{intent}", a.chain(a.idempotent(http.HandlerFunc(a.updateIntent))))
	mux.Handle("POST /v1/payment_intents/{intent}/confirm", a.chain(a.idempotent(http.HandlerFunc(a.confirmIntent))))
	mux.Handle("POST /v1/payment_intents/{intent}/capture", a.chain(a.idempotent(http.HandlerFunc(a.captureIntent))))
	mux.Handle("POST /v1/payment_intents/{intent}/cancel", a.chain(a.idempotent(http.HandlerFunc(a.cancelIntent))))
	mux.Handle("GET /v1/charges/{charge}", a.chain(http.HandlerFunc(a.getCharge)))
}

func noSuchIntent(w http.ResponseWriter, id string) {
	invalidRequest(w, http.StatusNotFound, "resource_missing", "intent", "No such payment_intent: '"+id+"'")
}

func unexpectedState(w http.ResponseWriter, p payment.Payment, action string, allowed ...string) {
	invalidRequest(w, http.StatusBadRequest, "payment_intent_unexpected_state", "",
		"You cannot "+action+" this PaymentIntent because it has a status of "+intentStatus(p)+". Only a "+
			"PaymentIntent with one of the following statuses may be "+action+"ed: "+strings.Join(allowed, ", ")+".")
}

// writeEngineError answers errors that the checks before the engine call did
// not catch, such as a payment that changed meanwhile.
func writeEngineError(w http.ResponseWriter, err error, id string) {
	var refused *engine.RefusedError
	switch {
	case errors.As(err, &refused):
		// Nothing was created, so a retry is safe; the SDKs read this header.
		w.Header().Set("Stripe-Should-Retry", "true")
		writeError(w, refused.Status, Error{Type: TypeAPI, Message: refused.Error()})
	case errors.Is(err, store.ErrNotFound):
		noSuchIntent(w, id)
	case errors.Is(err, engine.ErrInvalidScenario):
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "metadata["+MetaScenario+"]", err.Error())
	case errors.Is(err, payment.ErrInvalidAmount):
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_integer", "amount", err.Error())
	case errors.Is(err, payment.ErrInvalidState):
		invalidRequest(w, http.StatusBadRequest, "payment_intent_unexpected_state", "", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, Error{Type: TypeAPI, Message: err.Error()})
	}
}

// expandsCharge reads expand[] and reports whether latest_charge is in it.
// Other expansions are not modeled and are logged once.
func (a *API) expandsCharge(endpoint string, p *Params) (bool, error) {
	list, _, err := p.List("expand")
	if err != nil {
		return false, err
	}
	charge := false
	for _, v := range list {
		if v == "latest_charge" {
			charge = true
			continue
		}
		if _, seen := a.warned.LoadOrStore(endpoint+" expand "+v, true); !seen {
			a.opts.Log.Warn("Stripe expansion ignored: the sandbox does not model it", "endpoint", endpoint, "expand", v)
		}
	}
	return charge, nil
}

// metadataParams reads metadata and checks the keys the sandbox uses. A
// scenario in metadata is checked against the catalog now, not at confirm.
func (a *API) metadataParams(p *Params) (map[string]string, bool, error) {
	md, ok, err := p.Map("metadata")
	if err != nil || !ok {
		return md, ok, err
	}
	if v := md[MetaCallbackURL]; v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, true, &ParamError{Code: "url_invalid", Param: "metadata[" + MetaCallbackURL + "]",
				Message: "Invalid URL: " + v + ". Use an absolute http or https URL."}
		}
	}
	if v := md[MetaScenario]; v != "" {
		spec, err := scenario.ParseHeader(v)
		if err == nil {
			err = a.engine.CheckScenario(spec)
		}
		if err != nil {
			return nil, true, &ParamError{Code: "parameter_invalid_string", Param: "metadata[" + MetaScenario + "]",
				Message: err.Error()}
		}
	}
	return md, true, nil
}

// paymentMethodParam reads payment_method; an id Stripe would not know is 404.
func paymentMethodParam(w http.ResponseWriter, p *Params) (string, bool) {
	pm, ok, err := p.String("payment_method")
	if err != nil {
		writeParamError(w, err)
		return "", false
	}
	if !ok || pm == "" {
		return "", true
	}
	if _, known := cardScenario(pm); !known {
		invalidRequest(w, http.StatusNotFound, "resource_missing", "payment_method",
			"No such PaymentMethod: '"+pm+"'. The sandbox knows Stripe's test payment methods, such as pm_card_visa.")
		return "", false
	}
	return pm, true
}

func (a *API) createIntent(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/payment_intents"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	amount, hasAmount, err := p.Int("amount")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if !hasAmount {
		missing(w, "amount")
		return
	}
	if amount <= 0 {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_integer", "amount",
			"This value must be greater than or equal to 1.")
		return
	}
	currency, hasCurrency, err := p.String("currency")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if !hasCurrency {
		missing(w, "currency")
		return
	}
	if len(currency) != 3 || !payment.ValidCurrency(strings.ToUpper(currency)) {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "currency",
			"Invalid currency: "+currency+".")
		return
	}
	capture := payment.CaptureAuto
	switch cm, _, err := p.String("capture_method"); {
	case err != nil:
		writeParamError(w, err)
		return
	case cm == "manual":
		capture = payment.CaptureManual
	case cm == "", cm == "automatic", cm == "automatic_async":
	default:
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "capture_method",
			"Invalid capture_method: must be one of automatic, automatic_async, or manual")
		return
	}
	confirm, _, err := p.Bool("confirm")
	if err != nil {
		writeParamError(w, err)
		return
	}
	pm, ok := paymentMethodParam(w, p)
	if !ok {
		return
	}
	md, _, err := a.metadataParams(p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	description, _, err := p.String("description")
	if err != nil {
		writeParamError(w, err)
		return
	}
	expand, err := a.expandsCharge(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	req := engine.CreateRequest{
		Amount:        amount,
		Currency:      strings.ToUpper(currency),
		Reference:     md[MetaReference],
		Capture:       capture,
		CallbackURL:   md[MetaCallbackURL],
		Metadata:      md,
		PaymentMethod: pm,
		Description:   description,
	}
	if !confirm {
		pi, err := a.engine.Prepare(req)
		if err != nil {
			writeEngineError(w, err, "")
			return
		}
		respond(w, r, http.StatusOK, renderIntent(pi, expand), pi.ID)
		return
	}
	if pm == "" {
		noPaymentMethod(w)
		return
	}
	spec, err := pickScenario(r, md, pm)
	if err != nil {
		writeParamError(w, err)
		return
	}
	created, err := a.engine.PrepareAndConfirm(req, engine.ConfirmRequest{
		Scenario: spec, PaymentMethod: pm, RetryKey: retryKey(r, "create "+fingerprint(p.raw, r.Header.Get(scenario.Header))),
	})
	if err != nil {
		writeEngineError(w, err, "")
		return
	}
	a.answer(w, r, created, expand)
}

func noPaymentMethod(w http.ResponseWriter) {
	invalidRequest(w, http.StatusBadRequest, "payment_intent_unexpected_state", "payment_method",
		"You cannot confirm this PaymentIntent because it's missing a payment method. Pass payment_method, "+
			"for example pm_card_visa.")
}

// retryKey names the call for scenarios that refuse the first calls: the
// Idempotency-Key when there is one, so SDK retries count as one call.
func retryKey(r *http.Request, fallback string) string {
	if key := r.Header.Get(HeaderIdempotencyKey); key != "" {
		return "key " + key
	}
	return fallback
}

func (a *API) confirmIntent(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/payment_intents/{intent}/confirm"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	pm, ok := paymentMethodParam(w, p)
	if !ok {
		return
	}
	expand, err := a.expandsCharge(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	id := r.PathValue("intent")
	cur, err := a.engine.Payment(id)
	if err != nil {
		noSuchIntent(w, id)
		return
	}
	if cur.Status != payment.Unconfirmed && cur.Status != payment.Failed {
		unexpectedState(w, cur, "confirm", "requires_payment_method", "requires_confirmation")
		return
	}
	if pm == "" {
		pm = cur.PaymentMethod
	}
	if pm == "" {
		noPaymentMethod(w)
		return
	}
	spec, err := pickScenario(r, cur.Metadata, pm)
	if err != nil {
		writeParamError(w, err)
		return
	}
	created, err := a.engine.Confirm(id, engine.ConfirmRequest{
		Scenario: spec, PaymentMethod: pm, RetryKey: retryKey(r, "confirm "+id),
	})
	if err != nil {
		writeEngineError(w, err, id)
		return
	}
	a.answer(w, r, created, expand)
}

// answer waits for the outcome of a confirmed attempt and answers with it:
// the PaymentIntent, or 402 with the card error when the attempt failed.
func (a *API) answer(w http.ResponseWriter, r *http.Request, created engine.Created, expand bool) {
	id := created.Payment.ID
	if !a.opts.ManualClock {
		// With a manual clock nothing settles until the test moves time, so
		// the answer shows the attempt in progress.
		select {
		case <-created.Settled:
		case <-r.Context().Done():
			return
		}
	}
	p, err := a.engine.Payment(id)
	if err != nil {
		writeEngineError(w, err, id)
		return
	}
	status, v := http.StatusOK, any(renderIntent(p, expand))
	if p.Status == payment.Failed {
		e := lastError(p, p.FailureReason)
		e.PaymentIntent = v
		status, v = http.StatusPaymentRequired, errorBody{Error: e}
	}
	body, _ := json.Marshal(v)
	body = append(body, '\n')
	commitResponse(r, status, body, p.ID)
	if !a.hold(w, r, created) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// pickScenario applies the order of ADR 0005: header, then
// metadata[sandbox_scenario], then the test payment method. Nil leaves the
// choice to the rules file and the default.
func pickScenario(r *http.Request, metadata map[string]string, pm string) (*scenario.Spec, error) {
	if h := r.Header.Get(scenario.Header); h != "" {
		spec, err := scenario.ParseHeader(h)
		if err != nil {
			return nil, &ParamError{Code: "parameter_invalid_string", Message: scenario.Header + ": " + err.Error()}
		}
		return &spec, nil
	}
	if v := metadata[MetaScenario]; v != "" {
		spec, err := scenario.ParseHeader(v)
		if err != nil {
			return nil, &ParamError{Code: "parameter_invalid_string", Param: "metadata[" + MetaScenario + "]",
				Message: err.Error()}
		}
		return &spec, nil
	}
	spec, _ := cardScenario(pm)
	return spec, nil
}

// hold applies the scenario's response plan, as the native API does. It
// returns false if the response must not be written.
func (a *API) hold(w http.ResponseWriter, r *http.Request, c engine.Created) bool {
	plan := c.Response
	if plan.Reset {
		httpx.ResetConnection(w)
		return false
	}
	if plan.AfterFirstCallback {
		select {
		case <-c.FirstCallback:
		case <-r.Context().Done():
			return false
		}
	}
	if plan.Delay > 0 {
		t := time.NewTimer(plan.Delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.Context().Done():
			a.opts.Log.Info("client gave up waiting for the confirm response", "payment_intent", c.Payment.ID)
			return false
		}
	}
	return true
}

func (a *API) getIntent(w http.ResponseWriter, r *http.Request) {
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	expand, err := a.expandsCharge("GET /v1/payment_intents/{intent}", p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	id := r.PathValue("intent")
	pi, err := a.engine.Payment(id)
	if err != nil {
		noSuchIntent(w, id)
		return
	}
	writeJSON(w, http.StatusOK, renderIntent(pi, expand))
}

func (a *API) updateIntent(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/payment_intents/{intent}"
	p, ok := readParams(w, r)
	if !ok {
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
	pm, ok := paymentMethodParam(w, p)
	if !ok {
		return
	}
	md, hasMetadata, err := a.metadataParams(p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	description, hasDescription, err := p.String("description")
	if err != nil {
		writeParamError(w, err)
		return
	}
	expand, err := a.expandsCharge(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	id := r.PathValue("intent")
	errLocked := errors.New("locked")
	updated, err := a.engine.Update(id, func(p *payment.Payment) error {
		open := p.Status == payment.Unconfirmed || p.Status == payment.Failed
		if (hasAmount || pm != "") && !open {
			return errLocked
		}
		if hasAmount {
			p.Amount = amount
		}
		if pm != "" {
			p.PaymentMethod = pm
		}
		if hasDescription {
			p.Description = description
		}
		if hasMetadata {
			p.Metadata = mergeMetadata(p.Metadata, md)
			p.Reference = p.Metadata[MetaReference]
			if u := p.Metadata[MetaCallbackURL]; u != "" {
				p.CallbackURL = u
			}
		}
		return nil
	})
	if errors.Is(err, errLocked) {
		unexpectedState(w, updated, "update the amount or payment method of",
			"requires_payment_method", "requires_confirmation")
		return
	}
	if err != nil {
		writeEngineError(w, err, id)
		return
	}
	respond(w, r, http.StatusOK, renderIntent(updated, expand), updated.ID)
}

// mergeMetadata applies an update the way Stripe does: keys with an empty
// value are removed, and an empty update (metadata=) clears everything.
func mergeMetadata(cur, update map[string]string) map[string]string {
	if len(update) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range cur {
		out[k] = v
	}
	for k, v := range update {
		if v == "" {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

func (a *API) captureIntent(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/payment_intents/{intent}/capture"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	amount, hasAmount, err := p.Int("amount_to_capture")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if hasAmount && amount <= 0 {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_integer", "amount_to_capture",
			"This value must be greater than or equal to 1.")
		return
	}
	expand, err := a.expandsCharge(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	id := r.PathValue("intent")
	cur, err := a.engine.Payment(id)
	if err != nil {
		noSuchIntent(w, id)
		return
	}
	if cur.Status != payment.Authorized {
		unexpectedState(w, cur, "capture", "requires_capture")
		return
	}
	if amount > cur.Amount {
		invalidRequest(w, http.StatusBadRequest, "amount_too_large", "amount_to_capture",
			"The amount to capture ("+strconv.FormatInt(amount, 10)+") is greater than the amount capturable ("+
				strconv.FormatInt(cur.Amount, 10)+").")
		return
	}
	captured, err := a.engine.Capture(id, amount)
	if err != nil {
		writeEngineError(w, err, id)
		return
	}
	respond(w, r, http.StatusOK, renderIntent(captured, expand), captured.ID)
}

var cancellationReasons = []string{"duplicate", "fraudulent", "requested_by_customer", "abandoned"}

func (a *API) cancelIntent(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/payment_intents/{intent}/cancel"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	reason, _, err := p.String("cancellation_reason")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if reason != "" && !slices.Contains(cancellationReasons, reason) {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "cancellation_reason",
			"Invalid cancellation_reason: must be one of "+strings.Join(cancellationReasons, ", "))
		return
	}
	expand, err := a.expandsCharge(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	id := r.PathValue("intent")
	cur, err := a.engine.Payment(id)
	if err != nil {
		noSuchIntent(w, id)
		return
	}
	cancel := a.engine.Cancel
	switch cur.Status {
	case payment.Unconfirmed, payment.Failed:
		cancel = a.engine.Abandon
	case payment.Pending, payment.Authorized:
	default:
		unexpectedState(w, cur, "cancel", "requires_payment_method", "requires_capture",
			"requires_confirmation", "processing")
		return
	}
	if reason != "" {
		if _, err := a.engine.Update(id, func(p *payment.Payment) error {
			p.CancellationReason = reason
			return nil
		}); err != nil {
			writeEngineError(w, err, id)
			return
		}
	}
	canceled, err := cancel(id)
	if err != nil {
		writeEngineError(w, err, id)
		return
	}
	respond(w, r, http.StatusOK, renderIntent(canceled, expand), canceled.ID)
}

func (a *API) listIntents(w http.ResponseWriter, r *http.Request) {
	const endpoint = "GET /v1/payment_intents"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	limit, after, ok := pageParams(w, p)
	if !ok {
		return
	}
	expand, err := a.expandsCharge(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	all := a.engine.Payments("")
	slices.Reverse(all) // newest first
	page, hasMore, ok := paginate(w, all, after, limit, func(p payment.Payment) string { return p.ID }, "payment_intent")
	if !ok {
		return
	}
	data := make([]object, len(page))
	for i, pi := range page {
		data[i] = renderIntent(pi, expand)
	}
	writeJSON(w, http.StatusOK, object{
		"object":   "list",
		"data":     data,
		"has_more": hasMore,
		"url":      "/v1/payment_intents",
	})
}

func (a *API) getCharge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("charge")
	notFound := func() {
		invalidRequest(w, http.StatusNotFound, "resource_missing", "id", "No such charge: '"+id+"'")
	}
	paymentID, attempt, ok := parseChargeID(id)
	if !ok {
		notFound()
		return
	}
	p, err := a.engine.Payment(paymentID)
	if err != nil || attempt > p.Attempt {
		notFound()
		return
	}
	writeJSON(w, http.StatusOK, renderCharge(p, attempt))
}

func missing(w http.ResponseWriter, param string) {
	invalidRequest(w, http.StatusBadRequest, "parameter_missing", param, "Missing required param: "+param+".")
}
