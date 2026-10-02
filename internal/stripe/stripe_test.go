package stripe

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

// testAPI serves the real middleware around a test endpoint: POST /v1/test
// answers with the status in the "status" parameter and counts its runs.
type testAPI struct {
	*API
	srv     *httptest.Server
	runs    atomic.Int32
	release chan struct{}
}

func newTestAPI(t *testing.T, opts Options) *testAPI {
	t.Helper()
	ta := &testAPI{API: New(nil, store.New(), opts)}
	mux := http.NewServeMux()
	ta.Register(mux)
	mux.Handle("POST /v1/test", ta.chain(ta.idempotent(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ta.runs.Add(1)
		if ta.release != nil {
			<-ta.release
		}
		p, ok := readParams(w, r)
		if !ok {
			return
		}
		status, _, err := p.Int("status")
		if err != nil {
			writeParamError(w, err)
			return
		}
		ta.warnUnread("POST /v1/test", p)
		if status >= 500 {
			writeError(w, int(status), Error{Type: TypeAPI, Message: "refused"})
			return
		}
		respond(w, r, int(status), map[string]any{"run": ta.runs.Load(), "request": RequestID(r.Context())}, "")
	}))))
	ta.srv = httptest.NewServer(mux)
	t.Cleanup(ta.srv.Close)
	return ta
}

type reply struct {
	status int
	header http.Header
	body   map[string]any
}

func (ta *testAPI) do(t *testing.T, method, path, body string, header ...string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, ta.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sk_test_123")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(header); i += 2 {
		if header[i+1] == "" {
			req.Header.Del(header[i])
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := reply{status: resp.StatusCode, header: resp.Header}
	if err := json.Unmarshal(raw, &out.body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON", method, path, raw)
	}
	return out
}

func errorOf(r reply) map[string]any {
	e, _ := r.body["error"].(map[string]any)
	return e
}

func TestKeys(t *testing.T) {
	ta := newTestAPI(t, Options{})
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("sk_test_curl:"))
	cases := []struct {
		auth   string
		status int
		says   string
	}{
		{"Bearer sk_test_123", 200, ""},
		{"Bearer rk_test_123", 200, ""},
		{basic, 200, ""},
		{"", 401, "did not provide an API key"},
		{"Bearer sk_live_abcdef1234", 401, "sk_live_******1234"},
		{"Bearer rk_live_abcdef1234", 401, "test keys only"},
		{"Bearer pk_test_abcdef1234", 401, "Publishable"},
		{"Bearer whatever", 401, "Invalid API Key"},
	}
	for _, c := range cases {
		r := ta.do(t, "POST", "/v1/test", "status=200", "Authorization", c.auth)
		if r.status != c.status {
			t.Errorf("%q: status %d", c.auth, r.status)
			continue
		}
		if c.status == 401 {
			e := errorOf(r)
			if e["type"] != TypeInvalidRequest || !strings.Contains(e["message"].(string), c.says) {
				t.Errorf("%q: error %v", c.auth, e)
			}
		}
		if !strings.HasPrefix(r.header.Get(HeaderRequestID), "req_") {
			t.Errorf("%q: Request-Id %q", c.auth, r.header.Get(HeaderRequestID))
		}
	}
}

func TestOnlyConfiguredKey(t *testing.T) {
	ta := newTestAPI(t, Options{APIKey: "sk_test_mine"})
	if r := ta.do(t, "POST", "/v1/test", "status=200", "Authorization", "Bearer sk_test_mine"); r.status != 200 {
		t.Errorf("configured key: %d", r.status)
	}
	if r := ta.do(t, "POST", "/v1/test", "status=200", "Authorization", "Bearer sk_test_other"); r.status != 401 {
		t.Errorf("other test key: %d", r.status)
	}
}

func TestRequestIDsAreUniqueAndReproducible(t *testing.T) {
	a := newTestAPI(t, Options{Seed: "s"})
	b := newTestAPI(t, Options{Seed: "s"})
	first := a.do(t, "POST", "/v1/test", "status=200")
	second := a.do(t, "POST", "/v1/test", "status=200")
	id := first.header.Get(HeaderRequestID)
	if id == second.header.Get(HeaderRequestID) {
		t.Errorf("two requests share %s", id)
	}
	if first.body["request"] != id {
		t.Errorf("handler saw %v, header says %s", first.body["request"], id)
	}
	if other := b.do(t, "POST", "/v1/test", "status=200").header.Get(HeaderRequestID); other != id {
		t.Errorf("same seed gave %s and %s", id, other)
	}
}

func TestUnrecognizedURL(t *testing.T) {
	ta := newTestAPI(t, Options{})
	for _, path := range []string{"/v1/payments", "/v1/customers", "/v1/test/extra"} {
		r := ta.do(t, "GET", path, "")
		if r.status != 404 || errorOf(r)["type"] != TypeInvalidRequest ||
			!strings.Contains(errorOf(r)["message"].(string), "GET: "+path) {
			t.Errorf("%s: %d %v", path, r.status, r.body)
		}
	}
	if r := ta.do(t, "GET", "/v1/customers", "", "Authorization", ""); r.status != 401 {
		t.Errorf("unknown URL without a key: %d, the key is checked first", r.status)
	}
}

