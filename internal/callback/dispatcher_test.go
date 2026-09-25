package callback

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghuser/psp-sandbox/internal/clock"
	"github.com/ghuser/psp-sandbox/internal/ids"
	"github.com/ghuser/psp-sandbox/internal/payment"
	"github.com/ghuser/psp-sandbox/internal/signing"
)

const secret = "whsec_dGVzdC1zZWNyZXQ="

var start = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

// receiver records callbacks and answers with the status chosen by respond.
type receiver struct {
	*httptest.Server
	mu      sync.Mutex
	got     []received
	respond func(n int, r *http.Request) int
	signal  chan struct{}
}

type received struct {
	eventID string
	status  int
	header  http.Header
	body    []byte
}

func newReceiver(t *testing.T, respond func(n int, r *http.Request) int) *receiver {
	t.Helper()
	rc := &receiver{respond: respond, signal: make(chan struct{}, 100)}
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		n := len(rc.got)
		rc.mu.Unlock()
		status := http.StatusOK
		if rc.respond != nil {
			status = rc.respond(n, r)
		}
		rc.mu.Lock()
		rc.got = append(rc.got, received{eventID: r.Header.Get("webhook-id"), status: status, header: r.Header, body: body})
		rc.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte("ack"))
		rc.signal <- struct{}{}
	}))
	t.Cleanup(rc.Close)
	return rc
}

func (rc *receiver) count() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return len(rc.got)
}

func (rc *receiver) wait(t *testing.T, n int) []received {
	t.Helper()
	for {
		rc.mu.Lock()
		if len(rc.got) >= n {
			out := append([]received(nil), rc.got...)
			rc.mu.Unlock()
			return out
		}
		rc.mu.Unlock()
		select {
		case <-rc.signal:
		case <-time.After(3 * time.Second):
			t.Fatalf("waited for %d callbacks, got %d", n, rc.count())
		}
	}
}

func newDispatcher(t *testing.T, c clock.Clock, retry ...time.Duration) *Dispatcher {
	t.Helper()
	s, err := signing.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	d := New(Options{Clock: c, Signer: s, IDs: ids.New("test"), Retry: retry, Rand: ids.Rand("test", "jitter")})
	t.Cleanup(d.Close)
	return d
}

func event(id, paymentID string) payment.Event {
	return payment.Event{ID: id, Type: payment.EventPaymentCaptured, CreatedAt: start, PaymentID: paymentID,
		Data: map[string]string{"id": paymentID}}
}

func TestSignedDelivery(t *testing.T) {
	rc := newReceiver(t, nil)
	d := newDispatcher(t, clock.Real{})

	<-d.Send(event("evt_1", "pay_1"), rc.URL, Plan{})

	got := rc.wait(t, 1)[0]
	signer, _ := signing.New(secret)
	ts := got.header.Get("webhook-timestamp")
	var unix int64
	if err := json.Unmarshal([]byte(ts), &unix); err != nil {
		t.Fatalf("timestamp %q: %v", ts, err)
	}
	if want := signer.Signature("evt_1", unix, got.body); got.header.Get("webhook-signature") != want {
		t.Fatalf("signature %q, want %q", got.header.Get("webhook-signature"), want)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "evt_1" || body["type"] != "payment.captured" {
		t.Fatalf("body = %s", got.body)
	}

	dels := waitStatus(t, d, "pay_1", StatusSucceeded)
	if len(dels[0].Attempts) != 1 || dels[0].Attempts[0].StatusCode != 200 || dels[0].Attempts[0].ResponseBody != "ack" {
		t.Fatalf("delivery = %+v", dels[0])
	}
}

func TestRetriesOnScheduleWithManualClock(t *testing.T) {
	rc := newReceiver(t, func(n int, _ *http.Request) int {
		if n < 2 {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	})
	m := clock.NewManual(start)
	d := newDispatcher(t, m, 0, 5*time.Second, 30*time.Second, time.Hour)

	d.Send(event("evt_1", "pay_1"), rc.URL, Plan{})
	rc.wait(t, 1)

	waitTimers(t, m, 1)
	m.Advance(4 * time.Second)
	if rc.count() != 1 {
		t.Fatal("retried before the schedule")
	}
	m.Advance(2 * time.Second) // 6s: past 5s plus at most 10% jitter
	rc.wait(t, 2)

	waitTimers(t, m, 1)
	m.Advance(34 * time.Second)
	rc.wait(t, 3)

	dels := waitStatus(t, d, "pay_1", StatusSucceeded)
	if n := len(dels[0].Attempts); n != 3 {
		t.Fatalf("attempts = %d, want 3", n)
	}
	if m.Pending() != 0 {
		t.Fatal("a retry is still scheduled after success")
	}
}

func TestFailedAfterLastAttempt(t *testing.T) {
	rc := newReceiver(t, func(int, *http.Request) int { return http.StatusServiceUnavailable })
	d := newDispatcher(t, clock.Real{}, 0, time.Millisecond)

	d.Send(event("evt_1", "pay_1"), rc.URL, Plan{})
	dels := waitStatus(t, d, "pay_1", StatusFailed)
	if len(dels[0].Attempts) != 2 || dels[0].Attempts[1].Error == "" {
		t.Fatalf("delivery = %+v", dels[0])
	}
}

// A failing event blocks later events of the same payment, not of other payments.
func TestOrderPerPayment(t *testing.T) {
	var failedOnce atomic.Bool
	rc := newReceiver(t, func(_ int, r *http.Request) int {
		if r.Header.Get("webhook-id") == "evt_a1" && !failedOnce.Swap(true) {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	})
	m := clock.NewManual(start)
	d := newDispatcher(t, m, 0, time.Minute)

	d.Send(event("evt_a1", "pay_a"), rc.URL, Plan{})
	d.Send(event("evt_a2", "pay_a"), rc.URL, Plan{})
	d.Send(event("evt_b1", "pay_b"), rc.URL, Plan{})

	got := rc.wait(t, 2)
	ids := []string{got[0].eventID, got[1].eventID}
	if !(contains(ids, "evt_a1") && contains(ids, "evt_b1")) {
		t.Fatalf("before retry got %v, want evt_a1 and evt_b1", ids)
	}

	waitTimers(t, m, 1)
	m.Advance(2 * time.Minute)
	got = rc.wait(t, 4)
	if got[2].eventID != "evt_a1" || got[3].eventID != "evt_a2" {
		t.Fatalf("after retry got %s then %s, want evt_a1 then evt_a2", got[2].eventID, got[3].eventID)
	}
}

func TestSequentialCopies(t *testing.T) {
	rc := newReceiver(t, nil)
	d := newDispatcher(t, clock.Real{})

	d.Send(event("evt_1", "pay_1"), rc.URL, Plan{Copies: 3})
	got := rc.wait(t, 3)
	for _, g := range got {
		if g.eventID != "evt_1" {
			t.Fatalf("copy has event id %s", g.eventID)
		}
	}
	dels := waitStatus(t, d, "pay_1", StatusSucceeded)
	if len(dels) != 3 || dels[2].Copy != 3 {
		t.Fatalf("deliveries = %+v", dels)
	}
}

// With Parallel, all copies are in flight at the same time.
func TestParallelCopies(t *testing.T) {
	const copies = 4
	var inFlight, peak, count atomic.Int32
	arrived := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if count.Add(1) == copies {
			close(arrived)
		}
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
		}
		inFlight.Add(-1)
	}))
	t.Cleanup(srv.Close)

	d := newDispatcher(t, clock.Real{})
	d.Send(event("evt_1", "pay_1"), srv.URL, Plan{Copies: copies, Parallel: true})
	waitStatus(t, d, "pay_1", StatusSucceeded)
	if peak.Load() != copies {
		t.Fatalf("peak concurrency %d, want %d", peak.Load(), copies)
	}
}

