// Package scenario defines the Scenario interface, the built-in catalog, parsing
// of the X-Sandbox-Scenario header and matching of rules from the rules file.
//
// A scenario has two hooks: OnCreate decides the synchronous answer to a create
// request and the schedule of status changes, Deliver decides how each event is
// delivered. Most scenarios override only one of them by embedding base.
package scenario

import (
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// Scenario is the behavior of the sandbox for one payment.
type Scenario interface {
	Name() string
	// OnCreate is called once, after the payment is stored as pending.
	OnCreate(c CreateContext) Plan
	// Deliver is called for every event of the payment and its refunds.
	Deliver(e payment.Event) callback.Plan
}

// CreateContext is what a scenario knows when a payment is created.
type CreateContext struct {
	Payment payment.Payment
	// ProcessingDelay is PSP_PROCESSING_DELAY: the usual time until the first
	// status change.
	ProcessingDelay time.Duration
}

// Plan is the outcome of OnCreate.
type Plan struct {
	Response Response
	// Steps are status changes, applied in order at their offset from creation
	// on the sandbox clock. A step that is no longer allowed (for example, the
	// payment was canceled meanwhile) is skipped.
	Steps []Step
}

// Step is one scheduled status change.
type Step struct {
	After  time.Duration
	Status payment.Status
	// Reason is the decline reason when Status is failed.
	Reason string
}

// Response shapes the HTTP answer to the create request. Its timings are wall
// clock, since they are about the network, not the payment.
type Response struct {
	// AfterFirstCallback holds the answer until the first callback attempt for
	// the payment has finished.
	AfterFirstCallback bool
	// Delay holds the answer this long (after the first callback, if that is set).
	Delay time.Duration
	// Reset closes the connection without an answer.
	Reset bool
}

// base gives the default behavior: settle after the processing delay and send
// each event once.
type base struct {
	name string
}

func (b base) Name() string { return b.name }

func (base) OnCreate(c CreateContext) Plan {
	return Plan{Steps: []Step{settle(c)}}
}

func (base) Deliver(payment.Event) callback.Plan {
	return callback.Plan{}
}

// settle is the normal outcome: captured, or authorized with manual capture.
func settle(c CreateContext) Step {
	s := Step{After: c.ProcessingDelay, Status: payment.Captured}
	if c.Payment.Capture == payment.CaptureManual {
		s.Status = payment.Authorized
	}
	return s
}
