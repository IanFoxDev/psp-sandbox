// Package engine ties the sandbox together: it creates payments, asks the
// scenario what to do, applies status changes on the sandbox clock, records
// events and hands them to the callback dispatcher. The provider API and the
// control API are thin HTTP layers over it.
package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/ids"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// ErrInvalidScenario means the requested scenario or its parameters are wrong.
var ErrInvalidScenario = errors.New("invalid scenario")

// RefusedError is a create call that the scenario answered with a server error.
// Nothing was stored.
type RefusedError struct {
	Status   int
	Scenario string
	Attempt  int
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("scenario %s: call %d refused with %d", e.Scenario, e.Attempt, e.Status)
}

// Config holds the engine settings.
type Config struct {
	// ProcessingDelay is the usual time from create to the first status change.
	ProcessingDelay time.Duration
	// CallbackURL is used when a payment has no callback_url of its own.
	CallbackURL string
	// Rules pick a scenario when a request names none. May be nil.
	Rules *scenario.Rules
	// DefaultScenario applies when a request names none and no rule matches.
	DefaultScenario scenario.Spec
	// PaymentPrefix and RefundPrefix start new ids. Defaults: pay, ref.
	PaymentPrefix string
	RefundPrefix  string
}

// Engine runs payments. It is safe for concurrent use.
type Engine struct {
	cfg        Config
	clock      clock.Clock
	store      *store.Store
	dispatcher *callback.Dispatcher
	catalog    *scenario.Catalog
	ids        *ids.Generator
	log        *slog.Logger

	// mu serializes every status change together with sending its event, so
	// events reach the dispatcher in the order the changes happened.
	mu        sync.Mutex
	gen       uint64
	scenarios map[string]scenario.Scenario
	// refused counts refused create calls by CreateRequest.RetryKey.
	refused map[string]int
}

// Deps are the collaborators of an Engine.
type Deps struct {
	Clock      clock.Clock
	Store      *store.Store
	Dispatcher *callback.Dispatcher
	Catalog    *scenario.Catalog
	IDs        *ids.Generator
	Log        *slog.Logger
}

