package stripe

import (
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

func (a *API) registerCheckout(mux *http.ServeMux) {
	mux.Handle("POST /v1/checkout/sessions", a.chain(a.idempotent(http.HandlerFunc(a.createSession))))
	mux.Handle("GET /v1/checkout/sessions", a.chain(http.HandlerFunc(a.listSessions)))
	mux.Handle("GET /v1/checkout/sessions/{session}", a.chain(http.HandlerFunc(a.getSession)))
	mux.Handle("POST /v1/checkout/sessions/{session}/expire", a.chain(a.idempotent(http.HandlerFunc(a.expireSession))))
	mux.Handle("GET /v1/checkout/sessions/{session}/line_items", a.chain(http.HandlerFunc(a.sessionLineItems)))
	// The customer paying on the hosted page, for tests without a browser.
	mux.HandleFunc("POST /_sandbox/checkout/{session}/pay", a.payControl)
}

// CheckoutURL is the sandbox page that stands in for Stripe's hosted page.
func (a *API) CheckoutURL(id string) string {
	return a.opts.PublicURL + "/_sandbox/ui/checkout/" + url.PathEscape(id)
}

func noSuchSession(w http.ResponseWriter, id string) {
	invalidRequest(w, http.StatusNotFound, "resource_missing", "session", "No such checkout.session: '"+id+"'")
}

func absolute(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// lineItems reads line_items[][price_data][...] and line_items[][quantity].
func lineItems(p *Params) ([]payment.LineItem, string, error) {
	items, ok, err := p.Items("line_items")
	if err != nil {
		return nil, "", err
	}
	if !ok || len(items) == 0 {
		return nil, "", &ParamError{Code: "parameter_missing", Param: "line_items", Message: "Missing required param: line_items."}
	}
	var out []payment.LineItem
	currency := ""
	for i, it := range items {
		at := "line_items[" + strconv.Itoa(i) + "]"
		if it.Has("price") {
			return nil, "", &ParamError{Code: "parameter_unknown", Param: at + "[price]",
				Message: "Prices are not modeled by the sandbox. Use " + at + "[price_data] instead."}
		}
		pd, ok, err := it.Sub("price_data")
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", &ParamError{Code: "parameter_missing", Param: at + "[price_data]",
				Message: "Missing required param: " + at + "[price_data]."}
		}
		cur, _, err := pd.String("currency")
		if err != nil || len(cur) != 3 || !payment.ValidCurrency(strings.ToUpper(cur)) {
			return nil, "", &ParamError{Code: "parameter_invalid_string", Param: at + "[price_data][currency]",
				Message: "Invalid currency: " + cur + "."}
		}
		if currency == "" {
			currency = strings.ToUpper(cur)
		} else if currency != strings.ToUpper(cur) {
			return nil, "", &ParamError{Param: at + "[price_data][currency]",
				Message: "All line items must use the same currency."}
		}
		unit, ok, err := pd.Int("unit_amount")
		if err != nil {
			return nil, "", err
		}
		if !ok || unit < 0 {
			return nil, "", &ParamError{Code: "parameter_missing", Param: at + "[price_data][unit_amount]",
				Message: "Missing required param: " + at + "[price_data][unit_amount]."}
		}
		prod, _, err := pd.Sub("product_data")
		if err != nil {
			return nil, "", err
		}
		name := ""
		if prod != nil {
			name, _, _ = prod.String("name")
		}
		if name == "" {
			return nil, "", &ParamError{Code: "parameter_missing", Param: at + "[price_data][product_data][name]",
				Message: "Missing required param: " + at + "[price_data][product_data][name]."}
		}
		qty, ok, err := it.Int("quantity")
		if err != nil {
			return nil, "", err
		}
		if !ok || qty < 1 {
			return nil, "", &ParamError{Code: "parameter_missing", Param: at + "[quantity]",
				Message: "Missing required param: " + at + "[quantity]."}
		}
		out = append(out, payment.LineItem{Name: name, UnitAmount: unit, Quantity: qty})
	}
	return out, currency, nil
}

