package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadJSONMessages(t *testing.T) {
	type request struct {
		Amount   int64             `json:"amount"`
		Seconds  float64           `json:"seconds"`
		Currency string            `json:"currency"`
		Capture  *int64            `json:"capture"`
		Metadata map[string]string `json:"metadata"`
	}
	cases := map[string]string{
		`{"amount": 1.5}`:             "invalid JSON body: amount must be an integer",
		`{"seconds": "10"}`:           "invalid JSON body: seconds must be a number",
		`{"currency": 1}`:             "invalid JSON body: currency must be a string",
		`{"capture": "all"}`:          "invalid JSON body: capture must be an integer",
		`{"metadata": []}`:            "invalid JSON body: metadata must be an object",
		`[1, 2]`:                      "invalid JSON body: body must be a JSON object",
		`{"amout": 1}`:                `invalid JSON body: unknown field "amout"`,
		`{"amount": 1,}`:              "invalid JSON body: syntax error at byte 14",
		`{"amount": 1`:                "invalid JSON body: body ends too early",
		`{"amount": 1} {"amount": 2}`: "invalid JSON body: unexpected data after the object",
	}
	for body, want := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var v request
		if ReadJSON(w, r, &v, false) {
			t.Errorf("%s: accepted", body)
			continue
		}
		var got Error
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusBadRequest || got.Error.Message != want {
			t.Errorf("%s: %d %q, want %q", body, w.Code, got.Error.Message, want)
		}
	}
}

func TestReadJSONEmptyBody(t *testing.T) {
	var v struct{}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(" "))
	if !ReadJSON(httptest.NewRecorder(), r, &v, true) {
		t.Error("empty body rejected with allowEmpty")
	}
	w := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	if ReadJSON(w, r, &v, false) || !strings.Contains(w.Body.String(), "body is empty") {
		t.Errorf("empty body: %s", w.Body)
	}
}