// New returns an engine. It checks that the default scenario exists.
func New(cfg Config, d Deps) (*Engine, error) {
	if _, err := d.Catalog.Build(cfg.DefaultScenario); err != nil {
		return nil, fmt.Errorf("default scenario: %w", err)
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.PaymentPrefix == "" {
		cfg.PaymentPrefix = "pay"
	}
	if cfg.RefundPrefix == "" {
		cfg.RefundPrefix = "ref"
	}
	return &Engine{
		cfg:        cfg,
		clock:      d.Clock,
		store:      d.Store,
		dispatcher: d.Dispatcher,
		catalog:    d.Catalog,
		ids:        d.IDs,
		log:        d.Log,
		scenarios:  map[string]scenario.Scenario{},
		refused:    map[string]int{},
	}, nil
}

// CreateRequest is a validated request to create a payment.
type CreateRequest struct {
	Amount      int64
	Currency    string
	Reference   string
	Capture     payment.CaptureMode
	CallbackURL string
	Metadata    map[string]string
	// PaymentMethod and Description are kept for APIs that have them.
	PaymentMethod string
	Description   string
	// Scenario is nil when the request did not name one.
	Scenario *scenario.Spec
	// RetryKey tells retries of one request apart from new requests, for
	// scenarios that refuse the first calls. The API sets it from the
	// Idempotency-Key, or from the request itself when there is none.
	RetryKey string
}

// Created is the result of Create and Confirm.
type Created struct {
	// Payment is the payment as it was when confirmed (pending).
	Payment payment.Payment
	// Response says how to shape the HTTP answer.
	Response scenario.Response
	// FirstCallback is closed once the first callback attempt for the payment
	// has finished, or when it is clear that none will be made.
	FirstCallback <-chan struct{}
	// Settled is closed once the first status step of the scenario has been
	// applied or skipped, or when the scenario has no steps. APIs that answer
	// with the outcome wait for it.
	Settled <-chan struct{}
}

// Create stores a new pending payment and starts its scenario. Nothing is
// stored when the scenario refuses the call.
func (e *Engine) Create(req CreateRequest) (Created, error) {
	sc, err := e.pick(req.Scenario, scenario.Input{
		Amount: req.Amount, Currency: req.Currency, Reference: req.Reference, Metadata: req.Metadata,
	})
	if err != nil {
		return Created{}, err
	}
	if err := e.refuseIf(sc, req.RetryKey); err != nil {
		return Created{}, err
	}

	p := e.newPayment(req, payment.Pending)
	p.Scenario = sc.Name()
	p.Attempt = 1

	e.mu.Lock()
	e.store.AddPayment(p)
	e.scenarios[p.ID] = sc
	gen := e.gen
	e.mu.Unlock()

	e.log.Info("payment created", "payment", p.ID, "amount", p.Amount, "currency", p.Currency, "scenario", sc.Name())
	return e.start(gen, p, sc), nil
}

// Prepare stores a new unconfirmed payment and sends payment.created. No
// scenario runs until Confirm; req.Scenario and req.RetryKey are not used.
func (e *Engine) Prepare(req CreateRequest) (payment.Payment, error) {
	p := e.newPayment(req, payment.Unconfirmed)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.store.AddPayment(p)
	e.emitLocked(p, payment.EventPaymentCreated, p)
	e.log.Info("payment prepared", "payment", p.ID, "amount", p.Amount, "currency", p.Currency)
	return p, nil
}

// ConfirmRequest is a validated request to confirm a payment.
type ConfirmRequest struct {
	// Scenario is nil when the request did not name one. Rules then match the
	// payment as it is stored.
	Scenario *scenario.Spec
	// PaymentMethod replaces the stored one when not empty.
	PaymentMethod string
	// RetryKey is as in CreateRequest.
	RetryKey string
}

// Confirm sends an unconfirmed payment for processing, or starts a new
// attempt of a failed one, with a scenario picked now. Steps left over from an
// earlier attempt are skipped. When the scenario refuses the call, the payment
// stays as it was.
func (e *Engine) Confirm(id string, req ConfirmRequest) (Created, error) {
	cur, err := e.store.Payment(id)
	if err != nil {
		return Created{}, err
	}
	if cur.Status != payment.Unconfirmed && cur.Status != payment.Failed {
		return Created{}, fmt.Errorf("%w: payment is %s, only unconfirmed or failed payments can be confirmed",
			payment.ErrInvalidState, cur.Status)
	}
	sc, err := e.pick(req.Scenario, scenario.Input{
		Amount: cur.Amount, Currency: cur.Currency, Reference: cur.Reference, Metadata: cur.Metadata,
	})
	if err != nil {
		return Created{}, err
	}
	if err := e.refuseIf(sc, req.RetryKey); err != nil {
		return Created{}, err
	}
	return e.confirm(id, req.PaymentMethod, sc)
}

// PrepareAndConfirm is Prepare followed by Confirm in one call, for APIs that
// create and confirm in one request. The scenario is picked before anything is
// stored, so a refused call leaves nothing behind, as Create does.
func (e *Engine) PrepareAndConfirm(req CreateRequest, c ConfirmRequest) (Created, error) {
	sc, err := e.pick(c.Scenario, scenario.Input{
		Amount: req.Amount, Currency: req.Currency, Reference: req.Reference, Metadata: req.Metadata,
	})
	if err != nil {
		return Created{}, err
	}
	if err := e.refuseIf(sc, c.RetryKey); err != nil {
		return Created{}, err
	}
	p, err := e.Prepare(req)
	if err != nil {
		return Created{}, err
	}
	return e.confirm(p.ID, c.PaymentMethod, sc)
}

func (e *Engine) confirm(id, paymentMethod string, sc scenario.Scenario) (Created, error) {
	e.mu.Lock()
	now := e.now()
	p, err := e.store.UpdatePayment(id, func(p *payment.Payment) error {
		if err := p.Confirm(now); err != nil {
			return err
		}
		if paymentMethod != "" {
			p.PaymentMethod = paymentMethod
		}
		p.Scenario = sc.Name()
		return nil
	})
	if err != nil {
		e.mu.Unlock()
		return Created{}, err
	}
	e.scenarios[p.ID] = sc
	gen := e.gen
	e.mu.Unlock()

	e.log.Info("payment confirmed", "payment", p.ID, "attempt", p.Attempt, "scenario", sc.Name())
	return e.start(gen, p, sc), nil
}

// CheckScenario reports whether spec names a known scenario with valid
// parameters, for APIs that take a scenario before they use it.
func (e *Engine) CheckScenario(spec scenario.Spec) error {
	if _, err := e.catalog.Build(spec); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidScenario, err)
	}
	return nil
}

