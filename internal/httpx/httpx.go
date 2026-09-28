// Package httpx holds the JSON and error helpers shared by the provider API
// and the control API, so both answer errors in the same shape.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

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
	case errors.Is(err, payment.ErrInvalidAmount), errors.Is(err, engine.ErrInvalidScenario),
		errors.Is(err, engine.ErrUnsupportedEvent):
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
		WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+jsonProblem(err))
		return false
	}
	if dec.More() {
		WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: unexpected data after the object")
		return false
	}
	return true
}

// jsonProblem describes a decoding error in terms of the request, without Go
// type names such as "createRequest.amount of type int64".
func jsonProblem(err error) string {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.Is(err, io.EOF):
		return "body is empty"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "body ends too early"
	case errors.As(err, &syntaxErr):
		return fmt.Sprintf("syntax error at byte %d", syntaxErr.Offset)
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			return "body must be a JSON object"
		}
		return fmt.Sprintf("%s must be %s", typeErr.Field, jsonKind(typeErr.Type))
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return "unknown field " + name
	}
	return err.Error()
}

func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "an integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Map, reflect.Struct:
		return "an object"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Pointer:
		return jsonKind(t.Elem())
	}
	return "of another type"
}

// Routes wraps mux so that unknown paths and wrong methods get the usual JSON
// error instead of the plain text answers of http.ServeMux.
func Routes(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		probe := &statusProbe{header: http.Header{}}
		h.ServeHTTP(probe, r)
		switch probe.status {
		case http.StatusNotFound:
			WriteError(w, http.StatusNotFound, "not_found", "no endpoint "+r.URL.Path)
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", probe.header.Get("Allow"))
			WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed",
				r.Method+" is not allowed on "+r.URL.Path+", use "+probe.header.Get("Allow"))
		default:
			// Redirects to the canonical path and the like.
			mux.ServeHTTP(w, r)
		}
	})
}

// statusProbe records the status and headers a handler would write.
type statusProbe struct {
	header http.Header
	status int
}

func (p *statusProbe) Header() http.Header         { return p.header }
func (p *statusProbe) Write(b []byte) (int, error) { return len(b), nil }
func (p *statusProbe) WriteHeader(status int)      { p.status = status }