func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	const endpoint = "POST /v1/checkout/sessions"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	mode, _, err := p.String("mode")
	if err != nil {
		writeParamError(w, err)
		return
	}
	if mode != "payment" {
		invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "mode",
			"The sandbox supports Checkout Sessions in payment mode only; got mode="+strconv.Quote(mode)+".")
		return
	}
	items, currency, err := lineItems(p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	urls := map[string]string{}
	for _, name := range []string{"success_url", "cancel_url"} {
		v, _, err := p.String(name)
		if err != nil {
			writeParamError(w, err)
			return
		}
		if v != "" && !absolute(v) {
			invalidRequest(w, http.StatusBadRequest, "url_invalid", name, "Not a valid URL: "+v)
			return
		}
		urls[name] = v
	}
	if urls["success_url"] == "" {
		missing(w, "success_url")
		return
	}
	clientRef, _, err := p.String("client_reference_id")
	if err != nil {
		writeParamError(w, err)
		return
	}
	email, _, err := p.String("customer_email")
	if err != nil {
		writeParamError(w, err)
		return
	}
	md, _, err := a.metadataParams(p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	var piMetadata map[string]string
	if pid, ok, err := p.Sub("payment_intent_data"); err != nil {
		writeParamError(w, err)
		return
	} else if ok {
		if piMetadata, _, err = a.metadataParams(pid); err != nil {
			writeParamError(w, err)
			return
		}
		if cm, _, _ := pid.String("capture_method"); cm == "manual" {
			invalidRequest(w, http.StatusBadRequest, "parameter_invalid_string", "payment_intent_data[capture_method]",
				"The sandbox does not model Checkout Sessions with manual capture.")
			return
		}
	}
	now := a.engine.Now()
	var expiresAt time.Time
	if v, ok, err := p.Int("expires_at"); err != nil {
		writeParamError(w, err)
		return
	} else if ok {
		expiresAt = time.Unix(v, 0).UTC()
		if expiresAt.Before(now.Add(30*time.Minute)) || expiresAt.After(now.Add(engine.DefaultSessionLifetime)) {
			invalidRequest(w, http.StatusBadRequest, "parameter_invalid_integer", "expires_at",
				"The expires_at timestamp must be between 30 minutes and 24 hours after Checkout Session creation.")
			return
		}
	}
	expandPI, expandItems, err := a.sessionExpand(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	reference := piMetadata[MetaReference]
	if reference == "" {
		reference = clientRef
	}
	callbackURL := piMetadata[MetaCallbackURL]
	if callbackURL == "" {
		callbackURL = md[MetaCallbackURL]
	}
	cs, err := a.engine.CreateSession(engine.SessionRequest{
		Currency:          currency,
		LineItems:         items,
		SuccessURL:        urls["success_url"],
		CancelURL:         urls["cancel_url"],
		ClientReferenceID: clientRef,
		CustomerEmail:     email,
		Metadata:          md,
		PaymentMetadata:   piMetadata,
		ExpiresAt:         expiresAt,
		Reference:         reference,
		CallbackURL:       callbackURL,
	})
	if err != nil {
		writeEngineError(w, err, "")
		return
	}
	respond(w, r, http.StatusOK, a.renderSession(cs, expandPI, expandItems), "")
}

// sessionExpand reads expand[]: payment_intent and line_items.
func (a *API) sessionExpand(endpoint string, p *Params) (bool, bool, error) {
	list, _, err := p.List("expand")
	if err != nil {
		return false, false, err
	}
	pi, items := false, false
	for _, v := range list {
		switch v {
		case "payment_intent":
			pi = true
		case "line_items":
			items = true
		default:
			if _, seen := a.warned.LoadOrStore(endpoint+" expand "+v, true); !seen {
				a.opts.Log.Warn("Stripe expansion ignored: the sandbox does not model it", "endpoint", endpoint, "expand", v)
			}
		}
	}
	return pi, items, nil
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	expandPI, expandItems, err := a.sessionExpand("GET /v1/checkout/sessions/{session}", p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	id := r.PathValue("session")
	cs, err := a.engine.Session(id)
	if err != nil {
		noSuchSession(w, id)
		return
	}
	writeJSON(w, http.StatusOK, a.renderSession(cs, expandPI, expandItems))
}

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	const endpoint = "GET /v1/checkout/sessions"
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	limit, after, ok := pageParams(w, p)
	if !ok {
		return
	}
	pi, _, err := p.String("payment_intent")
	if err != nil {
		writeParamError(w, err)
		return
	}
	expandPI, expandItems, err := a.sessionExpand(endpoint, p)
	if err != nil {
		writeParamError(w, err)
		return
	}
	a.warnUnread(endpoint, p)

	all := a.engine.Sessions()
	if pi != "" {
		all = slices.DeleteFunc(all, func(cs payment.Session) bool { return cs.PaymentID != pi })
	}
	slices.Reverse(all)
	page, hasMore, ok := paginate(w, all, after, limit, func(cs payment.Session) string { return cs.ID }, "checkout.session")
	if !ok {
		return
	}
	data := make([]object, len(page))
	for i, cs := range page {
		data[i] = a.renderSession(cs, expandPI, expandItems)
	}
	writeJSON(w, http.StatusOK, object{"object": "list", "data": data, "has_more": hasMore, "url": "/v1/checkout/sessions"})
}

func (a *API) expireSession(w http.ResponseWriter, r *http.Request) {
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	a.warnUnread("POST /v1/checkout/sessions/{session}/expire", p)
	id := r.PathValue("session")
	cs, err := a.engine.ExpireSession(id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		noSuchSession(w, id)
		return
	case errors.Is(err, payment.ErrInvalidState):
		invalidRequest(w, http.StatusBadRequest, "", "",
			"Only Checkout Sessions with a status of open can be expired. This one is "+string(cs.Status)+".")
		return
	case err != nil:
		writeEngineError(w, err, id)
		return
	}
	respond(w, r, http.StatusOK, a.renderSession(cs, false, false), "")
}

func (a *API) sessionLineItems(w http.ResponseWriter, r *http.Request) {
	p, ok := readParams(w, r)
	if !ok {
		return
	}
	limit, after, ok := pageParams(w, p)
	if !ok {
		return
	}
	id := r.PathValue("session")
	cs, err := a.engine.Session(id)
	if err != nil {
		noSuchSession(w, id)
		return
	}
	page, hasMore, ok := paginate(w, cs.LineItems, after, limit, func(l payment.LineItem) string { return l.ID }, "line_item")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, lineItemList(cs, page, hasMore))
}

func lineItemList(cs payment.Session, items []payment.LineItem, hasMore bool) object {
	data := make([]object, len(items))
	for i, l := range items {
		data[i] = object{
			"id":              l.ID,
			"object":          "item",
			"amount_discount": 0,
			"amount_subtotal": l.Total(),
			"amount_tax":      0,
			"amount_total":    l.Total(),
			"currency":        strings.ToLower(cs.Currency),
			"description":     l.Name,
			"price":           nil,
			"quantity":        l.Quantity,
		}
	}
	return object{"object": "list", "data": data, "has_more": hasMore,
		"url": "/v1/checkout/sessions/" + cs.ID + "/line_items"}
}

// renderSession returns the Checkout Session. With expandPI the
// payment_intent is the object; with expandItems line_items is included.
func (a *API) renderSession(cs payment.Session, expandPI, expandItems bool) object {
	pi := nullable(cs.PaymentID)
	if expandPI && cs.PaymentID != "" {
		if p, err := a.engine.Payment(cs.PaymentID); err == nil {
			pi = renderIntent(p, false)
		}
	}
	return renderSessionWith(cs, pi, expandItems, a.CheckoutURL(cs.ID))
}

func renderSessionWith(cs payment.Session, pi any, expandItems bool, checkoutURL string) object {
	paymentStatus := "unpaid"
	var customer, pageURL any
	switch cs.Status {
	case payment.SessionComplete:
		paymentStatus = "paid"
		customer = object{"address": nil, "email": nullable(cs.CustomerEmail), "name": nil, "phone": nil,
			"tax_exempt": "none", "tax_ids": []any{}}
	case payment.SessionOpen:
		pageURL = checkoutURL
	}
	o := object{
		"id":                   cs.ID,
		"object":               "checkout.session",
		"amount_subtotal":      cs.AmountTotal(),
		"amount_total":         cs.AmountTotal(),
		"automatic_tax":        object{"enabled": false, "liability": nil, "provider": nil, "status": nil},
		"cancel_url":           nullable(cs.CancelURL),
		"client_reference_id":  nullable(cs.ClientReferenceID),
		"created":              unix(cs.CreatedAt),
		"currency":             strings.ToLower(cs.Currency),
		"custom_fields":        []any{},
		"custom_text":          object{"after_submit": nil, "shipping_address": nil, "submit": nil, "terms_of_service_acceptance": nil},
		"customer_details":     customer,
		"customer_email":       nullable(cs.CustomerEmail),
		"expires_at":           unix(cs.ExpiresAt),
		"livemode":             false,
		"metadata":             orEmpty(cs.Metadata),
		"mode":                 "payment",
		"payment_intent":       pi,
		"payment_method_types": []string{"card"},
		"payment_status":       paymentStatus,
		"shipping_options":     []any{},
		"status":               string(cs.Status),
		"success_url":          nullable(cs.SuccessURL),
		"url":                  pageURL,
	}
	if expandItems {
		o["line_items"] = lineItemList(cs, cs.LineItems, false)
	}
	return o
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// PayCheckout is the customer paying a session with a test card. header is
// an X-Sandbox-Scenario value, or empty. It returns the payment once the
// first status step of the attempt has applied.
func (a *API) PayCheckout(id, pm, header, returnURL string) (payment.Payment, error) {
	cs, err := a.engine.Session(id)
	if err != nil {
		return payment.Payment{}, err
	}
	if _, known := cardScenario(pm); !known {
		return payment.Payment{}, &ParamError{Code: "resource_missing", Param: "payment_method",
			Message: "No such PaymentMethod: '" + pm + "'. The sandbox knows Stripe's test payment methods, such as pm_card_visa."}
	}
	md := map[string]string{}
	for k, v := range cs.Metadata {
		md[k] = v
	}
	for k, v := range cs.PaymentMetadata {
		md[k] = v
	}
	spec, err := pickScenarioFrom(header, md, pm)
	if err != nil {
		return payment.Payment{}, err
	}
	created, err := a.engine.PaySession(id, engine.ConfirmRequest{
		Scenario: spec, PaymentMethod: pm, ReturnURL: returnURL, RetryKey: "checkout " + id,
	})
	if err != nil {
		return payment.Payment{}, err
	}
	if !a.opts.ManualClock {
		select {
		case <-created.Settled:
		case <-time.After(10 * time.Second):
		}
	}
	return a.engine.Payment(created.Payment.ID)
}

// PayFromPage is PayCheckout for the hosted page: after 3DS the customer
// comes back to the page, which sends them on to success_url.
func (a *API) PayFromPage(id, pm string) (payment.Payment, error) {
	return a.PayCheckout(id, pm, "", a.CheckoutURL(id))
}

func (a *API) payControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentMethod string `json:"payment_method"`
	}
	if !httpx.ReadJSON(w, r, &req, false) {
		return
	}
	if req.PaymentMethod == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "payment_method is required, for example pm_card_visa")
		return
	}
	id := r.PathValue("session")
	_, err := a.PayCheckout(id, req.PaymentMethod, r.Header.Get(scenario.Header), "")
	var pe *ParamError
	var refused *engine.RefusedError
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "checkout session not found")
		return
	case errors.As(err, &pe):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", pe.Message)
		return
	case errors.As(err, &refused):
		httpx.WriteError(w, refused.Status, "server_error", refused.Error())
		return
	case err != nil:
		httpx.WriteDomainError(w, err)
		return
	}
	cs, err := a.engine.Session(id)
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a.renderSession(cs, true, false))
}