// pick builds the requested scenario, or the one the rules give for in, or
// the default.
func (e *Engine) pick(requested *scenario.Spec, in scenario.Input) (scenario.Scenario, error) {
	spec := e.cfg.DefaultScenario
	if requested != nil {
		spec = *requested
	} else if s, ok := e.cfg.Rules.Match(in); ok {
		spec = s
	}
	sc, err := e.catalog.Build(spec)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidScenario, err)
	}
	return sc, nil
}

func (e *Engine) refuseIf(sc scenario.Scenario, key string) error {
	if rf, ok := sc.(scenario.Refuser); ok {
		return e.refuse(rf, sc.Name(), key)
	}
	return nil
}

func (e *Engine) newPayment(req CreateRequest, status payment.Status) payment.Payment {
	now := e.now()
	p := payment.Payment{
		ID:          e.ids.Next(e.cfg.PaymentPrefix),
		Status:      status,
		Amount:      req.Amount,
		Currency:    req.Currency,
		Reference:   req.Reference,
		Capture:     req.Capture,
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    req.Metadata,
		CallbackURL: req.CallbackURL,

		PaymentMethod: req.PaymentMethod,
		Description:   req.Description,
	}
	if p.Capture == "" {
		p.Capture = payment.CaptureAuto
	}
	if p.CallbackURL == "" {
		p.CallbackURL = e.cfg.CallbackURL
	}
	return p
}

// start asks the scenario for its plan and runs the steps of this attempt.
func (e *Engine) start(gen uint64, p payment.Payment, sc scenario.Scenario) Created {
	plan := sc.OnCreate(scenario.CreateContext{Payment: p.Clone(), ProcessingDelay: e.cfg.ProcessingDelay})
	first := make(chan struct{})
	settled := make(chan struct{})
	r := run{e: e, gen: gen, paymentID: p.ID, attempt: p.Attempt}
	r.steps(plan.Steps, 0, first, settled)
	return Created{Payment: p, Response: plan.Response, FirstCallback: first, Settled: settled}
}

// refuse counts the call and returns a RefusedError if the scenario refuses
// it. The count is dropped once a call gets through.
func (e *Engine) refuse(rf scenario.Refuser, name, key string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.refused[key]
	status, refused := rf.Refuse(n)
	if !refused {
		delete(e.refused, key)
		return nil
	}
	e.refused[key] = n + 1
	e.log.Info("create refused by scenario", "scenario", name, "attempt", n+1, "status", status)
	return &RefusedError{Status: status, Scenario: name, Attempt: n + 1}
}

// run is one attempt of a payment: its steps apply only while the payment is
// still on that attempt.
type run struct {
	e         *Engine
	gen       uint64
	paymentID string
	attempt   int
}

// steps applies steps[i:] in order at their offsets from confirmation. Steps
// due now are applied before it returns. first is closed after the first
// callback attempt of the first event, or when the steps run out without one.
// settled is closed after the first step, applied or skipped.
func (r run) steps(steps []scenario.Step, elapsed time.Duration, first, settled chan struct{}) {
	for i, step := range steps {
		if step.After > elapsed {
			rest := steps[i:]
			at := step.After
			r.e.clock.AfterFunc(at-elapsed, func() { r.steps(rest, at, first, settled) })
			return
		}
		sent := r.e.applyStep(r, step)
		if settled != nil {
			close(settled)
			settled = nil
		}
		if sent != nil && first != nil {
			go func(first chan struct{}) {
				<-sent
				close(first)
			}(first)
			first = nil
		}
	}
	if first != nil {
		close(first)
	}
	if settled != nil {
		close(settled)
	}
}

