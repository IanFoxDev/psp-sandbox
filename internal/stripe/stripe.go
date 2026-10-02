// Package stripe serves the Stripe-compatible profile (PSP_PROFILE=stripe):
// the part of Stripe's v1 API that ADR 0005 lists, over the same engine as
// the native API. Requests are form-encoded, errors and idempotency follow
// Stripe, and keys are Stripe test keys.
package stripe

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/httpx"
	"github.com/ianfoxdev/psp-sandbox/internal/ids"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// Header names.
const (
	HeaderRequestID      = "Request-Id"
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderReplayed       = "Idempotent-Replayed"
)

// Options configure the API.
type Options struct {
	// APIKey, if set, is the only key accepted. Otherwise any test key is.
	APIKey string
	// Seed makes request ids reproducible, as PSP_SEED does for other ids.
	Seed string
	// ManualClock is PSP_CLOCK=manual: confirm answers without waiting for
	// the outcome, since it only comes when the test moves time.
	ManualClock bool
	Log         *slog.Logger
}

// API serves the Stripe routes under /v1.
type API struct {
	engine *engine.Engine
	store  *store.Store
	ids    *ids.Generator
	opts   Options

	// warned holds "endpoint param" pairs already logged as ignored.
	warned sync.Map
}

// New returns the Stripe-compatible API. Idempotency keys are kept in st.
func New(e *engine.Engine, st *store.Store, opts Options) *API {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &API{engine: e, store: st, ids: ids.NewStream(opts.Seed, "stripe-requests"), opts: opts}
}

// Register adds the /v1 routes to mux. Paths that Stripe has and the sandbox
// does not answer 404, as an unknown URL does on Stripe.
func (a *API) Register(mux *http.ServeMux) {
	a.registerIntents(mux)
	a.registerRefunds(mux)
	a.registerEvents(mux)
	mux.Handle("/v1/", a.chain(http.HandlerFunc(a.unrecognized)))
}

// chain wraps every Stripe route: a request id first, so that even a refused
// key gets one, then the key check.
func (a *API) chain(h http.Handler) http.Handler {
	return a.requestID(a.auth(h))
}

func (a *API) unrecognized(w http.ResponseWriter, r *http.Request) {
	invalidRequest(w, http.StatusNotFound, "", "",
		"Unrecognized request URL ("+r.Method+": "+r.URL.Path+"). psp-sandbox serves the part of the Stripe API "+
			"listed in docs/stripe.md.")
}

type requestIDKey struct{}

func (a *API) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := a.ids.Next("req")
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestID returns the id of the request that ctx belongs to.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := apiKey(r)
		if msg := a.checkKey(key); msg != "" {
			invalidRequest(w, http.StatusUnauthorized, "", "", msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiKey reads the key from a bearer token or, as curl -u sends it, from the
// user of basic auth.
func apiKey(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if key, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(key)
	}
	if enc, ok := strings.CutPrefix(h, "Basic "); ok {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
		if err != nil {
			return ""
		}
		user, _, _ := strings.Cut(string(raw), ":")
		return user
	}
	return ""
}

// checkKey returns why the key is refused, or "" if it is accepted.
func (a *API) checkKey(key string) string {
	switch {
	case key == "":
		return "You did not provide an API key. Send it as a bearer token (Authorization: Bearer sk_test_...) " +
			"or as the user in basic auth."
	case a.opts.APIKey != "":
		if subtle.ConstantTimeCompare([]byte(key), []byte(a.opts.APIKey)) != 1 {
			return "Invalid API Key provided: " + mask(key) + ". This sandbox takes only the key in PSP_API_KEY."
		}
		return ""
	case strings.HasPrefix(key, "sk_test_"), strings.HasPrefix(key, "rk_test_"):
		return ""
	case strings.HasPrefix(key, "sk_live_"), strings.HasPrefix(key, "rk_live_"):
		return "Live API key provided: " + mask(key) + ". psp-sandbox takes test keys only (sk_test_...); " +
			"a live key in a test setup is a mistake worth fixing."
	case strings.HasPrefix(key, "pk_"):
		return "Publishable API key provided: " + mask(key) + ". This call needs a secret test key (sk_test_...)."
	}
	return "Invalid API Key provided: " + mask(key) + ". Use a Stripe test key such as sk_test_123."
}

// mask keeps the prefix and the last four characters, as Stripe does.
func mask(key string) string {
	prefix := ""
	if i := strings.LastIndexByte(key, '_'); i >= 0 && i < len(key)-1 {
		prefix = key[:i+1]
	}
	rest := key[len(prefix):]
	if len(rest) <= 4 {
		return prefix + strings.Repeat("*", len(rest))
	}
	return prefix + strings.Repeat("*", len(rest)-4) + rest[len(rest)-4:]
}

// readParams decodes the query string of a GET or the form body of other
// methods. On failure it writes a 400 and returns false.
func readParams(w http.ResponseWriter, r *http.Request) (*Params, bool) {
	raw := r.URL.RawQuery
	if r.Method != http.MethodGet {
		if ct := r.Header.Get("Content-Type"); ct != "" {
			mt, _, _ := mime.ParseMediaType(ct)
			if mt != "application/x-www-form-urlencoded" {
				invalidRequest(w, http.StatusBadRequest, "", "",
					"Invalid request: the v1 API takes application/x-www-form-urlencoded bodies, not "+mt+".")
				return nil, false
			}
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBody))
		if err != nil {
			invalidRequest(w, http.StatusBadRequest, "", "", "Cannot read the request body: "+err.Error())
			return nil, false
		}
		raw = string(body)
	}
	p, err := ParseParams(raw)
	if err != nil {
		writeParamError(w, err)
		return nil, false
	}
	return p, true
}

