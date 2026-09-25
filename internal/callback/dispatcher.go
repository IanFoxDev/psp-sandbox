// Package callback delivers events to the application: an ordered queue per
// payment, retries on the configured schedule, and a log of every attempt.
package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/ghuser/psp-sandbox/internal/clock"
	"github.com/ghuser/psp-sandbox/internal/ids"
	"github.com/ghuser/psp-sandbox/internal/payment"
	"github.com/ghuser/psp-sandbox/internal/signing"
)

// ErrNotFound means there is no delivery with this id.
var ErrNotFound = errors.New("delivery not found")

const (
	// Timeout is how long a receiver has to answer one attempt.
	Timeout = 10 * time.Second
	// maxResponseBody is how much of the receiver's answer is kept in the log.
	maxResponseBody = 4 << 10
	// jitter is the largest random addition to a retry delay, as a fraction.
	jitter = 0.1
)

// Options configure a Dispatcher. Clock, Signer and IDs are required.
type Options struct {
	Clock  clock.Clock
	Signer *signing.Signer
	IDs    *ids.Generator
	// Retry is the delay before each attempt; its length is the number of
	// attempts. Empty means one attempt.
	Retry []time.Duration
	// Rand drives retry jitter. Nil means random.
	Rand      *rand.Rand
	Client    *http.Client
	Log       *slog.Logger
	UserAgent string
}

// Dispatcher sends events for each payment one at a time, in the order they
// were given, and retries failed attempts. Different payments do not wait for
// each other.
type Dispatcher struct {
	opts Options

	mu         sync.Mutex
	gen        *generation
	queues     map[string]*queue
	deliveries map[string]*Delivery
	byPayment  map[string][]string
	rng        *rand.Rand
}

// generation groups the workers started between two resets.
type generation struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type queue struct {
	jobs []*job
}

type job struct {
	gen        *generation
	deliveries []*Delivery
	plan       Plan
	readyAt    time.Time
	first      chan struct{}
	firstOnce  sync.Once
}

func (j *job) firstDone() { j.firstOnce.Do(func() { close(j.first) }) }

// New returns a dispatcher. Call Close to stop it.
func New(opts Options) *Dispatcher {
	if opts.Client == nil {
		opts.Client = &http.Client{
			Timeout: Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if len(opts.Retry) == 0 {
		opts.Retry = []time.Duration{0}
	}
	if opts.Rand == nil {
		opts.Rand = ids.Rand("", "jitter")
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "psp-sandbox"
	}
	d := &Dispatcher{opts: opts, rng: opts.Rand}
	d.clear()
	return d
}

func (d *Dispatcher) clear() {
	ctx, cancel := context.WithCancel(context.Background())
	d.gen = &generation{ctx: ctx, cancel: cancel}
	d.queues = map[string]*queue{}
	d.deliveries = map[string]*Delivery{}
	d.byPayment = map[string][]string{}
}

// Send queues event e for delivery to url according to plan. The returned
// channel is closed once the first attempt of the first copy has finished
// (or right away if the event is dropped), so a caller can wait until the
// application has seen the callback.
func (d *Dispatcher) Send(e payment.Event, url string, plan Plan) <-chan struct{} {
	j := &job{plan: plan, first: make(chan struct{})}

	body, err := json.Marshal(e)
	if err != nil {
		d.opts.Log.Error("encode event", "event", e.ID, "err", err)
		j.firstDone()
		return j.first
	}

	copies := max(plan.Copies, 1)
	now := d.opts.Clock.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	j.gen = d.gen
	j.readyAt = now.Add(plan.Delay)
	for n := 1; n <= copies; n++ {
		del := &Delivery{
			ID:        d.opts.IDs.Next("dlv"),
			EventID:   e.ID,
			EventType: string(e.Type),
			PaymentID: e.PaymentID,
			URL:       url,
			Copy:      n,
			Status:    StatusPending,
			CreatedAt: now,
			Body:      body,
			Attempts:  []Attempt{},
		}
		if plan.Drop {
			del.Status = StatusDropped
		}
		d.deliveries[del.ID] = del
		d.byPayment[e.PaymentID] = append(d.byPayment[e.PaymentID], del.ID)
		j.deliveries = append(j.deliveries, del)
	}
	if plan.Drop {
		j.firstDone()
		return j.first
	}

	q, running := d.queues[e.PaymentID]
	if !running {
		q = &queue{}
		d.queues[e.PaymentID] = q
		d.gen.wg.Add(1)
		go d.run(d.gen, e.PaymentID, q)
	}
	q.jobs = append(q.jobs, j)
	return j.first
}

// Deliveries returns every delivery of a payment in the order they were queued.
func (d *Dispatcher) Deliveries(paymentID string) []Delivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []Delivery{}
	for _, id := range d.byPayment[paymentID] {
		out = append(out, d.deliveries[id].clone())
	}
	return out
}

// Delivery returns one delivery.
func (d *Dispatcher) Delivery(id string) (Delivery, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	del, ok := d.deliveries[id]
	if !ok {
		return Delivery{}, ErrNotFound
	}
	return del.clone(), nil
}

// Reset stops all pending deliveries and forgets the log.
func (d *Dispatcher) Reset() {
	d.mu.Lock()
	old := d.gen
	d.clear()
	d.mu.Unlock()

	old.cancel()
	old.wg.Wait()
}

// Close stops all pending deliveries and waits for the workers to exit.
func (d *Dispatcher) Close() {
	d.mu.Lock()
	g := d.gen
	d.mu.Unlock()
	g.cancel()
	g.wg.Wait()
}

// run sends the jobs of one payment in order until the queue is empty.
func (d *Dispatcher) run(g *generation, paymentID string, q *queue) {
	defer g.wg.Done()
	for {
		d.mu.Lock()
		if len(q.jobs) == 0 || g.ctx.Err() != nil {
			if d.queues[paymentID] == q {
				delete(d.queues, paymentID)
			}
			for _, j := range q.jobs {
				j.firstDone()
			}
			d.mu.Unlock()
			return
		}
		j := q.jobs[0]
		q.jobs = q.jobs[1:]
		d.mu.Unlock()

		d.process(g.ctx, j)
	}
}

func (d *Dispatcher) process(ctx context.Context, j *job) {
	defer j.firstDone()

	if err := clock.Sleep(ctx, d.opts.Clock, j.readyAt.Sub(d.opts.Clock.Now())); err != nil {
		return
	}

	if j.plan.Parallel {
		var wg sync.WaitGroup
		for i, del := range j.deliveries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.deliver(ctx, del, j, i == 0)
			}()
		}
		wg.Wait()
		return
	}

	for i, del := range j.deliveries {
		if i > 0 {
			if err := clock.Sleep(ctx, d.opts.Clock, j.plan.Interval); err != nil {
				return
			}
		}
		d.deliver(ctx, del, j, i == 0)
	}
}

