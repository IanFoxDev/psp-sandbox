// Package clock abstracts time so delayed scenarios can be driven by tests.
// Real wraps time.Now and timers; Manual only moves forward on Advance.
package clock

import (
	"context"
	"time"
)

// Clock is the source of time for everything the sandbox schedules: status
// changes, delayed callbacks and retries.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f in its own goroutine once d has passed on this clock.
	// A zero or negative d runs f right away, also for a manual clock.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending call scheduled with AfterFunc.
type Timer interface {
	// Stop cancels the call. It reports false if the call already ran or was stopped.
	Stop() bool
}

// Sleep blocks until d has passed on c or ctx is done.
func Sleep(ctx context.Context, c Clock, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	done := make(chan struct{})
	t := c.AfterFunc(d, func() { close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}

// Real is the wall clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}