// applyStep changes the payment status and sends the event. It returns the
// dispatcher channel of that event, or nil if nothing was sent.
func (e *Engine) applyStep(r run, step scenario.Step) <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.gen != e.gen {
		return nil
	}
	if p, err := e.store.Payment(r.paymentID); err == nil && p.Attempt != r.attempt {
		e.log.Info("scheduled status change skipped", "payment", r.paymentID, "to", step.Status,
			"err", "step of attempt "+strconv.Itoa(r.attempt)+", payment is on attempt "+strconv.Itoa(p.Attempt))
		return nil
	}
	_, _, sent, err := e.transitionLocked(r.paymentID, step.Status, step.Reason)
	if err != nil {
		e.log.Info("scheduled status change skipped", "payment", r.paymentID, "to", step.Status, "err", err)
		return nil
	}
	return sent
}

// transitionLocked moves a payment to status to and sends the matching event.
// e.mu must be held.
func (e *Engine) transitionLocked(paymentID string, to payment.Status, reason string) (payment.Payment, payment.Event, <-chan struct{}, error) {
	now := e.now()
	p, err := e.store.UpdatePayment(paymentID, func(p *payment.Payment) error {
		switch to {
		case payment.Failed:
			return p.Fail(reason, now)
		case payment.Captured:
			return p.CaptureAmount(0, now)
		default:
			return p.Become(to, now)
		}
	})
	if err != nil {
		return p, payment.Event{}, nil, err
	}
	ev, sent := e.emitPaymentLocked(p)
	return p, ev, sent, nil
}

// emitPaymentLocked sends the event for the payment's current status, if it has one.
func (e *Engine) emitPaymentLocked(p payment.Payment) (payment.Event, <-chan struct{}) {
	typ, ok := payment.EventTypeFor(p.Status)
	if !ok {
		return payment.Event{}, nil
	}
	return e.emitLocked(p, typ, p)
}

// emitLocked records an event and hands it to the dispatcher. e.mu must be held.
func (e *Engine) emitLocked(p payment.Payment, typ payment.EventType, data any) (payment.Event, <-chan struct{}) {
	ev := payment.Event{
		ID:        e.ids.Next("evt"),
		Type:      typ,
		CreatedAt: e.now(),
		Data:      data,
		PaymentID: p.ID,
	}
	e.store.AddEvent(ev)
	if p.CallbackURL == "" {
		e.log.Warn("no callback URL, event not delivered", "payment", p.ID, "event", ev.ID, "type", typ)
		return ev, nil
	}
	return ev, e.dispatcher.Send(ev, p.CallbackURL, e.scenarioLocked(p.ID).Deliver(ev))
}

func (e *Engine) scenarioLocked(paymentID string) scenario.Scenario {
	if sc, ok := e.scenarios[paymentID]; ok {
		return sc
	}
	sc, _ := e.catalog.Build(e.cfg.DefaultScenario)
	return sc
}

// now is the sandbox time as stored on objects: UTC, milliseconds.
func (e *Engine) now() time.Time {
	return e.clock.Now().UTC().Truncate(time.Millisecond)
}

// Payment returns one payment.
func (e *Engine) Payment(id string) (payment.Payment, error) {
	return e.store.Payment(id)
}

// Events returns the events of a payment in the order they happened.
func (e *Engine) Events(paymentID string) ([]payment.Event, error) {
	if _, err := e.store.Payment(paymentID); err != nil {
		return nil, err
	}
	return e.store.Events(paymentID), nil
}

// Payments lists payments, optionally only those with a reference.
func (e *Engine) Payments(reference string) []payment.Payment {
	return e.store.Payments(reference)
}

// Capture captures an authorized payment. Zero amount means the full amount.
func (e *Engine) Capture(id string, amount int64) (payment.Payment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	p, err := e.store.UpdatePayment(id, func(p *payment.Payment) error {
		if p.Status != payment.Authorized {
			return fmt.Errorf("%w: payment is %s, not authorized", payment.ErrInvalidState, p.Status)
		}
		return p.CaptureAmount(amount, now)
	})
	if err != nil {
		return p, err
	}
	e.emitPaymentLocked(p)
	return p, nil
}