func TestDelay(t *testing.T) {
	rc := newReceiver(t, nil)
	m := clock.NewManual(start)
	d := newDispatcher(t, m)

	d.Send(event("evt_1", "pay_1"), rc.URL, Plan{Delay: 10 * time.Second})
	waitTimers(t, m, 1)
	m.Advance(9 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if rc.count() != 0 {
		t.Fatal("delivered before the delay")
	}
	m.Advance(time.Second)
	rc.wait(t, 1)
}

func TestDrop(t *testing.T) {
	rc := newReceiver(t, nil)
	d := newDispatcher(t, clock.Real{})

	<-d.Send(event("evt_1", "pay_1"), rc.URL, Plan{Drop: true})
	time.Sleep(20 * time.Millisecond)
	if rc.count() != 0 {
		t.Fatal("dropped event was sent")
	}
	dels := d.Deliveries("pay_1")
	if len(dels) != 1 || dels[0].Status != StatusDropped {
		t.Fatalf("deliveries = %+v", dels)
	}
}

func TestResetStopsPendingRetries(t *testing.T) {
	rc := newReceiver(t, func(int, *http.Request) int { return http.StatusInternalServerError })
	m := clock.NewManual(start)
	d := newDispatcher(t, m, 0, time.Minute)

	d.Send(event("evt_1", "pay_1"), rc.URL, Plan{})
	rc.wait(t, 1)
	waitTimers(t, m, 1)

	d.Reset()
	if len(d.Deliveries("pay_1")) != 0 {
		t.Fatal("deliveries survived Reset")
	}
	m.Advance(time.Hour)
	time.Sleep(20 * time.Millisecond)
	if rc.count() != 1 {
		t.Fatal("retry ran after Reset")
	}
}

func TestUnreachableReceiver(t *testing.T) {
	d := newDispatcher(t, clock.Real{})
	d.Send(event("evt_1", "pay_1"), "http://127.0.0.1:1/callback", Plan{})
	dels := waitStatus(t, d, "pay_1", StatusFailed)
	if dels[0].Attempts[0].Error == "" {
		t.Fatal("attempt has no error")
	}
}

func waitStatus(t *testing.T, d *Dispatcher, paymentID string, s Status) []Delivery {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		dels := d.Deliveries(paymentID)
		done := len(dels) > 0
		for _, del := range dels {
			if del.Status != s {
				done = false
			}
		}
		if done {
			return dels
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries of %s never reached %s: %+v", paymentID, s, dels)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitTimers(t *testing.T, m *clock.Manual, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for m.Pending() < n {
		if time.Now().After(deadline) {
			t.Fatalf("pending timers = %d, want %d", m.Pending(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
