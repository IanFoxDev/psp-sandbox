// Package api implements the provider API under /v1: payments, captures,
// cancellations and refunds, with Idempotency-Key handling.
package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/httpx"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// Options configure the API.
type Options struct {
	// APIKey, if set, is the only bearer key accepted.
	APIKey string
	Log    *slog.Logger
}

// API serves /v1.
type API struct {
	engine *engine.Engine
	store  *store.Store
	opts   Options
}

// New returns the provider API. Idempotency keys are kept in st.
func New(e *engine.Engine, st *store.Store, opts Options) *API {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &API{engine: e, store: st, opts: opts}
}

// Register adds the /v1 routes to mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/payments", a.auth(a.idempotent(http.HandlerFunc(a.createPayment))))
	mux.Handle("GET /v1/payments", a.auth(http.HandlerFunc(a.listPayments)))
	mux.Handle("GET /v1/payments/{id}", a.auth(http.HandlerFunc(a.getPayment)))
	mux.Handle("POST /v1/payments/{id}/capture", a.auth(http.HandlerFunc(a.capture)))
	mux.Handle("POST /v1/payments/{id}/cancel", a.auth(http.HandlerFunc(a.cancel)))
	mux.Handle("POST /v1/payments/{id}/refunds", a.auth(a.idempotent(http.HandlerFunc(a.refund))))
}

func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.opts.APIKey != "" && (!ok || subtle.ConstantTimeCompare([]byte(key), []byte(a.opts.APIKey)) != 1) {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or wrong API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func fingerprint(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