// deliver makes attempts on the retry schedule until one succeeds.
func (d *Dispatcher) deliver(ctx context.Context, del *Delivery, j *job, first bool) {
	for n, delay := range d.opts.Retry {
		if err := clock.Sleep(ctx, d.opts.Clock, d.withJitter(delay)); err != nil {
			return
		}
		ok := d.attempt(ctx, del, n+1)
		if first {
			j.firstDone()
		}
		if ok {
			d.setStatus(del, StatusSucceeded)
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
	d.setStatus(del, StatusFailed)
}

func (d *Dispatcher) attempt(ctx context.Context, del *Delivery, n int) bool {
	a := Attempt{N: n, At: d.opts.Clock.Now()}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, del.URL, bytes.NewReader(del.Body))
	if err != nil {
		a.Error = err.Error()
		d.record(del, a)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", d.opts.UserAgent)
	// The signature timestamp is wall-clock time even with a manual clock:
	// receivers compare it with their own clock.
	d.opts.Signer.Sign(req.Header, del.EventID, time.Now(), del.Body)

	a.RequestHeaders = make(map[string]string, len(req.Header))
	for k := range req.Header {
		a.RequestHeaders[k] = req.Header.Get(k)
	}

	started := time.Now()
	resp, err := d.opts.Client.Do(req)
	a.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		a.Error = err.Error()
		d.record(del, a)
		d.opts.Log.Info("callback failed", "delivery", del.ID, "event", del.EventID, "attempt", n, "err", err)
		return false
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	a.StatusCode = resp.StatusCode
	a.ResponseBody = string(respBody)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !ok {
		a.Error = fmt.Sprintf("receiver answered %d", resp.StatusCode)
	}
	d.record(del, a)
	d.opts.Log.Info("callback sent", "delivery", del.ID, "event", del.EventID, "type", del.EventType,
		"attempt", n, "status", resp.StatusCode, "latency_ms", a.LatencyMS)
	return ok
}

func (d *Dispatcher) record(del *Delivery, a Attempt) {
	d.mu.Lock()
	defer d.mu.Unlock()
	del.Attempts = append(del.Attempts, a)
}

func (d *Dispatcher) setStatus(del *Delivery, s Status) {
	d.mu.Lock()
	defer d.mu.Unlock()
	del.Status = s
}

func (d *Dispatcher) withJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	d.mu.Lock()
	f := d.rng.Float64()
	d.mu.Unlock()
	return delay + time.Duration(float64(delay)*jitter*f)
}
