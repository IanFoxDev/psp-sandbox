package clock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var start = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func TestManualRunsTimersInDeadlineOrder(t *testing.T) {
	m := NewManual(start)
	var got []string
	m.AfterFunc(3*time.Second, func() { got = append(got, "3s") })
	m.AfterFunc(time.Second, func() { got = append(got, "1s") })
	m.AfterFunc(2*time.Second, func() { got = append(got, "2s-a") })
	m.AfterFunc(2*time.Second, func() { got = append(got, "2s-b") })

	m.Advance(2 * time.Second)
	if want := []string{"1s", "2s-a", "2s-b"}; !equal(got, want) {
		t.Fatalf("after 2s got %v, want %v", got, want)
	}
	if m.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", m.Pending())
	}
	m.Advance(time.Second)
	if len(got) != 4 || got[3] != "3s" {
		t.Fatalf("after 3s got %v", got)
	}
	if !m.Now().Equal(start.Add(3 * time.Second)) {
		t.Fatalf("Now = %v", m.Now())
	}
}

func TestManualNowInsideCallbackIsTheDeadline(t *testing.T) {
	m := NewManual(start)
	var seen time.Time
	m.AfterFunc(time.Minute, func() { seen = m.Now() })
	m.Advance(time.Hour)
	if !seen.Equal(start.Add(time.Minute)) {
		t.Fatalf("Now inside callback = %v, want %v", seen, start.Add(time.Minute))
	}
}

func TestManualTimersScheduledDuringAdvance(t *testing.T) {
	m := NewManual(start)
	var got []string
	m.AfterFunc(time.Second, func() {
		got = append(got, "first")
		m.AfterFunc(time.Second, func() { got = append(got, "second") })
		m.AfterFunc(time.Hour, func() { got = append(got, "later") })
	})
	m.Advance(10 * time.Second)
	if want := []string{"first", "second"}; !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestManualStop(t *testing.T) {
	m := NewManual(start)
	ran := false
	timer := m.AfterFunc(time.Second, func() { ran = true })
	if !timer.Stop() {
		t.Fatal("Stop on a pending timer returned false")
	}
	if timer.Stop() {
		t.Fatal("second Stop returned true")
	}
	m.Advance(time.Minute)
	if ran {
		t.Fatal("stopped timer ran")
	}
}

func TestManualZeroDelayRunsWithoutAdvance(t *testing.T) {
	m := NewManual(start)
	done := make(chan struct{})
	m.AfterFunc(0, func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("zero-delay timer did not run")
	}
}

func TestSleepOnManualClock(t *testing.T) {
	m := NewManual(start)
	errc := make(chan error, 1)
	go func() { errc <- Sleep(context.Background(), m, time.Hour) }()

	waitPending(t, m, 1)
	m.Advance(time.Hour)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestSleepCanceled(t *testing.T) {
	m := NewManual(start)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Sleep(ctx, m, time.Hour) }()

	waitPending(t, m, 1)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if m.Pending() != 0 {
		t.Fatal("canceled Sleep left its timer behind")
	}
}

func TestManualConcurrentUse(t *testing.T) {
	m := NewManual(start)
	var mu sync.Mutex
	fired := 0
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.AfterFunc(time.Duration(i+1)*time.Millisecond, func() {
				mu.Lock()
				fired++
				mu.Unlock()
			})
			_ = m.Now()
		}()
	}
	wg.Wait()
	m.Advance(time.Second)
	if fired != 50 {
		t.Fatalf("fired = %d, want 50", fired)
	}
}

func TestReal(t *testing.T) {
	done := make(chan struct{})
	Real{}.AfterFunc(time.Millisecond, func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("real timer did not fire")
	}
}

func waitPending(t *testing.T, m *Manual, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for m.Pending() != n {
		if time.Now().After(deadline) {
			t.Fatalf("Pending = %d, want %d", m.Pending(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