func TestJSONBodyRejected(t *testing.T) {
	ta := newTestAPI(t, Options{})
	r := ta.do(t, "POST", "/v1/test", `{"status":200}`, "Content-Type", "application/json")
	if r.status != 400 || !strings.Contains(errorOf(r)["message"].(string), "x-www-form-urlencoded") {
		t.Errorf("got %d %v", r.status, r.body)
	}
}

func TestParamErrorShape(t *testing.T) {
	ta := newTestAPI(t, Options{})
	r := ta.do(t, "POST", "/v1/test", "status=ok")
	e := errorOf(r)
	if r.status != 400 || e["type"] != TypeInvalidRequest || e["code"] != "parameter_invalid_integer" || e["param"] != "status" {
		t.Errorf("got %d %v", r.status, e)
	}
	if _, ok := e["decline_code"]; ok {
		t.Errorf("empty fields must be left out: %v", e)
	}
}

func TestIdempotentReplay(t *testing.T) {
	ta := newTestAPI(t, Options{})
	first := ta.do(t, "POST", "/v1/test", "status=200", HeaderIdempotencyKey, "k1")
	again := ta.do(t, "POST", "/v1/test", "status=200", HeaderIdempotencyKey, "k1")
	if ta.runs.Load() != 1 || again.body["run"] != first.body["run"] || again.header.Get(HeaderReplayed) != "true" {
		t.Errorf("runs %d, first %v, again %v replayed %q", ta.runs.Load(), first.body, again.body, again.header.Get(HeaderReplayed))
	}
	if first.header.Get(HeaderReplayed) != "" {
		t.Error("first answer marked as replayed")
	}
	if again.header.Get(HeaderRequestID) == first.header.Get(HeaderRequestID) {
		t.Error("a replay is a new request and gets its own Request-Id")
	}
}

func TestIdempotentDeclineIsStored(t *testing.T) {
	ta := newTestAPI(t, Options{})
	ta.do(t, "POST", "/v1/test", "status=402", HeaderIdempotencyKey, "k1")
	if r := ta.do(t, "POST", "/v1/test", "status=402", HeaderIdempotencyKey, "k1"); r.status != 402 || ta.runs.Load() != 1 {
		t.Errorf("status %d after %d runs", r.status, ta.runs.Load())
	}
}

func TestIdempotentErrorsAreNotStored(t *testing.T) {
	for _, body := range []string{"status=nope", "status=503"} {
		ta := newTestAPI(t, Options{})
		ta.do(t, "POST", "/v1/test", body, HeaderIdempotencyKey, "k1")
		ta.do(t, "POST", "/v1/test", body, HeaderIdempotencyKey, "k1")
		if ta.runs.Load() != 2 {
			t.Errorf("%s: %d runs, a retry with the same key must run again", body, ta.runs.Load())
		}
	}
}

func TestIdempotencyKeyWithOtherRequest(t *testing.T) {
	ta := newTestAPI(t, Options{})
	ta.do(t, "POST", "/v1/test", "status=200", HeaderIdempotencyKey, "k1")
	for _, c := range []struct{ path, body string }{
		{"/v1/test", "status=201"},
		{"/v1/test?x=1", "status=200"},
	} {
		r := ta.do(t, "POST", c.path, c.body, HeaderIdempotencyKey, "k1")
		if c.path == "/v1/test?x=1" {
			// The query string of a POST is not a parameter: same request.
			if r.status != 200 {
				t.Errorf("query on POST: %d", r.status)
			}
			continue
		}
		if r.status != 400 || errorOf(r)["type"] != TypeIdempotency {
			t.Errorf("%s %s: %d %v", c.path, c.body, r.status, r.body)
		}
	}
}

func TestIdempotencyKeyInProgress(t *testing.T) {
	ta := newTestAPI(t, Options{})
	ta.release = make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	var first reply
	go func() {
		defer wg.Done()
		first = ta.do(t, "POST", "/v1/test", "status=200", HeaderIdempotencyKey, "k1")
	}()
	for ta.runs.Load() == 0 {
		// wait until the first request holds the key
		runtime.Gosched()
	}
	r := ta.do(t, "POST", "/v1/test", "status=200", HeaderIdempotencyKey, "k1")
	close(ta.release)
	wg.Wait()
	if r.status != 409 || errorOf(r)["type"] != TypeIdempotency {
		t.Errorf("concurrent: %d %v", r.status, r.body)
	}
	if first.status != 200 {
		t.Errorf("first: %d", first.status)
	}
}

func TestMask(t *testing.T) {
	for in, want := range map[string]string{
		"sk_live_abcdef1234": "sk_live_******1234",
		"sk_test_12":         "sk_test_**",
		"whatever123":        "*******r123",
	} {
		if got := mask(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}
