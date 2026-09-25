package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// HeaderIdempotencyKey is the request header for safe retries.
const HeaderIdempotencyKey = "Idempotency-Key"

// HeaderReplayed is set on a response that was stored for an earlier request.
const HeaderReplayed = "Idempotent-Replayed"

type idemKey struct{}

// idempotent makes a POST handler safe to retry. The first request with a key
// runs; a later one with the same key and the same body gets the stored answer;
// one with a different body gets 409.
//
// The handler stores its answer with commit as soon as the answer is known,
// which may be before it is written: a scenario that holds the response still
// lets a retry with the same key see the payment.
func (a *API) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(HeaderIdempotencyKey)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "cannot read body: "+err.Error())
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		scope := r.Method + " " + r.URL.Path + " " + key
		fp := fingerprint(body, []byte(r.Header.Get(scenario.Header)))
		stored, err := a.store.BeginIdempotent(scope, fp)
		switch {
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotency_conflict", "this Idempotency-Key was used with a different request")
			return
		case errors.Is(err, store.ErrIdempotencyInProgress):
			writeError(w, http.StatusConflict, "idempotency_conflict", "a request with this Idempotency-Key is still in progress")
			return
		case stored != nil:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(HeaderReplayed, "true")
			w.WriteHeader(stored.Status)
			_, _ = w.Write(stored.Body)
			return
		}

		c := &commit{store: a.store, scope: scope}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), idemKey{}, c)))
		if !c.done {
			a.store.AbortIdempotent(scope)
		}
	})
}

type commit struct {
	store *store.Store
	scope string
	done  bool
}

// commitResponse stores the answer for the request's Idempotency-Key, if any.
func commitResponse(r *http.Request, status int, body []byte) {
	c, ok := r.Context().Value(idemKey{}).(*commit)
	if !ok || c.done {
		return
	}
	c.store.FinishIdempotent(c.scope, store.Response{Status: status, Body: body})
	c.done = true
}
