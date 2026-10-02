package stripe

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Error types, as in the "type" field of a Stripe error.
const (
	TypeInvalidRequest = "invalid_request_error"
	TypeCard           = "card_error"
	TypeIdempotency    = "idempotency_error"
	TypeAPI            = "api_error"
)

// Error is the "error" object of a Stripe error response. Empty fields are
// left out, as Stripe does.
type Error struct {
	Type          string `json:"type"`
	Code          string `json:"code,omitempty"`
	DeclineCode   string `json:"decline_code,omitempty"`
	Message       string `json:"message,omitempty"`
	Param         string `json:"param,omitempty"`
	Charge        string `json:"charge,omitempty"`
	PaymentIntent any    `json:"payment_intent,omitempty"`
}

type errorBody struct {
	Error Error `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, e Error) {
	writeJSON(w, status, errorBody{Error: e})
}

func invalidRequest(w http.ResponseWriter, status int, code, param, message string) {
	writeError(w, status, Error{Type: TypeInvalidRequest, Code: code, Param: param, Message: message})
}

// writeParamError answers a ParamError with 400, or any other error with 500.
func writeParamError(w http.ResponseWriter, err error) {
	var pe *ParamError
	if errors.As(err, &pe) {
		invalidRequest(w, http.StatusBadRequest, pe.Code, pe.Param, pe.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, Error{Type: TypeAPI, Message: err.Error()})
}
