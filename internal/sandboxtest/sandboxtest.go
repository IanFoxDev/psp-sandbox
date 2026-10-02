// Package sandboxtest runs a full sandbox and a callback receiver on local
// test servers, for tests that look at the sandbox the way an application does:
// over HTTP, with signed callbacks.
package sandboxtest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/app"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/signing"
)

// Secret signs callbacks in tests. It matches the shared test vector.
const Secret = "whsec_dGVzdC1zZWNyZXQ="

// Wait is how long helpers wait for something asynchronous before failing.
const Wait = 5 * time.Second

// Config returns settings suited to fast tests: short processing delay and
// short retries, fixed secret and seed.
func Config() config.Config {
	return config.Config{
		Profile:         config.ProfileNative,
		Addr:            ":0",
		WebhookSecret:   Secret,
		DefaultScenario: "happy_path",
		ProcessingDelay: 10 * time.Millisecond,
		RetrySchedule:   []time.Duration{0, 50 * time.Millisecond, 100 * time.Millisecond},
		Seed:            "test",
		LogFormat:       "text",
	}
}

// Sandbox is a running sandbox with a receiver for its callbacks.
type Sandbox struct {
	t        *testing.T
	App      *app.App
	Server   *httptest.Server
	Receiver *Receiver
}

// New starts a sandbox. change, if given, adjusts the config; the default
// callback URL already points at the receiver.
func New(t *testing.T, change ...func(*config.Config)) *Sandbox {
	t.Helper()
	rc := NewReceiver(t)
	cfg := Config()
	cfg.CallbackURL = rc.URL()
	for _, f := range change {
		f(&cfg)
	}
	a, err := app.New(cfg, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(func() {
		srv.Close()
		a.Close()
	})
	return &Sandbox{t: t, App: a, Server: srv, Receiver: rc}
}

// Response is an HTTP answer from the sandbox.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into a map.
func (r Response) JSON(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("response %d is not a JSON object: %s", r.Status, r.Body)
	}
	return m
}

// Do sends a request. body is encoded as JSON unless it is nil. headers are
// name, value pairs.
func (s *Sandbox) Do(method, path string, body any, headers ...string) Response {
	s.t.Helper()
	resp, err := s.do(method, path, body, headers...)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (s *Sandbox) do(method, path string, body any, headers ...string) (Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.Server.URL+path, rd)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := s.Server.Client().Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, err
}

// DoWithTimeout is Do with a client timeout. It returns the error instead of
// failing the test, for scenarios that make the sandbox slow or silent.
func (s *Sandbox) DoWithTimeout(timeout time.Duration, method, path string, body any, headers ...string) (Response, error) {
	s.t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.Server.URL+path, bytes.NewReader(b))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, err := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: rb}, err
}

// CreatePayment creates a payment of 1000 EUR, merged with fields, and
// expects 201. headers are name, value pairs, e.g. scenario.Header, "declined".
func (s *Sandbox) CreatePayment(fields map[string]any, headers ...string) map[string]any {
	s.t.Helper()
	body := map[string]any{"amount": 1000, "currency": "EUR", "reference": "order-1"}
	for k, v := range fields {
		body[k] = v
	}
	resp := s.Do(http.MethodPost, "/v1/payments", body, headers...)
	if resp.Status != http.StatusCreated {
		s.t.Fatalf("create payment: %d %s", resp.Status, resp.Body)
	}
	return resp.JSON(s.t)
}

// Payment fetches a payment and expects 200.
func (s *Sandbox) Payment(id string) map[string]any {
	s.t.Helper()
	resp := s.Do(http.MethodGet, "/v1/payments/"+id, nil)
	if resp.Status != http.StatusOK {
		s.t.Fatalf("get payment %s: %d %s", id, resp.Status, resp.Body)
	}
	return resp.JSON(s.t)
}

// WaitStatus polls a payment until it has the given status.
func (s *Sandbox) WaitStatus(id, status string) map[string]any {
	s.t.Helper()
	deadline := time.Now().Add(Wait)
	for {
		p := s.Payment(id)
		if p["status"] == status {
			return p
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("payment %s is %v, waited for %s", id, p["status"], status)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Callback is one request received by the Receiver.
type Callback struct {
	ID        string
	Type      string
	Data      map[string]any
	Header    http.Header
	Body      []byte
	Received  time.Time
	Signed    bool
	Timestamp int64
}

// Receiver is an application endpoint that records callbacks and checks their
// signatures with Secret.
type Receiver struct {
	t      *testing.T
	server *httptest.Server
	signer *signing.Signer

	mu      sync.Mutex
	got     []Callback
	respond func(c Callback) int
	notify  chan struct{}
}

// NewReceiver starts a receiver that answers 200 to everything.
func NewReceiver(t *testing.T) *Receiver {
	t.Helper()
	signer, err := signing.New(Secret)
	if err != nil {
		t.Fatal(err)
	}
	rc := &Receiver{t: t, signer: signer, notify: make(chan struct{}, 1)}
	rc.server = httptest.NewServer(http.HandlerFunc(rc.serve))
	t.Cleanup(rc.server.Close)
	return rc
}

// URL is where the receiver listens.
func (rc *Receiver) URL() string { return rc.server.URL + "/callback" }

// Respond sets the status code the receiver answers with.
func (rc *Receiver) Respond(f func(c Callback) int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.respond = f
}

func (rc *Receiver) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c := Callback{Header: r.Header, Body: body, Received: time.Now(), ID: r.Header.Get(signing.HeaderID)}
	c.Timestamp, _ = strconv.ParseInt(r.Header.Get(signing.HeaderTimestamp), 10, 64)
	c.Signed = r.Header.Get(signing.HeaderSignature) == rc.signer.Signature(c.ID, c.Timestamp, body)
	var ev struct {
		ID   string         `json:"id"`
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(body, &ev)
	c.Type, c.Data = ev.Type, ev.Data

	rc.mu.Lock()
	respond := rc.respond
	rc.mu.Unlock()
	status := http.StatusOK
	if respond != nil {
		status = respond(c)
	}

	rc.mu.Lock()
	rc.got = append(rc.got, c)
	rc.mu.Unlock()
	select {
	case rc.notify <- struct{}{}:
	default:
	}
	w.WriteHeader(status)
}

// All returns the callbacks received so far.
func (rc *Receiver) All() []Callback {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]Callback(nil), rc.got...)
}

// Wait blocks until at least n callbacks have arrived and returns all of them.
func (rc *Receiver) Wait(n int) []Callback {
	rc.t.Helper()
	deadline := time.After(Wait)
	for {
		if got := rc.All(); len(got) >= n {
			return got
		}
		select {
		case <-rc.notify:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			rc.t.Fatalf("waited for %d callbacks, got %d", n, len(rc.All()))
		}
	}
}

// Quiet fails the test if more than n callbacks arrive within d.
func (rc *Receiver) Quiet(n int, d time.Duration) {
	rc.t.Helper()
	time.Sleep(d)
	if got := rc.All(); len(got) > n {
		rc.t.Fatalf("got %d callbacks, want %d", len(got), n)
	}
}
