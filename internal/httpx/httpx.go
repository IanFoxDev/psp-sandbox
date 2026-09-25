// Package httpx holds the JSON and error helpers shared by the provider API
// and the control API, so both answer errors in the same shape.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// MaxBody is the largest request body the sandbox reads.
const MaxBody = 1 << 20

// Error is the body of every error response.
type Error struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// WriteError writes {"error": {"code": ..., "message": ...}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	var body Error
	body.Error.Code = code
	body.Error.Message = message
	WriteJSON(w, status, body)
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteDomainError maps engine, payment, store and callback errors to API errors.
func WriteDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "payment not found")
	case errors.Is(err, callback.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "delivery not found")
	case errors.Is(err, payment.ErrInvalidState):
		WriteError(w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, payment.ErrAmountExceedsCaptured):
		WriteError(w, http.StatusUnprocessableEntity, "amount_exceeds_captured", err.Error())
	case errors.Is(err, payment.ErrInvalidAmount), errors.Is(err, engine.ErrInvalidScenario):
		WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

// ReadJSON decodes the body into v, rejecting unknown fields. An empty body
// leaves v unchanged when allowEmpty is true. On failure it writes a 400 and
// returns false.
func ReadJSON(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "cannot read body: "+err.Error())
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 && allowEmpty {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
