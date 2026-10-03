// Package control implements the control API under /_sandbox used by tests:
// scenario catalog, delivery log, replay, forced events, clock and reset.
//
// It has no authentication. The sandbox belongs in a test network.
package control

import (
	"net/http"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/httpx"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

// Control serves /_sandbox.
type Control struct {
	engine     *engine.Engine
	dispatcher *callback.Dispatcher
	catalog    *scenario.Catalog
	clock      clock.Clock
}

// New returns the control API.
func New(e *engine.Engine, d *callback.Dispatcher, c *scenario.Catalog, clk clock.Clock) *Control {
	return &Control{engine: e, dispatcher: d, catalog: c, clock: clk}
}

// Register adds the /_sandbox routes to mux.
func (c *Control) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /_sandbox/scenarios", c.scenarios)
	mux.HandleFunc("GET /_sandbox/payments/{id}/deliveries", c.deliveries)
	mux.HandleFunc("GET /_sandbox/payments/{id}/events", c.events)
	mux.HandleFunc("POST /_sandbox/payments/{id}/events", c.force)
	mux.HandleFunc("POST /_sandbox/payments/{id}/authenticate", c.authenticate)
	mux.HandleFunc("POST /_sandbox/deliveries/{id}/replay", c.replay)
	mux.HandleFunc("GET /_sandbox/clock", c.clockNow)
	mux.HandleFunc("POST /_sandbox/clock/advance", c.advance)
	mux.HandleFunc("POST /_sandbox/reset", c.reset)
}

type list struct {
	Data any `json:"data"`
}

func (c *Control) scenarios(w http.ResponseWriter, _ *http.Request) {
	defs := c.catalog.List()
	for i := range defs {
		if defs[i].Params == nil {
			defs[i].Params = []scenario.Param{}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, list{Data: defs})
}

func (c *Control) deliveries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := c.engine.Payment(id); err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list{Data: c.dispatcher.Deliveries(id)})
}

func (c *Control) events(w http.ResponseWriter, r *http.Request) {
	events, err := c.engine.Events(r.PathValue("id"))
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list{Data: events})
}

func (c *Control) force(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string `json:"type"`
		Reason  string `json:"reason"`
		Outcome string `json:"outcome"`
	}
	if !httpx.ReadJSON(w, r, &req, false) {
		return
	}
	p, ev, err := c.engine.Force(r.PathValue("id"), engine.ForceRequest{
		Type:    payment.EventType(req.Type),
		Reason:  req.Reason,
		Outcome: req.Outcome,
	})
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"payment": p, "event": ev})
}

// authenticate is what the customer does on the 3DS page, for tests that do
// not drive a browser.
func (c *Control) authenticate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Result string `json:"result"`
	}
	if !httpx.ReadJSON(w, r, &req, false) {
		return
	}
	if req.Result != "success" && req.Result != "failure" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "result must be success or failure")
		return
	}
	p, err := c.engine.Authenticate(r.PathValue("id"), req.Result == "success")
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (c *Control) replay(w http.ResponseWriter, r *http.Request) {
	d, err := c.dispatcher.Replay(r.PathValue("id"))
	if err != nil {
		httpx.WriteDomainError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, d)
}

type clockState struct {
	Now    time.Time `json:"now"`
	Manual bool      `json:"manual"`
}

func (c *Control) state() clockState {
	_, manual := c.clock.(*clock.Manual)
	return clockState{Now: c.clock.Now().UTC(), Manual: manual}
}

func (c *Control) clockNow(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, c.state())
}

// advance moves a manual clock. Everything that falls due runs before the
// answer, so a GET right after it sees the new statuses. Callbacks are still
// sent in the background.
func (c *Control) advance(w http.ResponseWriter, r *http.Request) {
	m, ok := c.clock.(*clock.Manual)
	if !ok {
		httpx.WriteError(w, http.StatusConflict, "clock_not_manual", "start the sandbox with PSP_CLOCK=manual to move the clock")
		return
	}
	var req struct {
		Seconds float64 `json:"seconds"`
	}
	if !httpx.ReadJSON(w, r, &req, false) {
		return
	}
	// Ten years is far beyond any scenario and far below the time.Duration limit.
	const maxSeconds = 10 * 365 * 24 * 3600
	if req.Seconds <= 0 || req.Seconds > maxSeconds {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "seconds must be positive and at most 315360000 (10 years)")
		return
	}
	m.Advance(time.Duration(req.Seconds * float64(time.Second)))
	httpx.WriteJSON(w, http.StatusOK, c.state())
}

// reset drops everything, or with {"reference_prefix": "..."} only the
// payments of one test, so tests that share a sandbox can run in parallel.
func (c *Control) reset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReferencePrefix *string `json:"reference_prefix"`
	}
	if !httpx.ReadJSON(w, r, &req, true) {
		return
	}
	switch {
	case req.ReferencePrefix == nil:
		c.engine.Reset()
	case *req.ReferencePrefix == "":
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "reference_prefix must not be empty; send no body to reset everything")
		return
	default:
		c.engine.ResetPrefix(*req.ReferencePrefix)
	}
	w.WriteHeader(http.StatusNoContent)
}
