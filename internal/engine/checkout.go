package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// DefaultSessionLifetime is how long a checkout session stays open unless the
// request says otherwise.
const DefaultSessionLifetime = 24 * time.Hour

// SessionRequest is a validated request to open a checkout session.
type SessionRequest struct {
	Currency          string
	LineItems         []payment.LineItem
	SuccessURL        string
	CancelURL         string
	ClientReferenceID string
	CustomerEmail     string
	Metadata          map[string]string
	PaymentMetadata   map[string]string
	// ExpiresAt zero means DefaultSessionLifetime from now.
	ExpiresAt   time.Time
	Reference   string
	CallbackURL string
}

// Now is the sandbox time, as stored on objects.
func (e *Engine) Now() time.Time {
	return e.now()
}

// CreateSession opens a checkout session and schedules its expiry on the
// sandbox clock.
func (e *Engine) CreateSession(req SessionRequest) (payment.Session, error) {
	now := e.now()
	cs := payment.Session{
		ID:                e.ids.Next(e.cfg.SessionPrefix),
		Status:            payment.SessionOpen,
		Currency:          req.Currency,
		SuccessURL:        req.SuccessURL,
		CancelURL:         req.CancelURL,
		ClientReferenceID: req.ClientReferenceID,
		CustomerEmail:     req.CustomerEmail,
		Metadata:          req.Metadata,
		PaymentMetadata:   req.PaymentMetadata,
		CreatedAt:         now,
		ExpiresAt:         req.ExpiresAt,
		Reference:         req.Reference,
		CallbackURL:       req.CallbackURL,
	}
	if cs.ExpiresAt.IsZero() {
		cs.ExpiresAt = now.Add(DefaultSessionLifetime)
	}
	if cs.CallbackURL == "" {
		cs.CallbackURL = e.cfg.CallbackURL
	}
	for _, l := range req.LineItems {
		l.ID = e.ids.Next("li")
		cs.LineItems = append(cs.LineItems, l)
	}

	e.mu.Lock()
	e.store.AddSession(cs)
	gen := e.gen
	e.mu.Unlock()
	e.clock.AfterFunc(cs.ExpiresAt.Sub(now), func() {
		if _, err := e.expire(gen, cs.ID); err != nil && !errors.Is(err, payment.ErrInvalidState) && !errors.Is(err, errStaleGeneration) {
			e.log.Info("session not expired", "session", cs.ID, "err", err)
		}
	})
	e.log.Info("session created", "session", cs.ID, "amount", cs.AmountTotal(), "currency", cs.Currency)
	return cs, nil
}

// Session returns one checkout session.
func (e *Engine) Session(id string) (payment.Session, error) {
	return e.store.Session(id)
}

// Sessions returns the checkout sessions in the order they were created.
func (e *Engine) Sessions() []payment.Session {
	return e.store.Sessions()
}

// PaySession is the customer paying on the session's page: the first attempt
// creates the payment, later ones (after a decline) confirm it again. The
// session completes when the payment succeeds.
func (e *Engine) PaySession(id string, c ConfirmRequest) (Created, error) {
	e.payMu.Lock()
	defer e.payMu.Unlock()
	cs, err := e.store.Session(id)
	if err != nil {
		return Created{}, err
	}
	if cs.Status != payment.SessionOpen {
		return Created{}, fmt.Errorf("%w: the session is %s", payment.ErrInvalidState, cs.Status)
	}
	if cs.PaymentID != "" {
		return e.Confirm(cs.PaymentID, c)
	}
	created, err := e.PrepareAndConfirm(CreateRequest{
		Amount:        cs.AmountTotal(),
		Currency:      cs.Currency,
		Reference:     cs.Reference,
		Capture:       payment.CaptureAuto,
		CallbackURL:   cs.CallbackURL,
		Metadata:      cs.PaymentMetadata,
		PaymentMethod: c.PaymentMethod,
		ReturnURL:     c.ReturnURL,
		SessionID:     cs.ID,
	}, c)
	if err != nil {
		return created, err
	}
	// Steps due at once may have completed the session already.
	_, err = e.store.UpdateSession(id, func(s *payment.Session) error {
		s.PaymentID = created.Payment.ID
		return nil
	})
	return created, err
}

// ExpireSession expires an open session now.
func (e *Engine) ExpireSession(id string) (payment.Session, error) {
	e.mu.Lock()
	gen := e.gen
	e.mu.Unlock()
	return e.expire(gen, id)
}

var errStaleGeneration = errors.New("the sandbox was reset")

// expire moves an open session to expired and cancels its payment if that
// has not succeeded, as Stripe does with cancellation_reason expired.
func (e *Engine) expire(gen uint64, id string) (payment.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if gen != e.gen {
		return payment.Session{}, errStaleGeneration
	}
	now := e.now()
	cs, err := e.store.UpdateSession(id, func(s *payment.Session) error {
		if s.Status != payment.SessionOpen {
			return fmt.Errorf("%w: only an open session can be expired, this one is %s", payment.ErrInvalidState, s.Status)
		}
		s.Status = payment.SessionExpired
		return nil
	})
	if err != nil {
		return cs, err
	}
	e.emitSessionLocked(cs, payment.EventCheckoutExpired)
	if cs.PaymentID == "" {
		return cs, nil
	}
	p, err := e.store.UpdatePayment(cs.PaymentID, func(p *payment.Payment) error {
		var err error
		switch p.Status {
		case payment.Unconfirmed, payment.Failed:
			err = p.Abandon(now)
		case payment.Pending, payment.RequiresAction:
			err = p.Cancel(now)
		default:
			return payment.ErrInvalidState
		}
		p.CancellationReason = "expired"
		return err
	})
	if err == nil {
		e.emitPaymentLocked(p)
	}
	return cs, nil
}

// completeSessionLocked completes the session of a payment that has just
// succeeded and sends checkout.completed after the payment's own events.
// e.mu must be held.
func (e *Engine) completeSessionLocked(p payment.Payment) {
	now := e.now()
	cs, err := e.store.UpdateSession(p.SessionID, func(s *payment.Session) error {
		if s.Status != payment.SessionOpen {
			return payment.ErrInvalidState
		}
		s.Status = payment.SessionComplete
		s.CompletedAt = now
		s.PaymentID = p.ID
		return nil
	})
	if err != nil {
		e.log.Info("session not completed", "session", p.SessionID, "payment", p.ID, "err", err)
		return
	}
	e.emitSessionLocked(cs, payment.EventCheckoutCompleted)
}

// emitSessionLocked records a session event and delivers it with the plan of
// the session payment's scenario, in that payment's queue. A session without a
// payment uses its own id and the default scenario. e.mu must be held.
func (e *Engine) emitSessionLocked(cs payment.Session, typ payment.EventType) {
	key := cs.PaymentID
	var snapshot payment.Payment
	if key != "" {
		snapshot, _ = e.store.Payment(key)
	} else {
		key = cs.ID
	}
	ev := payment.Event{
		ID:        e.ids.Next("evt"),
		Type:      typ,
		CreatedAt: e.now(),
		Data:      cs.Clone(),
		PaymentID: key,
		Snapshot:  snapshot,
	}
	e.store.AddEvent(ev)
	if cs.CallbackURL == "" {
		e.log.Warn("no callback URL, event not delivered", "session", cs.ID, "event", ev.ID, "type", typ)
		return
	}
	e.dispatcher.Send(ev, cs.CallbackURL, e.scenarioLocked(key).Deliver(ev))
}
