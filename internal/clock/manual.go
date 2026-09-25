package clock

import (
	"sort"
	"sync"
	"time"
)

// Manual is a clock that moves only when Advance is called. Timers that become
// due during Advance run in deadline order, synchronously, before Advance returns.
type Manual struct {
	mu     sync.Mutex
	now    time.Time
	seq    uint64
	timers []*manualTimer
}

// NewManual returns a manual clock that starts at start.
func NewManual(start time.Time) *Manual {
	return &Manual{now: start}
}

func (m *Manual) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *Manual) AfterFunc(d time.Duration, f func()) Timer {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seq++
	t := &manualTimer{clock: m, at: m.now.Add(d), seq: m.seq, f: f}
	if d <= 0 {
		t.fired = true
		go f()
		return t
	}
	m.timers = append(m.timers, t)
	return t
}

// Advance moves the clock forward by d and runs every timer that is due.
// Timers scheduled by those callbacks also run if they fall within d.
func (m *Manual) Advance(d time.Duration) {
	m.mu.Lock()
	target := m.now.Add(d)
	for {
		t := m.popDue(target)
		if t == nil {
			break
		}
		m.now = t.at
		m.mu.Unlock()
		t.f()
		m.mu.Lock()
	}
	m.now = target
	m.mu.Unlock()
}

// Pending returns the number of timers that have not run yet.
func (m *Manual) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.timers)
}

// popDue removes and returns the earliest timer due at or before target.
func (m *Manual) popDue(target time.Time) *manualTimer {
	if len(m.timers) == 0 {
		return nil
	}
	sort.Slice(m.timers, func(i, j int) bool {
		a, b := m.timers[i], m.timers[j]
		if a.at.Equal(b.at) {
			return a.seq < b.seq
		}
		return a.at.Before(b.at)
	})
	t := m.timers[0]
	if t.at.After(target) {
		return nil
	}
	m.timers = m.timers[1:]
	t.fired = true
	return t
}

type manualTimer struct {
	clock *Manual
	at    time.Time
	seq   uint64
	f     func()
	fired bool
}

func (t *manualTimer) Stop() bool {
	m := t.clock
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.fired {
		return false
	}
	for i, other := range m.timers {
		if other == t {
			m.timers = append(m.timers[:i], m.timers[i+1:]...)
			t.fired = true
			return true
		}
	}
	return false
}
