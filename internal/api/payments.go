package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/httpx"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

type createRequest struct {
	Amount      int64             `json:"amount"`
	Currency    string            `json:"currency"`
	Reference   string            `json:"reference"`
	Capture     string            `json:"capture"`
	CallbackURL string            `json:"callback_url"`
	Metadata    map[string]string `json:"metadata"`
}

func (a *API) createPayment(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !httpx.ReadJSON(w, r, &req, false) {
		return
	}
	in, msg := validateCreate(req)
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	if h := r.Header.Get(scenario.Header); h != "" {
		spec, err := scenario.ParseHeader(h)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		in.Scenario = &spec
	}

	in.RetryKey = retryKey(r, req)

	created, err := a.engine.Create(in)
	if refused := (*engine.RefusedError)(nil); errors.As(err, &refused) {
		httpx.WriteError(w, refused.Status, "server_error", refused.Error())
		return
	}
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	body, _ := json.Marshal(created.Payment)
	body = append(body, '\n')
	commitResponse(r, http.StatusCreated, body)

	if !a.hold(w, r, created) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}

// retryKey names the request for scenarios that refuse the first calls: the
// Idempotency-Key if there is one, otherwise the request itself, so a client
// that retries without a key still gets through.
func retryKey(r *http.Request, req createRequest) string {
	if key := r.Header.Get(HeaderIdempotencyKey); key != "" {
		return "key " + key
	}
	body, _ := json.Marshal(req)
	return "body " + fingerprint(body, []byte(r.Header.Get(scenario.Header)))
}

// hold applies the scenario's response plan. It returns false if the
// response must not be written.
func (a *API) hold(w http.ResponseWriter, r *http.Request, c engine.Created) bool {
	plan := c.Response
	if plan.Reset {
		resetConnection(w)
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
			a.opts.Log.Info("client gave up waiting for the create response", "payment", c.Payment.ID)
			return false
		}
	}
	return true
}

// resetConnection closes the client connection without writing a response.
func resetConnection(w http.ResponseWriter) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func validateCreate(req createRequest) (engine.CreateRequest, string) {
	in := engine.CreateRequest{
		Amount:      req.Amount,
		Currency:    req.Currency,
		Reference:   req.Reference,
		Capture:     payment.CaptureMode(req.Capture),
		CallbackURL: req.CallbackURL,
		Metadata:    req.Metadata,
	}
	if req.Amount <= 0 {
		return in, "amount must be a positive integer in minor units"
	}
	if !payment.ValidCurrency(req.Currency) {
		return in, "currency must be an uppercase code such as EUR"
	}
	switch in.Capture {
	case "":
		in.Capture = payment.CaptureAuto
	case payment.CaptureAuto, payment.CaptureManual:
	default:
		return in, "capture must be auto or manual"
	}
	if req.CallbackURL != "" {
		u, err := url.Parse(req.CallbackURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return in, "callback_url must be an absolute http or https URL"
		}
	}
	return in, ""
}

func (a *API) getPayment(w http.ResponseWriter, r *http.Request) {
	p, err := a.engine.Payment(r.PathValue("id"))
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (a *API) listPayments(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": a.engine.Payments(r.URL.Query().Get("reference"))})
}

func (a *API) capture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Amount is a pointer so that a missing amount (capture everything)
		// differs from an explicit 0, which is rejected.
		Amount *int64 `json:"amount"`
	}
	if !httpx.ReadJSON(w, r, &req, true) {
		return
	}
	var amount int64
	if req.Amount != nil {
		if *req.Amount <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "amount must be positive")
			return
		}
		amount = *req.Amount
	}
	p, err := a.engine.Capture(r.PathValue("id"), amount)
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	p, err := a.engine.Cancel(r.PathValue("id"))
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (a *API) refund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount    int64  `json:"amount"`
		Reference string `json:"reference"`
	}
	if !httpx.ReadJSON(w, r, &req, false) {
		return
	}
	ref, err := a.engine.Refund(r.PathValue("id"), req.Amount, req.Reference)
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	body, _ := json.Marshal(ref)
	body = append(body, '\n')
	commitResponse(r, http.StatusCreated, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}
