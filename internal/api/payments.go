package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/ghuser/psp-sandbox/internal/engine"
	"github.com/ghuser/psp-sandbox/internal/payment"
	"github.com/ghuser/psp-sandbox/internal/scenario"
)

var currencyCode = regexp.MustCompile(`^[A-Z][A-Z0-9]{2,4}$`)

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
	if !readJSON(w, r, &req, false) {
		return
	}
	in, msg := validateCreate(req)
	if msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	if h := r.Header.Get(scenario.Header); h != "" {
		spec, err := scenario.ParseHeader(h)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		in.Scenario = &spec
	}

	created, err := a.engine.Create(in)
	if err != nil {
		writeDomainError(w, err)
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
	if !currencyCode.MatchString(req.Currency) {
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
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) listPayments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": a.engine.Payments(r.URL.Query().Get("reference"))})
}

func (a *API) capture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount int64 `json:"amount"`
	}
	if !readJSON(w, r, &req, true) {
		return
	}
	if req.Amount < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "amount must be positive")
		return
	}
	p, err := a.engine.Capture(r.PathValue("id"), req.Amount)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	p, err := a.engine.Cancel(r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) refund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount    int64  `json:"amount"`
		Reference string `json:"reference"`
	}
	if !readJSON(w, r, &req, false) {
		return
	}
	ref, err := a.engine.Refund(r.PathValue("id"), req.Amount, req.Reference)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	body, _ := json.Marshal(ref)
	body = append(body, '\n')
	commitResponse(r, http.StatusCreated, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}
