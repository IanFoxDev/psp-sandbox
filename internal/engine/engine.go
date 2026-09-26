// Package engine ties the sandbox together: it creates payments, asks the
// scenario what to do, applies status changes on the sandbox clock, records
// events and hands them to the callback dispatcher. The provider API and the
// control API are thin HTTP layers over it.
package engine

import (
	"errors"
	"fmt"
	"log/slog"
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
	return &Engine{
		cfg:        cfg,
		clock:      d.Clock,
		store:      d.Store,
		dispatcher: d.Dispatcher,
		catalog:    d.Catalog,
		ids:        d.IDs,
		log:        d.Log,
		scenarios:  map[string]scenario.Scenario{},
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
	// Scenario is nil when the request did not name one.
	Scenario *scenario.Spec
}

// Created is the result of Create.
type Created struct {
	// Payment is the payment as it was when created (pending).
	Payment payment.Payment
	// Response says how to shape the HTTP answer.
	Response scenario.Response
	// FirstCallback is closed once the first callback attempt for the payment
	// has finished, or when it is clear that none will be made.
	FirstCallback <-chan struct{}
}

// Create stores a new pending payment and starts its scenario.
func (e *Engine) Create(req CreateRequest) (Created, error) {
	spec := e.cfg.DefaultScenario
	if req.Scenario != nil {
		spec = *req.Scenario
	} else if s, ok := e.cfg.Rules.Match(scenario.Input{
		Amount: req.Amount, Currency: req.Currency, Reference: req.Reference, Metadata: req.Metadata,
	}); ok {
		spec = s
	}
	sc, err := e.catalog.Build(spec)
	if err != nil {
		return Created{}, fmt.Errorf("%w: %w", ErrInvalidScenario, err)
	}

	now := e.now()
	p := payment.Payment{
		ID:          e.ids.Next("pay"),
		Status:      payment.Pending,
		Amount:      req.Amount,
		Currency:    req.Currency,
		Reference:   req.Reference,
		Capture:     req.Capture,
		Scenario:    sc.Name(),
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    req.Metadata,
		CallbackURL: req.CallbackURL,
	}
	if p.Capture == "" {
		p.Capture = payment.CaptureAuto
	}
	if p.CallbackURL == "" {
		p.CallbackURL = e.cfg.CallbackURL
	}

	e.mu.Lock()
	e.store.AddPayment(p)
	e.scenarios[p.ID] = sc
	gen := e.gen
	e.mu.Unlock()

	e.log.Info("payment created", "payment", p.ID, "amount", p.Amount, "currency", p.Currency, "scenario", sc.Name())

	plan := sc.OnCreate(scenario.CreateContext{Payment: p.Clone(), ProcessingDelay: e.cfg.ProcessingDelay})
	first := make(chan struct{})
	e.runSteps(gen, p.ID, plan.Steps, 0, first)

	return Created{Payment: p, Response: plan.Response, FirstCallback: first}, nil
}

// runSteps applies steps[i:] in order at their offsets from creation. Steps
// due now are applied before it returns. first is closed after the first
// callback attempt of the first event, or when the steps run out without one.
func (e *Engine) runSteps(gen uint64, paymentID string, steps []scenario.Step, elapsed time.Duration, first chan struct{}) {
	for i, step := range steps {
		if step.After > elapsed {
			rest := steps[i:]
			at := step.After
			e.clock.AfterFunc(at-elapsed, func() { e.runSteps(gen, paymentID, rest, at, first) })
			return
		}
		sent := e.applyStep(gen, paymentID, step)
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
}

// applyStep changes the payment status and sends the event. It returns the
// dispatcher channel of that event, or nil if nothing was sent.
func (e *Engine) applyStep(gen uint64, paymentID string, step scenario.Step) <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if gen != e.gen {
		return nil
	}
	_, _, sent, err := e.transitionLocked(paymentID, step.Status, step.Reason)
	if err != nil {
		e.log.Info("scheduled status change skipped", "payment", paymentID, "to", step.Status, "err", err)
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
			ID:        e.ids.Next("ref"),
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

// Reset drops all state: payments, pending status changes and deliveries.
func (e *Engine) Reset() {
	e.mu.Lock()
	e.gen++
	e.scenarios = map[string]scenario.Scenario{}
	e.store.Reset()
	e.mu.Unlock()
	e.dispatcher.Reset()
}
