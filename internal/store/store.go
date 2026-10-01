// Package store keeps payments, refunds, events and idempotency keys in
// memory. Reset drops everything between tests.
//
// Every method is safe for concurrent use. Values go in and out as copies, and
// the update functions run under the store lock, so a read-check-write on one
// payment cannot interleave with another.
package store

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// ErrNotFound means there is no object with this id.
var ErrNotFound = errors.New("not found")

// Store is the in-memory state of the sandbox.
type Store struct {
	mu       sync.Mutex
	payments map[string]*payment.Payment
	order    []string
	refunds  map[string]*payment.Refund
	events   map[string][]payment.Event
	idem     map[string]*idemEntry
}

// New returns an empty store.
func New() *Store {
	s := &Store{}
	s.reset()
	return s
}

// Reset drops all payments, refunds, events and idempotency keys.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}

func (s *Store) reset() {
	s.payments = map[string]*payment.Payment{}
	s.order = nil
	s.refunds = map[string]*payment.Refund{}
	s.events = map[string][]payment.Event{}
	s.idem = map[string]*idemEntry{}
}

// DropByReferencePrefix removes the payments whose reference starts with
// prefix, with their refunds, events and idempotency keys, and returns their ids.
func (s *Store) DropByReferencePrefix(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := map[string]bool{}
	order := s.order[:0]
	for _, id := range s.order {
		if strings.HasPrefix(s.payments[id].Reference, prefix) {
			dropped[id] = true
			delete(s.payments, id)
			delete(s.events, id)
			continue
		}
		order = append(order, id)
	}
	s.order = order
	for id, r := range s.refunds {
		if dropped[r.PaymentID] {
			delete(s.refunds, id)
		}
	}
	for key, e := range s.idem {
		if dropped[e.paymentID] {
			delete(s.idem, key)
		}
	}
	return slices.Sorted(maps.Keys(dropped))
}

// AddPayment stores a new payment.
func (s *Store) AddPayment(p payment.Payment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := p.Clone()
	s.payments[p.ID] = &c
	s.order = append(s.order, p.ID)
}

// Payment returns the payment with this id.
func (s *Store) Payment(id string) (payment.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[id]
	if !ok {
		return payment.Payment{}, ErrNotFound
	}
	return p.Clone(), nil
}

// Payments returns payments in creation order, newest last. If reference is
// not empty, only payments with that reference are returned.
func (s *Store) Payments(reference string) []payment.Payment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []payment.Payment{}
	for _, id := range s.order {
		p := s.payments[id]
		if reference == "" || p.Reference == reference {
			out = append(out, p.Clone())
		}
	}
	return out
}

// UpdatePayment runs fn on the payment under the store lock. If fn returns an
// error, the payment is left as it was.
func (s *Store) UpdatePayment(id string, fn func(p *payment.Payment) error) (payment.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.payments[id]
	if !ok {
		return payment.Payment{}, ErrNotFound
	}
	next := cur.Clone()
	if err := fn(&next); err != nil {
		return cur.Clone(), err
	}
	s.payments[id] = &next
	return next.Clone(), nil
}

// AddRefund builds a refund from its payment and stores both in one step.
// build may change the payment (for example, reserve the refund amount).
// If build returns an error, nothing is stored.
func (s *Store) AddRefund(paymentID string, build func(p *payment.Payment) (payment.Refund, error)) (payment.Payment, payment.Refund, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.payments[paymentID]
	if !ok {
		return payment.Payment{}, payment.Refund{}, ErrNotFound
	}
	next := cur.Clone()
	r, err := build(&next)
	if err != nil {
		return cur.Clone(), payment.Refund{}, err
	}
	s.payments[paymentID] = &next
	s.refunds[r.ID] = &r
	return next.Clone(), r, nil
}

// UpdateRefund runs fn on a refund and its payment under the store lock.
// If fn returns an error, neither is changed.
func (s *Store) UpdateRefund(id string, fn func(p *payment.Payment, r *payment.Refund) error) (payment.Payment, payment.Refund, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cr, ok := s.refunds[id]
	if !ok {
		return payment.Payment{}, payment.Refund{}, ErrNotFound
	}
	cp, ok := s.payments[cr.PaymentID]
	if !ok {
		return payment.Payment{}, payment.Refund{}, ErrNotFound
	}
	p, r := cp.Clone(), *cr
	if err := fn(&p, &r); err != nil {
		return cp.Clone(), *cr, err
	}
	s.payments[p.ID] = &p
	s.refunds[r.ID] = &r
	return p.Clone(), r, nil
}

// AddEvent appends an event to the history of its payment.
func (s *Store) AddEvent(e payment.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[e.PaymentID] = append(s.events[e.PaymentID], e)
}

// Events returns the events of a payment in the order they happened.
func (s *Store) Events(paymentID string) []payment.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events[paymentID])
}
