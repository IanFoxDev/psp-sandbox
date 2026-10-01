package store

import "errors"

var (
	// ErrIdempotencyConflict means the key was used with a different request.
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	// ErrIdempotencyInProgress means the first request with this key is still running.
	ErrIdempotencyInProgress = errors.New("a request with this idempotency key is in progress")
)

// Response is a stored answer to an idempotent request.
type Response struct {
	Status int
	Body   []byte
}

type idemEntry struct {
	fingerprint string
	done        bool
	resp        Response
	// paymentID is the payment the answer belongs to, so that dropping the
	// payment drops the key too.
	paymentID string
}

// BeginIdempotent claims key for a request identified by fingerprint (a hash
// of what makes the request unique, such as method, path and body).
//
// It returns (nil, nil) when the caller owns the key and must run the request,
// then call FinishIdempotent or AbortIdempotent. It returns the stored response
// when the same request already finished, ErrIdempotencyInProgress while it is
// still running, and ErrIdempotencyConflict when the fingerprint differs.
func (s *Store) BeginIdempotent(key, fingerprint string) (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.idem[key]
	if !ok {
		s.idem[key] = &idemEntry{fingerprint: fingerprint}
		return nil, nil
	}
	if e.fingerprint != fingerprint {
		return nil, ErrIdempotencyConflict
	}
	if !e.done {
		return nil, ErrIdempotencyInProgress
	}
	r := e.resp
	return &r, nil
}

// FinishIdempotent stores the response for a key claimed with BeginIdempotent.
// paymentID is the payment the response is about.
func (s *Store) FinishIdempotent(key string, r Response, paymentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.idem[key]; ok {
		e.done = true
		e.resp = r
		e.paymentID = paymentID
	}
}

// AbortIdempotent releases a claimed key so the request can be retried. Use it
// when the request failed before it changed anything.
func (s *Store) AbortIdempotent(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.idem[key]; ok && !e.done {
		delete(s.idem, key)
	}
}