// warnUnread logs parameters the endpoint accepts but does not model, once
// per endpoint and name.
func (a *API) warnUnread(endpoint string, p *Params) {
	for _, name := range p.Unread() {
		if _, seen := a.warned.LoadOrStore(endpoint+" "+name, true); !seen {
			a.opts.Log.Warn("Stripe parameter ignored: the sandbox does not model it", "endpoint", endpoint, "param", name)
		}
	}
}

type idemKey struct{}

// idempotent gives a POST handler Stripe's idempotency: the same key with the
// same request gets the stored answer and Idempotent-Replayed: true; with a
// different request (another endpoint or other parameters) 400; while the
// first request runs 409. Keys are not tied to an endpoint, as on Stripe.
//
// Only answers the handler commits are stored. Handlers commit 2xx and 402
// answers; validation errors and refused calls are left out, so a retry with
// the same key runs again.
func (a *API) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(HeaderIdempotencyKey)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBody))
		if err != nil {
			invalidRequest(w, http.StatusBadRequest, "", "", "Cannot read the request body: "+err.Error())
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		scope := "stripe " + key
		fp := fingerprint(r.Method, r.URL.Path, string(body), r.Header.Get(scenario.Header))
		stored, err := a.store.BeginIdempotent(scope, fp)
		switch {
		case errors.Is(err, store.ErrIdempotencyConflict):
			writeError(w, http.StatusBadRequest, Error{Type: TypeIdempotency, Message: "Keys for idempotent " +
				"requests can only be used with the same parameters they were first used with. Try using a key " +
				"other than '" + key + "' if you meant to execute a different request."})
			return
		case errors.Is(err, store.ErrIdempotencyInProgress):
			writeError(w, http.StatusConflict, Error{Type: TypeIdempotency, Message: "There is currently another " +
				"in-progress request using this Idempotency-Key '" + key + "'. Retry once it has finished."})
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

func fingerprint(parts ...string) string {
	b, _ := json.Marshal(parts)
	return string(b)
}

type commit struct {
	store *store.Store
	scope string
	done  bool
}

// commitResponse stores the answer for the request's Idempotency-Key, if any.
// paymentID is the payment the answer is about.
func commitResponse(r *http.Request, status int, body []byte, paymentID string) {
	c, ok := r.Context().Value(idemKey{}).(*commit)
	if !ok || c.done {
		return
	}
	c.store.FinishIdempotent(c.scope, store.Response{Status: status, Body: body}, paymentID)
	c.done = true
}

// respond writes v as JSON and, for 2xx and 402, stores it under the
// request's Idempotency-Key.
func respond(w http.ResponseWriter, r *http.Request, status int, v any, paymentID string) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, Error{Type: TypeAPI, Message: err.Error()})
		return
	}
	body = append(body, '\n')
	if status/100 == 2 || status == http.StatusPaymentRequired {
		commitResponse(r, status, body, paymentID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
