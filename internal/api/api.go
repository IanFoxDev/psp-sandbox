// Package api implements the provider API under /v1: payments, captures,
// cancellations and refunds, with Idempotency-Key handling.
package api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// maxBody is the largest request body the API reads.
const maxBody = 1 << 20

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
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or wrong API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiError is the body of every error response.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeDomainError maps engine, payment and store errors to API errors.
func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "payment not found")
	case errors.Is(err, payment.ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, payment.ErrAmountExceedsCaptured):
		writeError(w, http.StatusUnprocessableEntity, "amount_exceeds_captured", err.Error())
	case errors.Is(err, payment.ErrInvalidAmount), errors.Is(err, engine.ErrInvalidScenario):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

// readJSON decodes the body into v. An empty body leaves v unchanged when
// allowEmpty is true.
func readJSON(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "cannot read body: "+err.Error())
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 && allowEmpty {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func fingerprint(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