// Update changes fields of a payment that are not its state, such as metadata,
// or the amount before confirmation. f must not change the status; it returns
// an error to leave the payment as it was.
func (e *Engine) Update(id string, f func(p *payment.Payment) error) (payment.Payment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	return e.store.UpdatePayment(id, func(p *payment.Payment) error {
		before := p.Status
		if err := f(p); err != nil {
			return err
		}
		if p.Status != before {
			return fmt.Errorf("%w: Update cannot change the status", payment.ErrInvalidState)
		}
		p.UpdatedAt = now
		return nil
	})
}

// Abandon cancels an unconfirmed or failed payment and sends payment.canceled.
func (e *Engine) Abandon(id string) (payment.Payment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	p, err := e.store.UpdatePayment(id, func(p *payment.Payment) error { return p.Abandon(now) })
	if err != nil {
		return p, err
	}
	e.emitPaymentLocked(p)
	return p, nil
}

// Cancel voids a pending or authorized payment.
func (e *Engine) Cancel(id string) (payment.Payment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	p, err := e.store.UpdatePayment(id, func(p *payment.Payment) error { return p.Cancel(now) })
	if err != nil {
		return p, err
	}
	e.emitPaymentLocked(p)
	return p, nil
}

// Refund accepts a refund. It settles after the processing delay and sends
// refund.succeeded.
func (e *Engine) Refund(paymentID string, amount int64, reference string) (payment.Refund, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	_, r, err := e.store.AddRefund(paymentID, func(p *payment.Payment) (payment.Refund, error) {
		if err := p.ReserveRefund(amount); err != nil {
			return payment.Refund{}, err
		}
		return payment.Refund{
			ID:        e.ids.Next(e.cfg.RefundPrefix),
			PaymentID: p.ID,
			Status:    payment.RefundPending,
			Amount:    amount,
			Currency:  p.Currency,
			Reference: reference,
			CreatedAt: now,
			UpdatedAt: now,
		}, nil
	})
	if err != nil {
		return r, err
	}
	gen := e.gen
	e.clock.AfterFunc(e.cfg.ProcessingDelay, func() { e.settleRefund(gen, r.ID) })
	return r, nil
}

func (e *Engine) settleRefund(gen uint64, refundID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if gen != e.gen {
		return
	}
	now := e.now()
	p, r, err := e.store.UpdateRefund(refundID, func(p *payment.Payment, r *payment.Refund) error {
		r.UpdatedAt = now
		if err := p.SettleRefund(r.Amount, now); err != nil {
			// The payment moved on meanwhile, for example to disputed.
			p.ReleaseRefund(r.Amount)
			r.Status = payment.RefundFailed
			r.FailureReason = "payment_" + string(p.Status)
			return nil
		}
		r.Status = payment.RefundSucceeded
		return nil
	})
	if err != nil {
		e.log.Info("refund not settled", "refund", refundID, "err", err)
		return
	}
	typ := payment.EventRefundSucceeded
	if r.Status == payment.RefundFailed {
		typ = payment.EventRefundFailed
	}
	e.emitLocked(p, typ, r)
}

// ResetPrefix drops the payments whose reference starts with prefix, with
// their events, refunds, idempotency keys and deliveries. Their pending
// status changes find no payment and are skipped. Other payments go on.
func (e *Engine) ResetPrefix(prefix string) {
	e.mu.Lock()
	ids := e.store.DropByReferencePrefix(prefix)
	for _, id := range ids {
		delete(e.scenarios, id)
	}
	e.mu.Unlock()
	e.dispatcher.Drop(ids)
	e.log.Info("payments reset", "reference_prefix", prefix, "count", len(ids))
}

// Reset drops all state: payments, pending status changes and deliveries.
func (e *Engine) Reset() {
	e.mu.Lock()
	e.gen++
	e.scenarios = map[string]scenario.Scenario{}
	e.refused = map[string]int{}
	e.store.Reset()
	e.mu.Unlock()
	e.dispatcher.Reset()
}
